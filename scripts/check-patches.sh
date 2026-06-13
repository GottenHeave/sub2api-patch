#!/usr/bin/env bash
set -euo pipefail

worktree="${1:-.}"
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

"$repo_root/scripts/apply-patches.sh" "$worktree"
"$repo_root/scripts/check-no-pr-issue-refs.sh" "$repo_root"

cd "$worktree"

if command -v go >/dev/null 2>&1; then
  make -C backend test
else
  echo "go is not available; skipping backend tests" >&2
fi

if command -v pnpm >/dev/null 2>&1; then
  pnpm --dir frontend install --frozen-lockfile
  pnpm --dir frontend run typecheck
  pnpm --dir frontend run lint:check
else
  echo "pnpm is not available; skipping frontend checks" >&2
fi
