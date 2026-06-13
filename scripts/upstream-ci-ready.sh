#!/usr/bin/env bash
set -euo pipefail

repo="${1:-Wei-Shaw/sub2api}"
sha="${2:?usage: scripts/upstream-ci-ready.sh owner/repo sha}"

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

status_file="$tmpdir/status.json"
checks_file="$tmpdir/checks.json"

gh api "repos/$repo/commits/$sha/status" >"$status_file" 2>/dev/null || printf '{}\n' >"$status_file"
gh api "repos/$repo/commits/$sha/check-runs" --paginate --slurp >"$checks_file" 2>/dev/null || printf '[{"check_runs":[]}]\n' >"$checks_file"

python3 - "$status_file" "$checks_file" <<'PY'
import json
import sys
from pathlib import Path

status = json.loads(Path(sys.argv[1]).read_text() or '{}')
checks_payload = json.loads(Path(sys.argv[2]).read_text() or '[{"check_runs":[]}]')

combined = status.get('state')
statuses = status.get('statuses') or []
if isinstance(checks_payload, list):
    runs = []
    for page in checks_payload:
        if isinstance(page, dict):
            runs.extend(page.get('check_runs') or [])
else:
    runs = checks_payload.get('check_runs') or []

required_names = {'test', 'frontend', 'golangci-lint'}
relevant_statuses = [s for s in statuses if s.get('context') in required_names]
relevant_runs = [r for r in runs if r.get('name') in required_names]

missing = sorted(required_names - {s.get('context') for s in relevant_statuses} - {r.get('name') for r in relevant_runs})
bad_statuses = [s for s in relevant_statuses if s.get('state') not in ('success',)]
bad_runs = [
    r for r in relevant_runs
    if r.get('status') != 'completed' or r.get('conclusion') not in ('success', 'skipped', 'neutral')
]

if not relevant_statuses and not relevant_runs:
    print('upstream frontend/backend CI state unavailable; sync paused')
    sys.exit(1)

if missing:
    print('upstream frontend/backend CI checks missing; sync paused')
    for name in missing:
        print(f'missing check: {name}')
    sys.exit(1)

if bad_statuses or bad_runs:
    print('upstream frontend/backend CI is not passing; sync paused')
    for item in bad_statuses:
        print(f"status: {item.get('context')}={item.get('state')}")
    for item in bad_runs:
        print(f"check: {item.get('name')} status={item.get('status')} conclusion={item.get('conclusion')}")
    sys.exit(1)

print('upstream frontend/backend CI ready')
PY
