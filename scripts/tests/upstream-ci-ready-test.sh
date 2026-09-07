#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

fake_bin="$tmp/bin"
fixtures="$tmp/fixtures"
mkdir -p "$fake_bin"

cat >"$fake_bin/gh" <<'SH'
#!/usr/bin/env bash
set -euo pipefail

test "${1:-}" = api
path="${2:?missing API path}"
fixture="$FAKE_GH_FIXTURES/$path.json"
test -f "$fixture"
cat "$fixture"
SH
chmod +x "$fake_bin/gh"

reset_fixtures() {
  rm -rf "$fixtures"
  mkdir -p "$fixtures/repos/example/upstream/commits"
}

write_commit() {
  local sha="$1"
  local message="$2"
  local files_json="$3"
  local parents_json="$4"
  local destination="$fixtures/repos/example/upstream/commits/$sha.json"
  printf '{"sha":"%s","commit":{"message":"%s"},"files":%s,"parents":%s}\n' \
    "$sha" "$message" "$files_json" "$parents_json" >"$destination"
}

write_empty_statuses() {
  local sha="$1"
  mkdir -p "$fixtures/repos/example/upstream/commits/$sha"
  printf '{"statuses":[]}\n' \
    >"$fixtures/repos/example/upstream/commits/$sha/status.json"
}

write_success_statuses() {
  local sha="$1"
  mkdir -p "$fixtures/repos/example/upstream/commits/$sha"
  printf '%s\n' \
    '{"statuses":[{"context":"test","state":"success"},{"context":"frontend","state":"success"},{"context":"golangci-lint","state":"success"}]}' \
    >"$fixtures/repos/example/upstream/commits/$sha/status.json"
}

write_checks() {
  local sha="$1"
  local test_result="$2"
  local frontend_result="$3"
  local lint_result="$4"
  local runs=''
  local name result status conclusion

  mkdir -p "$fixtures/repos/example/upstream/commits/$sha"
  for entry in "test:$test_result" "frontend:$frontend_result" "golangci-lint:$lint_result"; do
    name="${entry%%:*}"
    result="${entry#*:}"
    case "$result" in
      missing)
        continue
        ;;
      queued | in_progress)
        status="$result"
        conclusion=null
        ;;
      *)
        status=completed
        conclusion="\"$result\""
        ;;
    esac
    runs="${runs}${runs:+,}{\"name\":\"$name\",\"status\":\"$status\",\"conclusion\":$conclusion}"
  done
  printf '[{"check_runs":[%s]}]\n' "$runs" \
    >"$fixtures/repos/example/upstream/commits/$sha/check-runs.json"
  write_empty_statuses "$sha"
}

assert_selected() {
  local expected="$1"
  local ref="$2"
  local actual
  actual="$(PATH="$fake_bin:$PATH" FAKE_GH_FIXTURES="$fixtures" \
    "$repo_root/scripts/upstream-ci-ready.sh" example/upstream "$ref" \
    2>"$tmp/stderr")"
  if test "$actual" != "$expected"; then
    printf 'expected %s, got %s\n' "$expected" "$actual" >&2
    cat "$tmp/stderr" >&2
    exit 1
  fi
}

assert_rejected() {
  local ref="$1"
  if PATH="$fake_bin:$PATH" FAKE_GH_FIXTURES="$fixtures" \
    "$repo_root/scripts/upstream-ci-ready.sh" example/upstream "$ref" \
    >"$tmp/stdout" 2>"$tmp/stderr"; then
    printf 'expected %s to be rejected, selected %s\n' "$ref" "$(cat "$tmp/stdout")" >&2
    exit 1
  fi
}

version_file='[{"filename":"backend/cmd/server/VERSION"}]'
other_file='[{"filename":"backend/main.go"}]'
non_success_results=(
  neutral
  skipped
  cancelled
  timed_out
  action_required
  failure
  stale
  queued
  in_progress
  missing
)

# A normal candidate needs all three required checks to complete successfully.
reset_fixtures
write_commit normal-success normal "$other_file" '[]'
write_checks normal-success success success success
assert_selected normal-success normal-success

for result in "${non_success_results[@]}"; do
  reset_fixtures
  write_commit "normal-$result" normal "$other_file" '[{"sha":"normal-parent"}]'
  write_checks "normal-$result" "$result" success success
  write_commit normal-parent normal "$other_file" '[]'
  write_checks normal-parent success success success
  assert_selected normal-parent "normal-$result"
done

# Legacy commit statuses cannot qualify a commit or replace a required check run.
reset_fixtures
write_commit statuses-only normal "$other_file" '[{"sha":"checks-parent"}]'
write_checks statuses-only missing missing missing
write_success_statuses statuses-only
write_commit checks-parent normal "$other_file" '[]'
write_checks checks-parent success success success
assert_selected checks-parent statuses-only

reset_fixtures
write_commit status-substitution normal "$other_file" '[{"sha":"checks-parent"}]'
write_checks status-substitution missing success success
write_success_statuses status-substitution
write_commit checks-parent normal "$other_file" '[]'
write_checks checks-parent success success success
assert_selected checks-parent status-substitution

# A VERSION-only skip commit without its own checks is ineligible.
reset_fixtures
write_commit version-skip 'release [skip ci]' "$version_file" '[{"sha":"version-parent"}]'
write_checks version-skip missing missing missing
write_commit version-parent normal "$other_file" '[]'
write_checks version-parent success success success
assert_selected version-parent version-skip

# A VERSION-only skip commit remains eligible when its own checks pass.
reset_fixtures
write_commit checked-version-skip 'release [skip ci]' "$version_file" '[]'
write_checks checked-version-skip success success success
assert_selected checked-version-skip checked-version-skip

# A Checks API failure is not evidence that a commit intentionally has no runs.
reset_fixtures
write_commit candidate-checks-error 'release [skip ci]' "$version_file" \
  '[{"sha":"candidate-error-parent"}]'
write_commit candidate-error-parent normal "$other_file" '[]'
write_checks candidate-error-parent success success success
assert_rejected candidate-checks-error

reset_fixtures
write_commit unready-candidate normal "$other_file" '[{"sha":"unreadable-parent-checks"}]'
write_checks unready-candidate missing missing missing
write_commit unreadable-parent-checks normal "$other_file" \
  '[{"sha":"checks-error-grandparent"}]'
write_commit checks-error-grandparent normal "$other_file" '[]'
write_checks checks-error-grandparent success success success
assert_rejected unready-candidate

# Traversal follows only the first parent of a merge commit.
reset_fixtures
write_commit version-merge 'release [skip ci]' "$version_file" \
  '[{"sha":"first-parent"},{"sha":"second-parent"}]'
write_checks version-merge missing missing missing
write_commit first-parent normal "$other_file" '[]'
write_checks first-parent success success success
write_commit second-parent normal "$other_file" '[]'
write_checks second-parent success success success
assert_selected first-parent version-merge

reset_fixtures
write_commit no-parent 'release [skip ci]' "$version_file" '[]'
write_checks no-parent missing missing missing
assert_rejected no-parent

# Consecutive VERSION-only skip commits are each ineligible without their own checks.
reset_fixtures
write_commit newest-skip 'release [skip ci]' "$version_file" '[{"sha":"older-skip"}]'
write_checks newest-skip missing missing missing
write_commit older-skip 'release [skip ci]' "$version_file" '[{"sha":"checked-parent"}]'
write_checks older-skip missing missing missing
write_commit checked-parent normal "$other_file" '[]'
write_checks checked-parent success success success
assert_selected checked-parent newest-skip

echo 'upstream CI readiness policy regressions passed'
