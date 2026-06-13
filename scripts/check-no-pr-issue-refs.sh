#!/usr/bin/env bash
set -euo pipefail

root="${1:-$(pwd)}"
cd "$root"

scan_paths=()
for path in patches scripts .github README.md PATCHES.md RELEASE_POLICY.md docs; do
  [ -e "$path" ] && scan_paths+=("$path")
done

if [ "${#scan_paths[@]}" -eq 0 ]; then
  exit 0
fi

pattern='(^|[^A-Za-z0-9_])#[0-9]+|PR[[:space:]]*#[0-9]+|pull request[[:space:]]*#[0-9]+|issue[[:space:]]*#[0-9]+|/issues/[0-9]+|/pull/[0-9]+|github\.com/[^[:space:]]+/(issues|pull)/[0-9]+'

if grep -RInE "$pattern" "${scan_paths[@]}"; then
  echo "blocked pull request, issue, or mention reference found" >&2
  exit 1
fi
