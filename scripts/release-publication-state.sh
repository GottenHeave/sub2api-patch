#!/usr/bin/env bash
set -euo pipefail

image="$1"
version="$2"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

status="$(curl --silent --show-error --location --output "$tmp/release.json" --write-out '%{http_code}' \
  --header "Authorization: Bearer $GH_TOKEN" \
  --header 'Accept: application/vnd.github+json' \
  "https://api.github.com/repos/${GITHUB_REPOSITORY}/releases/tags/${version}")"
case "$status" in
  404) release_exists=false ;;
  200) release_exists=true ;;
  *) echo "GitHub release lookup failed with HTTP $status" >&2; exit 1 ;;
esac

if docker buildx imagetools inspect "$image:$version" >"$tmp/image.json" 2>"$tmp/image.err"; then
  image_exists=true
else
  grep -Eqi 'not found|manifest unknown|no such manifest' "$tmp/image.err" || {
    cat "$tmp/image.err" >&2
    exit 1
  }
  image_exists=false
fi

printf '%s %s\n' "$image_exists" "$release_exists"
