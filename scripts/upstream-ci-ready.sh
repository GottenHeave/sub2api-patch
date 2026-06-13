#!/usr/bin/env bash
set -euo pipefail

repo="${1:-Wei-Shaw/sub2api}"
ref="${2:?usage: scripts/upstream-ci-ready.sh owner/repo sha-or-ref}"

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

commit_file="$tmpdir/commit.json"
status_file="$tmpdir/status.json"
checks_file="$tmpdir/checks.json"

gh api "repos/$repo/commits/$ref" >"$commit_file"
sha="$(python3 - "$commit_file" <<'PY'
import json, sys
print(json.load(open(sys.argv[1]))['sha'])
PY
)"

gh api "repos/$repo/commits/$sha/status" >"$status_file" 2>/dev/null || printf '{}\n' >"$status_file"
gh api "repos/$repo/commits/$sha/check-runs" --paginate --slurp >"$checks_file" 2>/dev/null || printf '[{"check_runs":[]}]\n' >"$checks_file"

python3 - "$repo" "$sha" "$commit_file" "$status_file" "$checks_file" <<'PY'
import json
import subprocess
import sys
from pathlib import Path

repo, sha, commit_path, status_path, checks_path = sys.argv[1:6]
required_names = {'test', 'frontend', 'golangci-lint'}


def load(path, default):
    text = Path(path).read_text()
    return json.loads(text) if text else default


def flatten_runs(payload):
    if isinstance(payload, list):
        runs = []
        for page in payload:
            if isinstance(page, dict):
                runs.extend(page.get('check_runs') or [])
        return runs
    return payload.get('check_runs') or []


def evaluate(commit, status, runs):
    statuses = status.get('statuses') or []
    relevant_statuses = [s for s in statuses if s.get('context') in required_names]
    relevant_runs = [r for r in runs if r.get('name') in required_names]
    present = {s.get('context') for s in relevant_statuses} | {r.get('name') for r in relevant_runs}
    missing = sorted(required_names - present)
    bad_statuses = [s for s in relevant_statuses if s.get('state') not in ('success',)]
    bad_runs = [
        r for r in relevant_runs
        if r.get('status') != 'completed' or r.get('conclusion') not in ('success', 'skipped', 'neutral')
    ]
    return relevant_statuses, relevant_runs, missing, bad_statuses, bad_runs


def fetch_json(path):
    return json.loads(subprocess.check_output(['gh', 'api', path], text=True))


def fetch_checks(target_sha):
    raw = subprocess.check_output([
        'gh', 'api', f'repos/{repo}/commits/{target_sha}/check-runs', '--paginate', '--slurp'
    ], text=True)
    return flatten_runs(json.loads(raw or '[{"check_runs":[]}]'))


def is_version_only_skip_ci(commit):
    message = commit.get('commit', {}).get('message', '')
    files = commit.get('files') or []
    changed = {f.get('filename') for f in files}
    return '[skip ci]' in message.lower() and changed <= {'backend/cmd/server/VERSION'}

commit = load(commit_path, {})
status = load(status_path, {})
runs = flatten_runs(load(checks_path, [{'check_runs': []}]))
relevant_statuses, relevant_runs, missing, bad_statuses, bad_runs = evaluate(commit, status, runs)

if (not relevant_statuses and not relevant_runs) and is_version_only_skip_ci(commit):
    parents = commit.get('parents') or []
    if not parents:
        print('version-only skip-ci commit has no parent; sync paused')
        sys.exit(1)
    parent_sha = parents[0]['sha']
    parent_commit = fetch_json(f'repos/{repo}/commits/{parent_sha}')
    parent_status = fetch_json(f'repos/{repo}/commits/{parent_sha}/status')
    parent_runs = fetch_checks(parent_sha)
    relevant_statuses, relevant_runs, missing, bad_statuses, bad_runs = evaluate(parent_commit, parent_status, parent_runs)
    if relevant_statuses or relevant_runs:
        print(f'using frontend/backend CI from tested parent {parent_sha[:12]} for version-only skip-ci commit {sha[:12]}')

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
