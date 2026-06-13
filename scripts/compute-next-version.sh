#!/usr/bin/env bash
set -euo pipefail

worktree="${1:-.}"
cd "$worktree"
upstream_version="$(tr -d '[:space:]' < backend/cmd/server/VERSION)"
base="v${upstream_version}"

existing="$(gh release list --limit 200 2>/dev/null | awk '{print $1}' | grep -E "^${base}-patch\\.[0-9]+$" || true)"
last="$(printf '%s\n' "$existing" | sed -E "s/^${base}-patch\.([0-9]+)$/\1/" | sort -n | tail -1)"
if [ -z "$last" ]; then
  next=1
else
  next=$((last + 1))
fi
printf '%s-patch.%s\n' "$base" "$next"
