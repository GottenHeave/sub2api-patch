#!/usr/bin/env bash
set -euo pipefail

repo="${1:-Wei-Shaw/sub2api}"
sha="${2:?usage: scripts/upstream-ci-ready.sh owner/repo sha}"

status_json="$(gh api "repos/$repo/commits/$sha/status" 2>/dev/null || echo '{}')"
check_json="$(gh api "repos/$repo/commits/$sha/check-runs" --paginate 2>/dev/null || echo '{"check_runs":[]}')"

python3 - "$status_json" "$check_json" <<'PY'
import json
import sys

status = json.loads(sys.argv[1] or '{}')
checks = json.loads(sys.argv[2] or '{"check_runs":[]}')

combined = status.get('state')
statuses = status.get('statuses') or []
runs = checks.get('check_runs') or []

bad_statuses = [s for s in statuses if s.get('state') not in ('success',)]
bad_runs = [r for r in runs if r.get('status') != 'completed' or r.get('conclusion') not in ('success', 'skipped', 'neutral')]

# Safe default: if upstream exposes no status and no check run, do not sync.
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
