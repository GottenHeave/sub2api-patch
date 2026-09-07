#!/usr/bin/env python3
"""Smoke-test the release workflow's functional wiring."""

from __future__ import annotations

import re
import unittest
from pathlib import Path


ROOT = Path(__file__).parents[2]
SYNC = (ROOT / ".github/workflows/sync-upstream.yml").read_text()
VALIDATION = (ROOT / ".github/workflows/upstream-pr-check.yml").read_text()
RELEASE = (ROOT / ".github/workflows/auto-release.yml").read_text()


class WorkflowPolicyTest(unittest.TestCase):
    def test_patchset_push_and_sync_triggers_exist(self) -> None:
        self.assertRegex(VALIDATION, r"(?m)^  push:\n    branches:\n      - patchset$")
        self.assertRegex(SYNC, r"(?m)^  schedule:$")
        self.assertRegex(SYNC, r"(?m)^  workflow_dispatch:$")
        self.assertNotIn("workflow_dispatch:", RELEASE)

    def test_sync_resolves_current_eligible_upstream(self) -> None:
        self.assertIn("refs/remotes/upstream/main", SYNC)
        self.assertIn('upstream-ci-ready.sh "$UPSTREAM_REPOSITORY" "$upstream_head"', SYNC)
        self.assertNotRegex(SYNC, r"upstream_(?:ref|sha):\s*[0-9a-f]{40}")
        readiness = (ROOT / "scripts/upstream-ci-ready.sh").read_text()
        self.assertIn("test", readiness)
        self.assertIn("frontend", readiness)
        self.assertIn("golangci-lint", readiness)

    def test_release_depends_on_validation(self) -> None:
        release_job = SYNC.split("\n  release:\n", 1)[1]
        self.assertRegex(release_job, r"needs:\n      - resolve\n      - validate")
        self.assertIn("needs.validate.result == 'success'", release_job)
        for gate in (
            "Apply patches",
            "Backend tests",
            "Backend lint and format checks",
            "Frontend checks",
            "Preflight patched image build",
        ):
            self.assertIn(f"- name: {gate}", VALIDATION)

    def test_collision_and_compare_and_swap_guards_exist(self) -> None:
        self.assertIn("version tag already exists", RELEASE)
        self.assertIn("GitHub release already exists", RELEASE)
        self.assertIn("versioned image tag already exists", RELEASE)
        self.assertIn("--atomic", RELEASE)
        self.assertEqual(RELEASE.count("--force-with-lease="), 3)
        self.assertIn(":latest-patch", RELEASE)

    def test_upstream_is_read_only(self) -> None:
        combined = SYNC + VALIDATION + RELEASE
        self.assertNotRegex(combined, r"git(?: -C \S+)? push upstream")
        self.assertNotRegex(
            combined,
            r"gh (?:api|pr|issue|release).*Wei-Shaw/sub2api",
        )
        self.assertNotIn("github.com/Wei-Shaw/sub2api.git@", combined)


if __name__ == "__main__":
    unittest.main()
