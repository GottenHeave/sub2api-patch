#!/usr/bin/env bash
set -euo pipefail

if [ "${PWD}" = "@TARGET_REPO@" ] && [ "${1:-}" = "format-patch" ]; then
  "@REAL_GIT@" "$@"
  patch_dir=""
  previous=""
  for argument in "$@"; do
    if [ "$previous" = "-o" ]; then
      patch_dir="$argument"
      break
    fi
    previous="$argument"
  done
  patch_file="$(find "$patch_dir" -type f -name '*.patch' -print -quit)"
  printf '\ndiff --git a/missing.txt b/missing.txt\n--- a/missing.txt\n+++ b/missing.txt\n@@ -1 +1 @@\n-missing\n+corrupted\n' >> "$patch_file"
  exit 0
fi

exec "@REAL_GIT@" "$@"
