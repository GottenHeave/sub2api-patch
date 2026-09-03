#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

repository="$tmp/repository"
git init -q "$repository"
git -C "$repository" config user.name test
git -C "$repository" config user.email test@example.com

commit_file() {
  local path="$1"
  local contents="$2"
  local message="$3"

  mkdir -p "$repository/$(dirname "$path")"
  printf '%s\n' "$contents" > "$repository/$path"
  git -C "$repository" add -A
  git -C "$repository" commit -qm "$message"
  git -C "$repository" rev-parse 'HEAD^{commit}'
}

assert_changed() {
  local expected="$1"
  local upstream_sha="$2"
  local candidate_tree="$3"
  local publication_tree="$4"
  local current_main="$5"
  local current_mirror="$6"
  local current_patched="$7"
  local actual

  actual="$("$repo_root/scripts/sync-published-state-changed.sh" \
    "$repository" "$upstream_sha" "$candidate_tree" "$publication_tree" \
    "$current_main" "$current_mirror" "$current_patched")"
  if [ "$actual" != "$expected" ]; then
    printf 'expected changed=%s, got %s\n' "$expected" "$actual" >&2
    exit 1
  fi
}

upstream_a="$(commit_file product.txt product-v1 upstream-a)"
workflow_a="$(commit_file .github/workflows/upstream-pr-check.yml workflow-v1 workflow-a)"
candidate_tree_a="$(git -C "$repository" rev-parse "${workflow_a}^{tree}")"

old_main="$(git -C "$repository" commit-tree "${upstream_a}^{tree}" -p "$upstream_a" <<< 'old main')"
published_main="$(
  git -C "$repository" commit-tree "$candidate_tree_a" \
    -p "$old_main" -p "$workflow_a" <<< 'published main'
)"
published_patched="$(git -C "$repository" commit-tree "${upstream_a}^{tree}" -p "$upstream_a" <<< 'patched')"
publication_tree_a="$(git -C "$repository" rev-parse "${published_patched}^{tree}")"

# A fully represented source, installed workflow, mirror, and product is a no-op.
assert_changed false "$upstream_a" "$candidate_tree_a" "$publication_tree_a" \
  "$published_main" "$published_main" "$published_patched"

# Changing only the installed patchset workflow requires validation and release.
git -C "$repository" checkout -q --detach "$workflow_a"
workflow_b="$(commit_file .github/workflows/upstream-pr-check.yml workflow-v2 workflow-b)"
candidate_tree_b="$(git -C "$repository" rev-parse "${workflow_b}^{tree}")"
assert_changed true "$upstream_a" "$candidate_tree_b" "$publication_tree_a" \
  "$published_main" "$published_main" "$published_patched"

# A new selected upstream commit with the same tree still changes published ancestry.
upstream_b="$(git -C "$repository" commit-tree "${upstream_a}^{tree}" -p "$upstream_a" <<< 'upstream-b')"
assert_changed true "$upstream_b" "$candidate_tree_a" "$publication_tree_a" \
  "$published_main" "$published_main" "$published_patched"

# A patch-only product change requires validation and release.
git -C "$repository" checkout -q --detach "$upstream_a"
patched_b="$(commit_file product.txt product-v2 patched-b)"
publication_tree_b="$(git -C "$repository" rev-parse "${patched_b}^{tree}")"
assert_changed true "$upstream_a" "$candidate_tree_a" "$publication_tree_b" \
  "$published_main" "$published_main" "$published_patched"

# Missing or divergent publication refs fail closed.
assert_changed true "$upstream_a" "$candidate_tree_a" "$publication_tree_a" \
  "$published_main" "" "$published_patched"
assert_changed true "$upstream_a" "$candidate_tree_a" "$publication_tree_a" \
  "$published_main" "$published_main" ""
assert_changed true "$upstream_a" "$candidate_tree_a" "$publication_tree_a" \
  "$published_main" "$old_main" "$published_patched"

printf 'sync published-state change fixtures passed\n'
