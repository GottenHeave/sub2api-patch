#!/usr/bin/env bash
set -euo pipefail

worktree="${1:?usage: scripts/refresh-patches.sh /path/to/patched-worktree [base-ref]}"
base_ref="${2:-upstream/main}"
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
patch_dir="$repo_root/patches/cur"

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

rm -f "$patch_dir"/*.patch

git format-patch \
  --zero-commit \
  --no-signature \
  --no-numbered \
  --no-stat \
  -o "$patch_dir" \
  "$base_ref"..HEAD

cd "$repo_root"
python3 scripts/sanitize-patches.py patches/cur/*.patch
scripts/check-no-pr-issue-refs.sh "$repo_root"
