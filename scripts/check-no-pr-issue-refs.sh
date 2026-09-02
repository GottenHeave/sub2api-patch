#!/usr/bin/env bash
set -euo pipefail

root="${1:-$(pwd)}"
cd "$root"

scan_paths=()
for path in scripts .github README.md PATCHES.md RELEASE_POLICY.md docs; do
  [ -e "$path" ] && scan_paths+=("$path")
done

patch_files=()
while IFS= read -r -d '' path; do
  patch_files+=("$path")
done < <(find patches -type f -name '*.patch' -print0 2>/dev/null | sort -z)

if [ "${#patch_files[@]}" -gt 0 ]; then
  base_repo="${PATCH_BASE_REPO:?PATCH_BASE_REPO is required when checking patches}"
  base_ref="${EXPECTED_BASE_SHA:?EXPECTED_BASE_SHA is required when checking patches}"
  python3 scripts/sanitize-patches.py \
    --check \
    --repo "$base_repo" \
    --base-ref "$base_ref" \
    "${patch_files[@]}"
fi

if [ "${#scan_paths[@]}" -eq 0 ]; then
  exit 0
fi

pattern='(^|[^A-Za-z0-9_])#[0-9]+|PR[[:space:]]*#[0-9]+|pull request[[:space:]]*#[0-9]+|issue[[:space:]]*#[0-9]+|/issues/[0-9]+|/pull/[0-9]+|github\.com/[^[:space:]]+/(issues|pull)/[0-9]+'

if grep -RInE "$pattern" "${scan_paths[@]}"; then
  echo "blocked pull request, issue, or mention reference found" >&2
  exit 1
fi
