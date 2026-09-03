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
prefix="${base}-patch."

filter_versions() {
  local input="$1"
  local ref_format="${2:-names}"
  local value suffix

  while read -r value _; do
    if [ "$ref_format" = refs ]; then
      value="${value#refs/tags/}"
    fi
    case "$value" in
      "$prefix"*)
        suffix="${value#"$prefix"}"
        if [[ "$suffix" =~ ^[0-9]+$ ]]; then
          printf '%s\n' "$value"
        fi
        ;;
    esac
  done <<< "$input"
}

# A failed query leaves the namespace unknown, so it must stop versioning.
release_rows="$(gh release list --repo "$release_repo" --limit 1000 \
  --json tagName --jq '.[].tagName')"
remote_tag_refs="$(gh api "repos/${release_repo}/git/matching-refs/tags/${prefix}" --jq '.[].ref')"
local_tag_names="$(git tag --list "${prefix}*")"

release_versions="$(filter_versions "$release_rows")"
remote_tag_versions="$(filter_versions "$remote_tag_refs" refs)"
local_tag_versions="$(filter_versions "$local_tag_names")"
all_versions="$(printf '%s\n%s\n%s\n' \
  "$release_versions" "$remote_tag_versions" "$local_tag_versions")"

last="$(printf '%s\n' "$all_versions" | sed '/^$/d' | sed -E "s/^${base}-patch\.([0-9]+)$/\1/" | sort -n | tail -1)"
if [ -z "$last" ]; then
  next=1
else
  next=$((last + 1))
fi
candidate="${base}-patch.${next}"

if printf '%s\n' "$all_versions" | grep -Fxq "$candidate"; then
  echo "computed version already exists in ${release_repo}: ${candidate}" >&2
  exit 1
fi

printf '%s\n' "$candidate"
