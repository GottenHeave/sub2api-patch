#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
sanitizer="$repo_root/scripts/sanitize-patches.py"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

new_repo() {
  local root="$1"
  git init -q "$root"
  git -C "$root" config user.name test
  git -C "$root" config user.email test@example.com
}

format_head() {
  local root="$1"
  local output="$2"
  git -C "$root" format-patch -1 --stdout \
    --zero-commit --no-signature --no-numbered --no-stat > "$output"
}

valid_repo="$tmp/valid"
valid_patch="$tmp/valid.patch"
context_ref=5056
deleted_ref=6060
new_repo "$valid_repo"
printf 'first\nunchanged issue #%s\ndeleted issue #%s\nlast\n' \
  "$context_ref" "$deleted_ref" > "$valid_repo/file.txt"
git -C "$valid_repo" add file.txt
git -C "$valid_repo" commit -qm base
printf 'first\nunchanged issue #%s\nreplacement\nlast\ndownstream behavior\n' \
  "$context_ref" > "$valid_repo/file.txt"
git -C "$valid_repo" commit -qam capability
format_head "$valid_repo" "$valid_patch"
grep -q "^ unchanged issue #$context_ref" "$valid_patch"
grep -q "^-deleted issue #$deleted_ref" "$valid_patch"
python3 "$sanitizer" --check "$valid_patch"

added_repo="$tmp/added"
added_patch="$tmp/added.patch"
added_ref=123
new_repo "$added_repo"
printf 'base\n' > "$added_repo/file.txt"
git -C "$added_repo" add file.txt
git -C "$added_repo" commit -qm base
printf 'downstream issue #%s\n' "$added_ref" >> "$added_repo/file.txt"
git -C "$added_repo" commit -qam capability
format_head "$added_repo" "$added_patch"
if python3 "$sanitizer" --check "$added_patch" 2>"$tmp/added.stderr"; then
  echo 'sanitizer accepted an added forbidden reference' >&2
  exit 1
fi
grep -q 'blocked pull request or issue reference' "$tmp/added.stderr"

body_repo="$tmp/body"
body_patch="$tmp/body.patch"
body_ref=321
new_repo "$body_repo"
printf 'base\n' > "$body_repo/file.txt"
git -C "$body_repo" add file.txt
git -C "$body_repo" commit -qm base
printf 'safe addition\n' >> "$body_repo/file.txt"
printf 'capability\n\ndiff --git a/fake b/fake\nissue #%s\n' "$body_ref" \
  > "$tmp/commit-message"
git -C "$body_repo" add file.txt
git -C "$body_repo" commit -qF "$tmp/commit-message"
format_head "$body_repo" "$body_patch"
if python3 "$sanitizer" --check "$body_patch" 2>"$tmp/body.stderr"; then
  echo 'sanitizer accepted a forbidden reference after a diff-like commit-body line' >&2
  exit 1
fi
grep -q 'blocked pull request or issue reference' "$tmp/body.stderr"

echo 'patch sanitizer regressions passed'
