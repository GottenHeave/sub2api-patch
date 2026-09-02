#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
base_sha="5097b31457e6dc9f49e5f5c9c72b925ce79543b3"
expected_tree="db5773c6a547bba992cba30430b1ae5fbf208234"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

shopt -s nullglob
patches=("$repo_root"/patches/cur/*.patch)
if [ "${#patches[@]}" -ne 11 ]; then
  echo "expected 11 canonical patches, found ${#patches[@]}" >&2
  exit 1
fi

for patch in "${patches[@]}"; do
  grep -q '^---$' "$patch"
done
python3 "$repo_root/scripts/sanitize-patches.py" \
  --check \
  --repo "$repo_root" \
  --base-ref "$base_sha" \
  "${patches[@]}"

replay_selection() {
  local name="$1"
  shift
  local replay="$tmp/$name"
  git init -q "$replay"
  git -C "$replay" config user.name test
  git -C "$replay" config user.email test@example.com
  git -C "$replay" fetch --quiet --no-tags --depth=1 "$repo_root" "$base_sha"
  git -C "$replay" checkout --quiet --detach FETCH_HEAD
  git -C "$replay" am --quiet "$@"
}

replay_selection docker "${patches[0]}"
replay_selection cache "${patches[1]}"
replay_selection cached_tokens "${patches[2]}"
replay_selection transcription \
  "${patches[2]}" "${patches[3]}" "${patches[4]}" "${patches[5]}"
replay_selection realtime_ws "${patches[6]}"
replay_selection realtime_transport \
  "${patches[3]}" "${patches[6]}" "${patches[7]}"
replay_selection realtime_moderation "${patches[6]}" "${patches[8]}"
replay_selection integrated_realtime \
  "${patches[3]}" "${patches[6]}" "${patches[7]}" \
  "${patches[8]}" "${patches[9]}"
replay_selection codex "${patches[10]}"

replay_selection replay "${patches[@]}"
actual_tree="$(git -C "$tmp/replay" rev-parse 'HEAD^{tree}')"
if [ "$actual_tree" != "$expected_tree" ]; then
  echo "canonical series tree mismatch: expected $expected_tree, got $actual_tree" >&2
  exit 1
fi

echo "canonical series replayed tree $actual_tree"
