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

echo 'refresh patch regressions passed'
