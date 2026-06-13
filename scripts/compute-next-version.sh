#!/usr/bin/env bash
set -euo pipefail

worktree="${1:-.}"
release_repo="${RELEASE_REPOSITORY:-${GITHUB_REPOSITORY:-}}"

if [ -z "$release_repo" ]; then
  release_repo="$(gh repo view --json nameWithOwner --jq .nameWithOwner)"
fi

cd "$worktree"
upstream_version="$(tr -d '[:space:]' < backend/cmd/server/VERSION)"
base="v${upstream_version}"

release_versions="$(gh release list --repo "$release_repo" --limit 200 2>/dev/null | awk '{print $1}' | grep -E "^${base}-patch\\.[0-9]+$" || true)"
tag_versions="$(gh api "repos/${release_repo}/git/matching-refs/tags/${base}-patch." --jq '.[].ref' 2>/dev/null | sed 's#^refs/tags/##' | grep -E "^${base}-patch\\.[0-9]+$" || true)"

last="$(printf '%s\n%s\n' "$release_versions" "$tag_versions" | sed '/^$/d' | sed -E "s/^${base}-patch\.([0-9]+)$/\1/" | sort -n | tail -1)"
if [ -z "$last" ]; then
  next=1
else
  next=$((last + 1))
fi
candidate="${base}-patch.${next}"

if printf '%s\n%s\n' "$release_versions" "$tag_versions" | grep -Fxq "$candidate"; then
  echo "computed version already exists in ${release_repo}: ${candidate}" >&2
  exit 1
fi

printf '%s\n' "$candidate"
