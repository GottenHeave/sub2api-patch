#!/usr/bin/env bash
set -euo pipefail

repo="${1:-Wei-Shaw/sub2api}"
ref="${2:?usage: scripts/upstream-ci-ready.sh owner/repo sha-or-ref}"

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

commit_file="$tmpdir/commit.json"

gh api "repos/$repo/commits/$ref" >"$commit_file"

python3 - "$repo" "$commit_file" <<'PY'
import json
import subprocess
import sys
from pathlib import Path

repo, commit_path = sys.argv[1:3]
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


def evaluate(runs):
    relevant_runs = [r for r in runs if r.get('name') in required_names]
    present = {r.get('name') for r in relevant_runs}
    missing = sorted(required_names - present)
    bad_runs = [
        r for r in relevant_runs
        if r.get('status') != 'completed' or r.get('conclusion') != 'success'
    ]
    return relevant_runs, missing, bad_runs


def fetch_json(path, default):
    try:
        return json.loads(subprocess.check_output(['gh', 'api', path], text=True))
    except subprocess.CalledProcessError:
        return default


def fetch_checks(target_sha):
    try:
        raw = subprocess.check_output([
            'gh', 'api', f'repos/{repo}/commits/{target_sha}/check-runs', '--paginate', '--slurp'
        ], text=True)
    except subprocess.CalledProcessError:
        print(f'failed to fetch check runs for {target_sha}', file=sys.stderr)
        sys.exit(1)
    return flatten_runs(json.loads(raw or '[{"check_runs":[]}]'))


def ci_ready(runs):
    relevant_runs, missing, bad_runs = evaluate(runs)
    return not missing and not bad_runs and relevant_runs


def is_version_only_skip_ci(commit):
    message = commit.get('commit', {}).get('message', '')
    subject = message.splitlines()[0] if message else ''
    files = commit.get('files') or []
    parents = commit.get('parents') or []
    return (
        '[skip ci]' in subject.lower()
        and len(files) == 1
        and files[0].get('filename') == 'backend/cmd/server/VERSION'
        and 'previous_filename' not in files[0]
        and len(parents) == 1
    )


def parent_sha(commit):
    parents = commit.get('parents') or []
    return parents[0].get('sha') if parents else None


commit = load(commit_path, {})
while commit:
    sha = commit['sha']
    runs = fetch_checks(sha)

    if ci_ready(runs):
        print(f'using upstream commit {sha[:12]} with passing required CI', file=sys.stderr)
        print(sha)
        break

    parent = parent_sha(commit)
    if not parent:
        print('no upstream commit with passing required CI was found', file=sys.stderr)
        sys.exit(1)

    relevant_runs, _, _ = evaluate(runs)
    if not relevant_runs and is_version_only_skip_ci(commit):
        parent_commit = fetch_json(f'repos/{repo}/commits/{parent}', {})
        parent_runs = fetch_checks(parent)
        if parent_commit.get('sha') == parent and ci_ready(parent_runs):
            print(
                f'using version-only upstream commit {sha[:12]} with parent {parent[:12]} CI',
                file=sys.stderr,
            )
            print(sha)
            break

    print(f'upstream commit {sha[:12]} is not ready; checking parent {parent[:12]}', file=sys.stderr)
    commit = fetch_json(f'repos/{repo}/commits/{parent}', {})
else:
    print('no upstream commit with passing required CI was found', file=sys.stderr)
    sys.exit(1)
PY
