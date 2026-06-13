#!/usr/bin/env bash
set -euo pipefail

repo="${1:-Wei-Shaw/sub2api}"
sha="${2:?usage: scripts/upstream-ci-ready.sh owner/repo sha}"

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

status_file="$tmpdir/status.json"
checks_file="$tmpdir/checks.json"

gh api "repos/$repo/commits/$sha/status" >"$status_file" 2>/dev/null || printf '{}\n' >"$status_file"
gh api "repos/$repo/commits/$sha/check-runs" --paginate >"$checks_file" 2>/dev/null || printf '{"check_runs":[]}\n' >"$checks_file"

python3 - "$status_file" "$checks_file" <<'PY'
import json
import sys
from pathlib import Path

status = json.loads(Path(sys.argv[1]).read_text() or '{}')
checks = json.loads(Path(sys.argv[2]).read_text() or '{"check_runs":[]}')

combined = status.get('state')
statuses = status.get('statuses') or []
runs = checks.get('check_runs') or []

bad_statuses = [s for s in statuses if s.get('state') not in ('success',)]
bad_runs = [
    r for r in runs
    if r.get('status') != 'completed' or r.get('conclusion') not in ('success', 'skipped', 'neutral')
]

if not statuses and not runs:
    print('upstream CI state unavailable; sync paused')
    sys.exit(1)

if combined not in (None, 'success'):
    print(f'combined status is {combined}; sync paused')
    sys.exit(1)

if bad_statuses or bad_runs:
    print('upstream CI is not fully passing; sync paused')
    for item in bad_statuses:
        print(f"status: {item.get('context')}={item.get('state')}")
    for item in bad_runs:
        print(f"check: {item.get('name')} status={item.get('status')} conclusion={item.get('conclusion')}")
    sys.exit(1)

print('upstream CI ready')
PY
