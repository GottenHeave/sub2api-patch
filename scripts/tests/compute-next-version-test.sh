#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

fake_bin="$tmp/bin"
worktree="$tmp/worktree"
mkdir -p "$fake_bin" "$worktree/backend/cmd/server"
printf '0.2.0\n' > "$worktree/backend/cmd/server/VERSION"
git init -q "$worktree"
git -C "$worktree" config user.name test
git -C "$worktree" config user.email test@example.com
git -C "$worktree" add backend/cmd/server/VERSION
git -C "$worktree" commit -qm base

cat > "$fake_bin/gh" <<'SH'
#!/usr/bin/env bash
set -euo pipefail

has_arg() {
  local expected="$1"
  shift
  local arg
  for arg in "$@"; do
    [ "$arg" = "$expected" ] && return 0
  done
  return 1
}

[ "${1:-}" = api ] || {
  printf 'expected gh api, got: %s\n' "$*" >&2
  exit 43
}
has_arg --paginate "$@" || {
  printf 'missing --paginate: %s\n' "$*" >&2
  exit 44
}

case "${2:-}" in
  'repos/example/downstream/releases?per_page=100')
    has_arg '.[].tag_name' "$@" || {
      printf 'release query does not select tag_name: %s\n' "$*" >&2
      exit 45
    }
    if [ "${FAKE_RELEASE_FAILURE:-false}" = true ]; then
      echo 'release lookup failed' >&2
      exit 41
    fi
    printf '%s' "${FAKE_RELEASES:-}"
    ;;
  'repos/example/downstream/git/matching-refs/tags/v0.2.0-patch.?per_page=100')
    has_arg '.[].ref' "$@" || {
      printf 'tag query does not select ref: %s\n' "$*" >&2
      exit 46
    }
    if [ "${FAKE_TAG_FAILURE:-false}" = true ]; then
      echo 'tag lookup failed' >&2
      exit 42
    fi
    printf '%s' "${FAKE_TAGS:-}"
    ;;
  *)
    printf 'unexpected gh API endpoint: %s\n' "$*" >&2
    exit 47
    ;;
esac
SH
chmod +x "$fake_bin/gh"

compute() {
  PATH="$fake_bin:$PATH" RELEASE_REPOSITORY=example/downstream \
    "$repo_root/scripts/compute-next-version.sh" "$worktree"
}

assert_version() {
  local expected="$1"
  local actual
  actual="$(compute)"
  if [ "$actual" != "$expected" ]; then
    printf 'expected %s, got %s\n' "$expected" "$actual" >&2
    exit 1
  fi
}

assert_lookup_failure() {
  local expected_message="$1"
  if compute > "$tmp/stdout" 2> "$tmp/stderr"; then
    printf 'expected lookup failure, got %s\n' "$(cat "$tmp/stdout")" >&2
    exit 1
  fi
  grep -Fq "$expected_message" "$tmp/stderr"
}

# An empty tag and release namespace starts each upstream line at patch 1.
FAKE_RELEASES='' FAKE_TAGS='' assert_version 'v0.2.0-patch.1'

# Release and remote-tag counters share one namespace.
FAKE_RELEASES=$'v0.2.0-patch.2\nv0.2.0-patch.7\n' \
  FAKE_TAGS=$'refs/tags/v0.2.0-patch.4\nrefs/tags/v0.2.0-patch.9\n' \
  assert_version 'v0.2.0-patch.10'

# Namespace checks must consume inputs larger than a pipe buffer completely.
large_tags=''
for counter in $(seq 1 2000); do
  large_tags+="refs/tags/v0.2.0-patch.${counter}"$'\n'
done
FAKE_RELEASES='' FAKE_TAGS="$large_tags" \
  assert_version 'v0.2.0-patch.2001'

# Fetched local tags participate in collision avoidance too.
git -C "$worktree" tag v0.2.0-patch.12
FAKE_RELEASES='' FAKE_TAGS='' assert_version 'v0.2.0-patch.13'
git -C "$worktree" tag -d v0.2.0-patch.12 >/dev/null

# An unavailable namespace is unknown, not empty.
FAKE_RELEASE_FAILURE=true FAKE_TAGS='' \
  assert_lookup_failure 'release lookup failed'
FAKE_RELEASES='' FAKE_TAG_FAILURE=true \
  assert_lookup_failure 'tag lookup failed'

echo 'next version namespace regressions passed'
