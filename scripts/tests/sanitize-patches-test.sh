#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
sanitizer="$repo_root/scripts/sanitize-patches.py"
formatter="$repo_root/scripts/format-patch-series.py"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

new_repo() {
  local root="$1"
  git init -q "$root"
  git -C "$root" config user.name test
  git -C "$root" config user.email test@example.com
  printf 'base\n' > "$root/file.txt"
  git -C "$root" add file.txt
  git -C "$root" commit -qm base
}

format_series() {
  local root="$1"
  local base="$2"
  local output="$3"
  python3 "$formatter" --repo "$root" --base-ref "$base" --output "$output"
}

format_series_without_stat() {
  local root="$1"
  local base="$2"
  local output="$3"
  mkdir -p "$output"
  LC_ALL=C git -C "$root" -c core.attributesFile=/dev/null format-patch \
    --zero-commit \
    --no-signature \
    --numbered \
    --no-stat \
    --unified=1 \
    --no-renames \
    --src-prefix=a/ \
    --dst-prefix=b/ \
    -o "$output" \
    "$base..HEAD" >/dev/null
}

only_patch() {
  local output="$1"
  local patches=("$output"/*.patch)
  test "${#patches[@]}" -eq 1
  printf '%s\n' "${patches[0]}"
}

assert_accepted() {
  local name="$1"
  local root="$2"
  local base="$3"
  shift 3
  if ! python3 "$sanitizer" --check --repo "$root" --base-ref "$base" "$@" \
    2> "$tmp/$name.stderr"; then
    echo "sanitizer rejected valid fixture: $name" >&2
    cat "$tmp/$name.stderr" >&2
    exit 1
  fi
}

assert_rejected() {
  local name="$1"
  local expected="$2"
  local root="$3"
  local base="$4"
  shift 4
  if python3 "$sanitizer" --check --repo "$root" --base-ref "$base" "$@" \
    2> "$tmp/$name.stderr"; then
    echo "sanitizer accepted invalid fixture: $name" >&2
    exit 1
  fi
  if ! grep -Eq "$expected" "$tmp/$name.stderr"; then
    echo "sanitizer rejected $name for an unexpected reason" >&2
    cat "$tmp/$name.stderr" >&2
    exit 1
  fi
}

assert_hooks_suppressed() {
  local name="$1"
  local config="$2"
  local marker="$3"
  rm -f "$marker"
  if ! GIT_CONFIG_GLOBAL="$config" python3 "$sanitizer" \
    --check \
    --repo "$safe_root" \
    --base-ref "$safe_base" \
    "$safe_patch" 2> "$tmp/$name.stderr"; then
    echo "sanitizer rejected the $name hook fixture" >&2
    cat "$tmp/$name.stderr" >&2
    exit 1
  fi
  if [[ -e "$marker" ]]; then
    echo "sanitizer replay executed the $name post-checkout hook" >&2
    exit 1
  fi
}

commit_safe_change() {
  local root="$1"
  local subject="$2"
  local body="${3:-}"
  printf 'safe addition\n' >> "$root/file.txt"
  git -C "$root" add file.txt
  if [[ -n "$body" ]]; then
    git -C "$root" commit -qm "$subject" -m "$body"
  else
    git -C "$root" commit -qm "$subject"
  fi
}

replace_subject_with_base64() {
  local patch="$1"
  local subject="$2"
  local encoded
  encoded="$(printf '%s' "$subject" | base64 -w0)"
  awk -v encoded="$encoded" '
    /^Subject: / {
      print "Subject: =?UTF-8?B?" encoded "?="
      in_subject = 1
      next
    }
    in_subject && /^[[:space:]]/ { next }
    { in_subject = 0; print }
  ' "$patch" > "$patch.new"
  mv "$patch.new" "$patch"
}

encode_body_quoted_printable() {
  local patch="$1"
  local reference="$2"
  awk '
    in_headers && /^$/ {
      print "MIME-Version: 1.0"
      print "Content-Type: text/plain; charset=UTF-8"
      print "Content-Transfer-Encoding: quoted-printable"
      in_headers = 0
    }
    NR == 1 { in_headers = 1 }
    { print }
  ' "$patch" | sed "s/#$reference/=23$reference/g" > "$patch.new"
  mv "$patch.new" "$patch"
}

encode_body_base64() {
  local patch="$1"
  awk '
    BEGIN { in_headers = 1 }
    in_headers && /^$/ {
      print "MIME-Version: 1.0"
      print "Content-Type: text/plain; charset=UTF-8"
      print "Content-Transfer-Encoding: base64"
      print
      in_headers = 0
      next
    }
    in_headers { print }
  ' "$patch" > "$patch.new"
  awk 'found { print } /^$/ { found = 1 }' "$patch" | base64 >> "$patch.new"
  mv "$patch.new" "$patch"
}

# Canonical safe patch with forbidden references only in unchanged and deleted
# upstream lines. Applied added-line inspection must leave both cases allowed.
safe_root="$tmp/safe-content"
context_ref=6001
deleted_content_ref=6002
new_repo "$safe_root"
printf 'unchanged issue #%s\ndeleted pull request #%s\n' \
  "$context_ref" "$deleted_content_ref" >> "$safe_root/file.txt"
git -C "$safe_root" add file.txt
git -C "$safe_root" commit --amend -qm base
safe_base="$(git -C "$safe_root" rev-parse HEAD)"
printf 'base\nunchanged issue #%s\nsafe replacement\nsafe addition\n' \
  "$context_ref" > "$safe_root/file.txt"
git -C "$safe_root" commit -qam 'safe applied content'
format_series "$safe_root" "$safe_base" "$tmp/safe-content-patches"
safe_patch="$(only_patch "$tmp/safe-content-patches")"
grep -q "^ unchanged issue #$context_ref" "$safe_patch"
grep -q "^-deleted pull request #$deleted_content_ref" "$safe_patch"
assert_accepted safe-content "$safe_root" "$safe_base" "$safe_patch"

# A global template can install checkout hooks into every cloned repository.
# Replay validation must suppress them before checking out the explicit base.
template_hook_marker="$tmp/template-post-checkout-ran"
hook_template="$tmp/hook-template"
template_hook_config="$tmp/template-hook-global-config"
mkdir -p "$hook_template/hooks"
printf '#!/bin/sh\n: > "%s"\n' "$template_hook_marker" \
  > "$hook_template/hooks/post-checkout"
chmod +x "$hook_template/hooks/post-checkout"
git config --file "$template_hook_config" init.templateDir "$hook_template"
assert_hooks_suppressed template "$template_hook_config" "$template_hook_marker"

global_hook_marker="$tmp/global-post-checkout-ran"
global_hook_root="$tmp/global-hooks"
global_hook_config="$tmp/global-hook-config"
mkdir "$global_hook_root"
printf '#!/bin/sh\n: > "%s"\n' "$global_hook_marker" \
  > "$global_hook_root/post-checkout"
chmod +x "$global_hook_root/post-checkout"
git config --file "$global_hook_config" core.hooksPath "$global_hook_root"
assert_hooks_suppressed global "$global_hook_config" "$global_hook_marker"

# RFC 2047 Q encoding hides the subject marker from raw patch-line matching.
q_root="$tmp/subject-q"
q_ref=6101
new_repo "$q_root"
q_base="$(git -C "$q_root" rev-parse HEAD)"
commit_safe_change "$q_root" "café subject (#$q_ref)"
format_series "$q_root" "$q_base" "$tmp/subject-q-patches"
q_patch="$(only_patch "$tmp/subject-q-patches")"
sed "s/(#$q_ref)/(=23$q_ref)/" "$q_patch" > "$q_patch.new"
mv "$q_patch.new" "$q_patch"
grep -q "=?UTF-8?q?.*=23$q_ref" "$q_patch"
assert_rejected subject-q 'blocked pull request or issue reference' \
  "$q_root" "$q_base" "$q_patch"

# RFC 2047 B encoding exercises Git-decoded subject authority independently of
# the spelling emitted by format-patch.
b_root="$tmp/subject-b"
b_ref=6102
new_repo "$b_root"
b_base="$(git -C "$b_root" rev-parse HEAD)"
commit_safe_change "$b_root" 'safe encoded subject'
format_series "$b_root" "$b_base" "$tmp/subject-b-patches"
b_patch="$(only_patch "$tmp/subject-b-patches")"
replace_subject_with_base64 "$b_patch" "[PATCH 1/1] capability (#$b_ref)"
grep -q '^Subject: =?UTF-8?B?' "$b_patch"
assert_rejected subject-b 'blocked pull request or issue reference' \
  "$b_root" "$b_base" "$b_patch"

# Content-Transfer-Encoding applies to the complete mail body, including the
# patch. Git decodes it before committing, so metadata checks must do the same.
qp_root="$tmp/body-quoted-printable"
qp_ref=6201
new_repo "$qp_root"
qp_base="$(git -C "$qp_root" rev-parse HEAD)"
commit_safe_change "$qp_root" 'safe subject' "issue #$qp_ref"
format_series "$qp_root" "$qp_base" "$tmp/body-quoted-printable-patches"
qp_patch="$(only_patch "$tmp/body-quoted-printable-patches")"
encode_body_quoted_printable "$qp_patch" "$qp_ref"
grep -q 'Content-Transfer-Encoding: quoted-printable' "$qp_patch"
grep -q "issue =23$qp_ref" "$qp_patch"
assert_rejected body-quoted-printable 'blocked pull request or issue reference' \
  "$qp_root" "$qp_base" "$qp_patch"

b64_root="$tmp/body-base64"
b64_ref=6202
new_repo "$b64_root"
b64_base="$(git -C "$b64_root" rev-parse HEAD)"
commit_safe_change "$b64_root" 'safe subject' "pull request #$b64_ref"
format_series "$b64_root" "$b64_base" "$tmp/body-base64-patches"
b64_patch="$(only_patch "$tmp/body-base64-patches")"
encode_body_base64 "$b64_patch"
grep -q 'Content-Transfer-Encoding: base64' "$b64_patch"
assert_rejected body-base64 'blocked pull request or issue reference' \
  "$b64_root" "$b64_base" "$b64_patch"

# Envelope-only text is discarded by git am. Canonical regeneration must still
# reject patches whose diffstat, signature tail, or body/diff separator lies.
envelope_root="$tmp/envelope"
diffstat_ref=6301
trailing_ref=6302
new_repo "$envelope_root"
envelope_base="$(git -C "$envelope_root" rev-parse HEAD)"
commit_safe_change "$envelope_root" 'safe envelope'
format_series "$envelope_root" "$envelope_base" "$tmp/envelope-patches"
envelope_patch="$(only_patch "$tmp/envelope-patches")"

mkdir "$tmp/diffstat-only"
diffstat_patch="$tmp/diffstat-only/$(basename "$envelope_patch")"
awk -v reference="$diffstat_ref" '
  !inserted && /^---$/ {
    print
    print " issue #" reference " | 1 +"
    inserted = 1
    next
  }
  { print }
' "$envelope_patch" > "$diffstat_patch"
assert_rejected diffstat-only 'patch is not canonical' \
  "$envelope_root" "$envelope_base" "$diffstat_patch"

mkdir "$tmp/trailing-envelope"
trailing_patch="$tmp/trailing-envelope/$(basename "$envelope_patch")"
cp "$envelope_patch" "$trailing_patch"
printf '\n-- \nissue #%s\n' "$trailing_ref" >> "$trailing_patch"
assert_rejected trailing-envelope 'patch is not canonical' \
  "$envelope_root" "$envelope_base" "$trailing_patch"

forged_root="$tmp/forged-body"
forged_ref=6303
new_repo "$forged_root"
forged_base="$(git -C "$forged_root" rev-parse HEAD)"
printf 'safe addition\n' >> "$forged_root/file.txt"
git -C "$forged_root" add file.txt
printf '%s\n' \
  'forged body' \
  '' \
  ' diff --git a/forged.txt b/forged.txt' \
  ' index 1111111..2222222 100644' \
  ' --- a/forged.txt' \
  ' +++ b/forged.txt' \
  ' @@ -1 +1 @@' \
  ' -safe' \
  " +issue #$forged_ref" \
  '---' > "$tmp/forged-message"
git -C "$forged_root" commit -qF "$tmp/forged-message"
format_series "$forged_root" "$forged_base" "$tmp/forged-canonical"
format_series_without_stat "$forged_root" "$forged_base" "$tmp/forged-no-stat"
forged_canonical="$(only_patch "$tmp/forged-canonical")"
forged_patch="$(only_patch "$tmp/forged-no-stat")"
test "$(basename "$forged_patch")" = "$(basename "$forged_canonical")"

git clone -q --no-checkout "$forged_root" "$tmp/forged-replay"
git -C "$tmp/forged-replay" config user.name test
git -C "$tmp/forged-replay" config user.email test@example.com
git -C "$tmp/forged-replay" checkout -q --detach "$forged_base"
git -C "$tmp/forged-replay" am -q --no-3way "$forged_patch"
git -C "$tmp/forged-replay" show -s --format=%B | \
  grep -Fq " +issue #$forged_ref"
assert_rejected forged-body-diff 'blocked pull request or issue reference' \
  "$forged_root" "$forged_base" "$forged_patch"

# Git can apply a patch whose postimage index is false. Regeneration from the
# resulting commit detects the disagreement.
mkdir "$tmp/false-index"
false_index_patch="$tmp/false-index/$(basename "$envelope_patch")"
sed -E '0,/^index /s/(\.\.)[0-9a-f]+/\1deadbee/' \
  "$envelope_patch" > "$false_index_patch"
grep -q '^index .*[.]deadbee ' "$false_index_patch"
assert_rejected false-index 'patch is not canonical' \
  "$envelope_root" "$envelope_base" "$false_index_patch"

assert_blocked_destination() {
  local kind="$1"
  local reference="$2"
  local root="$tmp/destination-$kind"
  local output="$tmp/destination-$kind-patches"
  local base expected patch status
  new_repo "$root"
  base="$(git -C "$root" rev-parse HEAD)"

  case "$kind" in
    new)
      expected=A
      printf 'new destination\n' > "$root/issue #$reference.txt"
      git -C "$root" add .
      ;;
    rename)
      expected=R
      git -C "$root" mv file.txt "PR #$reference.txt"
      ;;
    copy)
      expected=C
      cp "$root/file.txt" "${root}/pull request #$reference.txt"
      printf 'modified source\n' >> "$root/file.txt"
      git -C "$root" add .
      ;;
  esac
  git -C "$root" commit -qm "unsafe $kind destination"
  status="$(git -C "$root" diff-tree --no-commit-id --name-status -r -M -C \
    "$base" HEAD | awk -v suffix="#$reference.txt" '$NF ~ suffix "$" { print substr($1, 1, 1) }')"
  test "$status" = "$expected" || {
    echo "fixture did not produce Git $kind status: got '$status'" >&2
    exit 1
  }
  format_series "$root" "$base" "$output"
  patch="$(only_patch "$output")"
  assert_rejected "destination-$kind" 'blocked pull request or issue reference' \
    "$root" "$base" "$patch"
}

assert_blocked_destination new 6401
assert_blocked_destination rename 6402
assert_blocked_destination copy 6403

# Forbidden references in deleted paths and rename sources are upstream state.
deleted_root="$tmp/deleted-path"
deleted_path_ref=6501
deleted_path="issue #$deleted_path_ref.txt"
new_repo "$deleted_root"
git -C "$deleted_root" mv file.txt "$deleted_path"
git -C "$deleted_root" commit -qam base-path
deleted_base="$(git -C "$deleted_root" rev-parse HEAD)"
git -C "$deleted_root" rm -q "$deleted_path"
git -C "$deleted_root" commit -qm 'delete upstream path'
format_series "$deleted_root" "$deleted_base" "$tmp/deleted-path-patches"
deleted_patch="$(only_patch "$tmp/deleted-path-patches")"
assert_accepted deleted-path "$deleted_root" "$deleted_base" "$deleted_patch"

rename_away_root="$tmp/rename-away"
rename_away_ref=6502
rename_away_source="pull request #$rename_away_ref.txt"
new_repo "$rename_away_root"
git -C "$rename_away_root" mv file.txt "$rename_away_source"
git -C "$rename_away_root" commit -qam base-path
rename_away_base="$(git -C "$rename_away_root" rev-parse HEAD)"
git -C "$rename_away_root" mv "$rename_away_source" safe.txt
git -C "$rename_away_root" commit -qm 'rename away from upstream reference'
format_series "$rename_away_root" "$rename_away_base" "$tmp/rename-away-patches"
rename_away_patch="$(only_patch "$tmp/rename-away-patches")"
assert_accepted rename-away "$rename_away_root" "$rename_away_base" \
  "$rename_away_patch"

# One path argument must represent exactly one canonical mail message.
mkdir "$tmp/concatenated"
concatenated_patch="$tmp/concatenated/$(basename "$envelope_patch")"
concatenated_ref=6601
cp "$envelope_patch" "$concatenated_patch"
printf '\n' >> "$concatenated_patch"
sed "s/safe envelope/second message issue #$concatenated_ref/" "$envelope_patch" \
  >> "$concatenated_patch"
assert_rejected concatenated-message \
  'git .* am .* failed|blocked pull request or issue reference|patch is not canonical' \
  "$envelope_root" "$envelope_base" "$concatenated_patch"

# Binary changes have no trustworthy added-line representation in textual Git
# diff output, so the sanitizer must reject them rather than silently skip them.
binary_root="$tmp/binary"
new_repo "$binary_root"
printf '\000base\377\n' > "$binary_root/data.bin"
git -C "$binary_root" add data.bin
git -C "$binary_root" commit --amend -qm base
binary_base="$(git -C "$binary_root" rev-parse HEAD)"
printf '\000changed\376\n' > "$binary_root/data.bin"
git -C "$binary_root" commit -qam 'binary change'
format_series "$binary_root" "$binary_base" "$tmp/binary-patches"
binary_patch="$(only_patch "$tmp/binary-patches")"
grep -Eq '^(Binary files .* differ|GIT binary patch)$' "$binary_patch"
assert_rejected binary-change 'binary patch content cannot be reference-scanned' \
  "$binary_root" "$binary_base" "$binary_patch"

echo 'patch sanitizer regressions passed'
