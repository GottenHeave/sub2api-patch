#!/usr/bin/env bash
set -euo pipefail

worktree="${1:-.}"
patch_dir="${PATCH_DIR:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/patches/cur}"

cd "$worktree"

if [ -n "$(git status --porcelain)" ]; then
  echo "target worktree is dirty: $worktree" >&2
  git status --short >&2
  exit 1
fi

if [ -n "${EXPECTED_BASE_SHA:-}" ]; then
  actual_base_sha="$(git rev-parse HEAD^{commit})"
  expected_base_sha="$(git rev-parse "${EXPECTED_BASE_SHA}^{commit}")"
  if [ "$actual_base_sha" != "$expected_base_sha" ]; then
    echo "target base does not match EXPECTED_BASE_SHA: expected $expected_base_sha, got $actual_base_sha" >&2
    exit 1
  fi
fi

git config rerere.enabled true

if ! git config user.name >/dev/null; then
  git config user.name "sub2api-patch automation"
fi
if ! git config user.email >/dev/null; then
  git config user.email "actions@users.noreply.github.com"
fi

shopt -s nullglob
patches=("$patch_dir"/*.patch)
if [ "${#patches[@]}" -eq 0 ]; then
  echo "no patches found in $patch_dir" >&2
  exit 1
fi

for patch in "${patches[@]}"; do
  echo "==== applying $(basename "$patch") ===="
  if ! git am --3way "$patch"; then
    echo "patch failed: $(basename "$patch")" >&2
    git status --short >&2 || true
    git diff --name-only --diff-filter=U >&2 || true
    exit 1
  fi
done
