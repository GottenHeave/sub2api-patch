#!/usr/bin/env bash
set -euo pipefail

source_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

new_metadata_repo() {
  local root="$1"
  mkdir -p "$root/scripts" "$root/patches/cur"
  cp "$source_root/scripts/refresh-patches.sh" "$root/scripts/"
  cp "$source_root/scripts/sanitize-patches.py" "$root/scripts/"
  cp "$source_root/scripts/check-no-pr-issue-refs.sh" "$root/scripts/"
  printf 'existing patch series\n' > "$root/patches/cur/existing.patch"
}

new_worktree() {
  local root="$1"
  local base_line="$2"
  git init -q "$root"
  git -C "$root" config user.name test
  git -C "$root" config user.email test@example.com
  printf '%s\n' "$base_line" > "$root/file.txt"
  git -C "$root" add file.txt
  git -C "$root" commit -qm base
}

context_metadata="$tmp/context-metadata"
context_worktree="$tmp/context-worktree"
new_metadata_repo "$context_metadata"
new_worktree "$context_worktree" "$(printf 'unchanged upstream issue #%s' 5056)"
context_base="$(git -C "$context_worktree" rev-parse HEAD)"
printf 'downstream behavior\n' >> "$context_worktree/file.txt"
git -C "$context_worktree" commit -qam capability
"$context_metadata/scripts/refresh-patches.sh" "$context_worktree" "$context_base"
test ! -e "$context_metadata/patches/cur/existing.patch"
test "$(find "$context_metadata/patches/cur" -type f -name '*.patch' | wc -l)" -eq 1

failure_metadata="$tmp/failure-metadata"
failure_worktree="$tmp/failure-worktree"
new_metadata_repo "$failure_metadata"
cp -a "$failure_metadata/patches/cur" "$tmp/original-series"
new_worktree "$failure_worktree" 'upstream behavior'
failure_base="$(git -C "$failure_worktree" rev-parse HEAD)"
printf 'downstream issue #%s\n' 123 >> "$failure_worktree/file.txt"
git -C "$failure_worktree" commit -qam capability
if "$failure_metadata/scripts/refresh-patches.sh" "$failure_worktree" "$failure_base" \
  >"$tmp/failure.stdout" 2>"$tmp/failure.stderr"; then
  echo 'refresh accepted a downstream-added forbidden reference' >&2
  exit 1
fi
grep -q 'blocked pull request or issue reference' "$tmp/failure.stderr"
diff -ru "$tmp/original-series" "$failure_metadata/patches/cur"
test -z "$(find "$failure_metadata/patches" -maxdepth 1 -type d -name '.*' -print)"

malformed_metadata="$tmp/malformed-metadata"
malformed_worktree="$tmp/malformed-worktree"
fake_bin="$tmp/fake-bin"
new_metadata_repo "$malformed_metadata"
cp -a "$malformed_metadata/patches/cur" "$tmp/malformed-original-series"
new_worktree "$malformed_worktree" 'upstream behavior'
malformed_base="$(git -C "$malformed_worktree" rev-parse HEAD)"
printf 'downstream behavior\n' >> "$malformed_worktree/file.txt"
git -C "$malformed_worktree" commit -qam capability
mkdir -p "$fake_bin"
real_git="$(command -v git)"
sed \
  -e "s|@REAL_GIT@|$real_git|g" \
  -e "s|@TARGET_REPO@|$malformed_worktree|g" \
  "$source_root/scripts/tests/fixtures/corrupt-format-patch-git.sh" \
  > "$fake_bin/git"
chmod +x "$fake_bin/git"
if PATH="$fake_bin:$PATH" \
  "$malformed_metadata/scripts/refresh-patches.sh" \
  "$malformed_worktree" "$malformed_base" \
  >"$tmp/malformed.stdout" 2>"$tmp/malformed.stderr"; then
  echo 'refresh replaced the series with an unreplayable patch' >&2
  exit 1
fi
grep -Eq 'Patch failed|does not exist in index' "$tmp/malformed.stderr"
diff -ru "$tmp/malformed-original-series" "$malformed_metadata/patches/cur"
test -z "$(find "$malformed_metadata/patches" -maxdepth 1 -type d -name '.*' -print)"

post_swap_metadata="$tmp/post-swap-metadata"
post_swap_worktree="$tmp/post-swap-worktree"
new_metadata_repo "$post_swap_metadata"
cp -a "$post_swap_metadata/patches/cur" "$tmp/post-swap-original-series"
printf 'blocked issue #%s\n' 999 > "$post_swap_metadata/scripts/marker.sh"
new_worktree "$post_swap_worktree" 'upstream behavior'
post_swap_base="$(git -C "$post_swap_worktree" rev-parse HEAD)"
printf 'downstream behavior\n' >> "$post_swap_worktree/file.txt"
git -C "$post_swap_worktree" commit -qam capability
if "$post_swap_metadata/scripts/refresh-patches.sh" \
  "$post_swap_worktree" "$post_swap_base" \
  >"$tmp/post-swap.stdout" 2>"$tmp/post-swap.stderr"; then
  echo 'refresh retained a new series after repository validation failed' >&2
  exit 1
fi
grep -q 'blocked pull request, issue, or mention reference found' \
  "$tmp/post-swap.stderr"
diff -ru "$tmp/post-swap-original-series" "$post_swap_metadata/patches/cur"
test -z "$(find "$post_swap_metadata/patches" -maxdepth 1 -type d -name '.*' -print)"
test -z "$(find "$post_swap_metadata" -maxdepth 1 -type d -name '.cur-backup.*' -print)"

echo 'refresh patch regressions passed'
