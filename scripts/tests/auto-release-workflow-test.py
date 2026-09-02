#!/usr/bin/env python3
from pathlib import Path


workflow = Path(__file__).parents[2] / ".github/workflows/auto-release.yml"
text = workflow.read_text(encoding="utf-8")

ci_step = text.index("- name: Require manual upstream CI success")
record_step = text.index("- name: Record upstream base")
ci_block = text[ci_step:record_step]
apply_step = text.index("- name: Apply patches")
record_block = text[record_step:apply_step]

required = (
    'ready_sha="$(sub2api-patch/scripts/upstream-ci-ready.sh '
    '"$UPSTREAM_REPOSITORY" "$upstream_sha")"',
    'git -C worktree checkout --detach "$ready_sha"',
    'echo "upstream_sha=${ready_sha}" >> "$GITHUB_OUTPUT"',
)
for command in required:
    if command not in ci_block:
        raise SystemExit(f"manual release does not use CI-ready SHA: {command}")

checkout = ci_block.index('git -C worktree checkout --detach "$ready_sha"')
output = ci_block.index('echo "upstream_sha=${ready_sha}" >> "$GITHUB_OUTPUT"')
if checkout > output:
    raise SystemExit("manual release publishes CI-ready SHA before checking it out")

recorded_sha = 'candidate_head="${{ steps.require_manual_ci.outputs.upstream_sha }}"'
checkout_guard = 'if [ "$candidate_head" != "$checked_out_head" ]; then'
for command in (recorded_sha, checkout_guard):
    if command not in record_block:
        raise SystemExit(f"release metadata does not use CI-ready SHA: {command}")

print("auto-release workflow regression passed")
