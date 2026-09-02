#!/usr/bin/env bash
set -euo pipefail

worktree="${1:?usage: scripts/refresh-patches.sh /path/to/patched-worktree [base-ref]}"
base_ref="${2:-upstream/main}"
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
patch_dir="$repo_root/patches/cur"
staging_dir=""
backup_dir=""
validation_dir=""

cleanup() {
  if [ -n "$backup_dir" ] && [ -d "$backup_dir" ]; then
    rm -rf "$patch_dir"
    mv "$backup_dir" "$patch_dir"
  fi
  if [ -n "$staging_dir" ] && [ -d "$staging_dir" ]; then
    rm -rf "$staging_dir"
  fi
  if [ -n "$validation_dir" ] && [ -d "$validation_dir" ]; then
    rm -rf "$validation_dir"
  fi
}
trap cleanup EXIT

cd "$worktree"

if [ -n "$(git status --porcelain)" ]; then
  echo "patched worktree is dirty: $worktree" >&2
  git status --short >&2
  exit 1
fi

if [ -n "${EXPECTED_BASE_SHA:-}" ]; then
  actual_base_sha="$(git rev-parse "${base_ref}^{commit}")"
  expected_base_sha="$(git rev-parse "${EXPECTED_BASE_SHA}^{commit}")"
  if [ "$actual_base_sha" != "$expected_base_sha" ]; then
    echo "refresh base does not match EXPECTED_BASE_SHA: expected $expected_base_sha, got $actual_base_sha" >&2
    exit 1
  fi
fi
base_sha="$(git rev-parse "${base_ref}^{commit}")"
expected_tree="$(git rev-parse 'HEAD^{tree}')"

staging_dir="$(mktemp -d "$repo_root/patches/.refresh.XXXXXX")"

python3 "$repo_root/scripts/format-patch-series.py" \
  --repo "$worktree" \
  --base-ref "$base_ref" \
  --output "$staging_dir"

cd "$repo_root"
shopt -s nullglob
staged_patches=("$staging_dir"/*.patch)
if [ "${#staged_patches[@]}" -eq 0 ]; then
  echo "refresh produced no patches for $base_ref..HEAD" >&2
  exit 1
fi

python3 scripts/sanitize-patches.py \
  --repo "$worktree" \
  --base-ref "$base_sha" \
  "${staged_patches[@]}"

validation_dir="$(mktemp -d "$repo_root/patches/.replay.XXXXXX")"
git clone --quiet --no-checkout "$worktree" "$validation_dir"
git -C "$validation_dir" config user.name "patch replay validation"
git -C "$validation_dir" config user.email "patch-replay@example.invalid"
git -C "$validation_dir" config core.hooksPath /dev/null
git -C "$validation_dir" checkout --quiet --detach "$base_sha"
git -C "$validation_dir" -c core.hooksPath=/dev/null \
  am --quiet --no-3way "${staged_patches[@]}"
actual_tree="$(git -C "$validation_dir" rev-parse 'HEAD^{tree}')"
if [ "$actual_tree" != "$expected_tree" ]; then
  echo "refreshed patches do not reproduce the patched tree" >&2
  exit 1
fi
rm -rf "$validation_dir"
validation_dir=""

backup_dir="$(mktemp -d "$repo_root/.cur-backup.XXXXXX")"
rmdir "$backup_dir"
mv "$patch_dir" "$backup_dir"
mv "$staging_dir" "$patch_dir"
staging_dir=""
PATCH_BASE_REPO="$worktree" EXPECTED_BASE_SHA="$base_sha" \
  scripts/check-no-pr-issue-refs.sh "$repo_root"
rm -rf "$backup_dir"
backup_dir=""
