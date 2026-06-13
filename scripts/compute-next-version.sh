#!/usr/bin/env bash
set -euo pipefail

worktree="${1:-.}"
cd "$worktree"
upstream_version="$(tr -d '[:space:]' < backend/cmd/server/VERSION)"
base="v${upstream_version}"

release_versions="$(gh release list --limit 200 2>/dev/null | awk '{print $1}' | grep -E "^${base}-patch\\.[0-9]+$" || true)"

git fetch --tags origin >/dev/null 2>&1 || true
tag_versions="$(git tag -l "${base}-patch.*" | grep -E "^${base}-patch\\.[0-9]+$" || true)"

last="$(printf '%s\n%s\n' "$release_versions" "$tag_versions" | sed '/^$/d' | sed -E "s/^${base}-patch\.([0-9]+)$/\1/" | sort -n | tail -1)"
if [ -z "$last" ]; then
  next=1
else
  next=$((last + 1))
fi
candidate="${base}-patch.${next}"

if git rev-parse -q --verify "refs/tags/${candidate}" >/dev/null; then
  echo "computed version already exists as a tag: ${candidate}" >&2
  exit 1
fi

printf '%s\n' "$candidate"
