#!/usr/bin/env bash
set -euo pipefail

worktree="${1:?usage: scripts/refresh-patches.sh /path/to/patched-worktree [base-ref]}"
base_ref="${2:-upstream/main}"
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
patch_dir="$repo_root/patches/cur"

rm -f "$patch_dir"/*.patch
cd "$worktree"

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
