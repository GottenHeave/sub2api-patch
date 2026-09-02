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
    --zero-commit --no-signature --numbered --stat --unified=2 > "$output"
}

format_head_without_stat() {
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

diff_target_ref=7070
diff_target_patch="$tmp/diff-target.patch"
sed "s|diff --git a/file.txt b/file.txt|diff --git a/file.txt b/ref-#$diff_target_ref.txt|" \
  "$valid_patch" > "$diff_target_patch"
git apply --numstat "$diff_target_patch" >/dev/null
if python3 "$sanitizer" --check "$diff_target_patch" 2>"$tmp/diff-target.stderr"; then
  echo 'sanitizer accepted a forbidden reference in a diff target path' >&2
  exit 1
fi
grep -q 'invalid Git diff destination header' "$tmp/diff-target.stderr"

embedded_target_ref=7171
embedded_target_patch="$tmp/embedded-target.patch"
sed "s|diff --git a/file.txt b/file.txt|diff --git a/file.txt b/ref-#$embedded_target_ref b/file.txt|" \
  "$valid_patch" > "$embedded_target_patch"
git apply --numstat "$embedded_target_patch" >/dev/null
if python3 "$sanitizer" --check "$embedded_target_patch" \
  2>"$tmp/embedded-target.stderr"; then
  echo 'sanitizer accepted an ambiguous forbidden diff target path' >&2
  exit 1
fi
grep -q 'invalid Git diff destination header' "$tmp/embedded-target.stderr"

plus_target_ref=8080
plus_target_patch="$tmp/plus-target.patch"
sed "s|+++ b/file.txt|+++ b/ref-#$plus_target_ref.txt|" \
  "$valid_patch" > "$plus_target_patch"
git apply --numstat "$plus_target_patch" >/dev/null
if python3 "$sanitizer" --check "$plus_target_patch" 2>"$tmp/plus-target.stderr"; then
  echo 'sanitizer accepted a forbidden reference in a plus target path' >&2
  exit 1
fi
grep -q 'invalid Git diff destination header' "$tmp/plus-target.stderr"

malformed_patch="$tmp/malformed.patch"
sed '$d' "$valid_patch" > "$malformed_patch"
if python3 "$sanitizer" --check "$malformed_patch" 2>"$tmp/malformed.stderr"; then
  echo 'sanitizer accepted malformed Git patch structure' >&2
  exit 1
fi
grep -q 'invalid Git patch structure' "$tmp/malformed.stderr"

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
printf 'capability\n\ndiff --git a/fake b/fake\nindex 1111111..2222222 100644\n--- a/fake\n+++ b/fake\n@@ -1 +1 @@\n-old\n+new\nissue #%s\n' "$body_ref" \
  > "$tmp/commit-message"
git -C "$body_repo" add file.txt
git -C "$body_repo" commit -qF "$tmp/commit-message"
format_head "$body_repo" "$body_patch"
if python3 "$sanitizer" --check "$body_patch" 2>"$tmp/body.stderr"; then
  echo 'sanitizer accepted a forbidden reference after a diff-like commit-body line' >&2
  exit 1
fi
grep -q 'blocked pull request or issue reference' "$tmp/body.stderr"

ambiguous_patch="$tmp/ambiguous.patch"
format_head_without_stat "$body_repo" "$ambiguous_patch"
if python3 "$sanitizer" --check "$ambiguous_patch" 2>"$tmp/ambiguous.stderr"; then
  echo 'sanitizer accepted separator-less patch metadata as a diff' >&2
  exit 1
fi
grep -q 'canonical format-patch separator' "$tmp/ambiguous.stderr"

assert_blocked_new_path() {
  local name="$1"
  local filename="$2"
  local root="$tmp/path-$name"
  local patch="$tmp/path-$name.patch"
  new_repo "$root"
  printf 'base\n' > "$root/base.txt"
  git -C "$root" add base.txt
  git -C "$root" commit -qm base
  mkdir -p "$(dirname "$root/$filename")"
  printf 'safe content\n' > "$root/$filename"
  git -C "$root" add .
  git -C "$root" commit -qm capability
  format_head "$root" "$patch"
  if python3 "$sanitizer" --check "$patch" 2>"$tmp/path-$name.stderr"; then
    echo "sanitizer accepted a forbidden reference in $name destination path" >&2
    exit 1
  fi
  grep -q 'blocked pull request or issue reference' "$tmp/path-$name.stderr"
}

path_ref=4242
path_filename="$(printf 'issue #%s\t.txt' "$path_ref")"
assert_blocked_new_path normal "ref-#$path_ref.txt"
assert_blocked_new_path quoted "$path_filename"
assert_blocked_new_path issues "refs/issues/$path_ref.txt"
assert_blocked_new_path pull "refs/pull/$path_ref.txt"
grep -q '^diff --git "a/issue #' "$tmp/path-quoted.patch"

rename_repo="$tmp/rename"
rename_patch="$tmp/rename.patch"
rename_filename="$(printf 'PR #%s\t.txt' 4243)"
new_repo "$rename_repo"
printf 'safe content\n' > "$rename_repo/original.txt"
git -C "$rename_repo" add original.txt
git -C "$rename_repo" commit -qm base
git -C "$rename_repo" mv original.txt "$rename_filename"
git -C "$rename_repo" commit -qm capability
format_head "$rename_repo" "$rename_patch"
grep -q '^rename to "PR #' "$rename_patch"
if python3 "$sanitizer" --check "$rename_patch" 2>"$tmp/rename.stderr"; then
  echo 'sanitizer accepted a forbidden reference in a rename destination' >&2
  exit 1
fi
grep -q 'blocked pull request or issue reference' "$tmp/rename.stderr"

copy_repo="$tmp/copy"
copy_patch="$tmp/copy.patch"
copy_filename="$(printf 'pull request #%s\t.txt' 4244)"
new_repo "$copy_repo"
printf 'content copied through Git detection\n' > "$copy_repo/original.txt"
git -C "$copy_repo" add original.txt
git -C "$copy_repo" commit -qm base
cp "$copy_repo/original.txt" "$copy_repo/$copy_filename"
git -C "$copy_repo" add .
git -C "$copy_repo" commit -qm capability
git -C "$copy_repo" format-patch -1 --stdout \
  --zero-commit --no-signature --numbered --stat --unified=2 \
  --find-copies-harder -C > "$copy_patch"
grep -q '^copy to "pull request #' "$copy_patch"
if python3 "$sanitizer" --check "$copy_patch" 2>"$tmp/copy.stderr"; then
  echo 'sanitizer accepted a forbidden reference in a copy destination' >&2
  exit 1
fi
grep -q 'blocked pull request or issue reference' "$tmp/copy.stderr"

removed_ref=9090
removed_filename="$(printf 'issue #%s\t.txt' "$removed_ref")"
delete_repo="$tmp/delete"
delete_patch="$tmp/delete.patch"
new_repo "$delete_repo"
printf 'safe content\n' > "$delete_repo/$removed_filename"
git -C "$delete_repo" add .
git -C "$delete_repo" commit -qm base
git -C "$delete_repo" rm -q "$removed_filename"
git -C "$delete_repo" commit -qm capability
format_head "$delete_repo" "$delete_patch"
python3 "$sanitizer" --check "$delete_patch"

rename_away_repo="$tmp/rename-away"
rename_away_patch="$tmp/rename-away.patch"
new_repo "$rename_away_repo"
printf 'safe content\n' > "$rename_away_repo/$removed_filename"
git -C "$rename_away_repo" add .
git -C "$rename_away_repo" commit -qm base
git -C "$rename_away_repo" mv "$removed_filename" safe.txt
git -C "$rename_away_repo" commit -qm capability
format_head "$rename_away_repo" "$rename_away_patch"
python3 "$sanitizer" --check "$rename_away_patch"

replay_repo="$tmp/replay"
git clone -q "$valid_repo" "$replay_repo"
git -C "$replay_repo" config user.name test
git -C "$replay_repo" config user.email test@example.com
git -C "$replay_repo" reset -q --hard HEAD^
git -C "$replay_repo" am -q "$valid_patch"
test "$(git -C "$replay_repo" rev-parse 'HEAD^{tree}')" = \
  "$(git -C "$valid_repo" rev-parse 'HEAD^{tree}')"

echo 'patch sanitizer regressions passed'
