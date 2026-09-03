#!/usr/bin/env python3
"""Executable security policy for the release-related GitHub workflows.

The loader intentionally implements the small YAML subset used by GitHub
Actions instead of relying on PyYAML. In particular, the key ``on`` is kept as
a string and can never be resolved as the YAML 1.1 boolean ``True``.
"""

from __future__ import annotations

import re
import unittest
from dataclasses import dataclass
from pathlib import Path
from typing import Any, Iterable


ROOT = Path(__file__).parents[2]
WORKFLOW_DIR = ROOT / ".github" / "workflows"
VALIDATION_PATH = WORKFLOW_DIR / "upstream-pr-check.yml"
SYNC_PATH = WORKFLOW_DIR / "sync-upstream.yml"
RELEASE_PATH = WORKFLOW_DIR / "auto-release.yml"

WRITE_SCOPES = {
    "actions",
    "contents",
    "id-token",
    "packages",
    "pull-requests",
}

TOPICS = (
    "publish downstream Docker images",
    "cache pnpm dependencies with BuildKit",
    "parse singular OpenAI cached token details",
    "add shared endpoint account scheduling context",
    "schedule resilient OpenAI audio transcriptions",
    "expose OpenAI audio transcription transport",
    "proxy OpenAI realtime WebSocket sessions",
    "proxy OpenAI realtime REST endpoints",
    "moderate OpenAI realtime client events",
    "connect realtime REST moderation protocol",
    "preserve caller Codex system prompts",
)

MUTATION_RE = re.compile(
    r"(?:"
    r"\bgit\s+push\b|"
    r"\bgh\s+(?:pr\s+(?:create|edit|merge)|release\s+create|workflow\s+run)\b|"
    r"\bgh\s+api\b[^\n]*(?:--method|-X)\s+(?:POST|PUT|PATCH|DELETE)\b|"
    r"\bdocker\s+(?:push|buildx\s+build\b[^\n]*--push)\b"
    r")",
    re.IGNORECASE,
)


@dataclass(frozen=True)
class SourceLine:
    indent: int
    content: str


class WorkflowYaml:
    """Parse the indentation-based YAML subset used by workflow files."""

    def __init__(self, text: str):
        self.lines = self._tokenize(text)

    @staticmethod
    def _tokenize(text: str) -> list[SourceLine]:
        lines: list[SourceLine] = []
        for raw in text.splitlines():
            if not raw.strip() or raw.lstrip().startswith("#"):
                continue
            if "\t" in raw[: len(raw) - len(raw.lstrip())]:
                raise ValueError("tabs are not valid YAML indentation")
            indent = len(raw) - len(raw.lstrip(" "))
            lines.append(SourceLine(indent, raw[indent:]))
        return lines

    def load(self) -> dict[str, Any]:
        if not self.lines:
            return {}
        value, position = self._parse_block(0, self.lines[0].indent)
        if position != len(self.lines) or not isinstance(value, dict):
            raise ValueError("workflow root must be a mapping")
        return value

    def _parse_block(self, position: int, indent: int) -> tuple[Any, int]:
        if self.lines[position].content.startswith("- "):
            return self._parse_list(position, indent)
        return self._parse_mapping(position, indent)

    def _parse_mapping(
        self, position: int, indent: int
    ) -> tuple[dict[str, Any], int]:
        result: dict[str, Any] = {}
        while position < len(self.lines):
            line = self.lines[position]
            if line.indent < indent:
                break
            if line.indent != indent or line.content.startswith("- "):
                break
            key, separator, remainder = line.content.partition(":")
            if not separator:
                raise ValueError(f"expected mapping entry: {line.content}")
            key = self._unquote(key.strip())
            remainder = remainder.strip()
            position += 1
            if remainder in {"|", "|-", ">", ">-"}:
                value, position = self._parse_block_scalar(position, indent)
            elif remainder:
                value = self._parse_scalar(remainder)
            elif position < len(self.lines) and self.lines[position].indent > indent:
                value, position = self._parse_block(
                    position, self.lines[position].indent
                )
            else:
                value = None
            if key in result:
                raise ValueError(f"duplicate YAML key: {key}")
            result[key] = value
        return result, position

    def _parse_list(self, position: int, indent: int) -> tuple[list[Any], int]:
        result: list[Any] = []
        while position < len(self.lines):
            line = self.lines[position]
            if line.indent < indent:
                break
            if line.indent != indent or not line.content.startswith("- "):
                break
            remainder = line.content[2:].strip()
            position += 1
            if not remainder:
                if position >= len(self.lines) or self.lines[position].indent <= indent:
                    result.append(None)
                    continue
                value, position = self._parse_block(
                    position, self.lines[position].indent
                )
                result.append(value)
                continue
            key, separator, item_remainder = remainder.partition(":")
            if separator:
                item: dict[str, Any] = {
                    self._unquote(key.strip()): self._parse_scalar(
                        item_remainder.strip()
                    )
                    if item_remainder.strip()
                    else None
                }
                if position < len(self.lines) and self.lines[position].indent > indent:
                    extra, position = self._parse_mapping(
                        position, self.lines[position].indent
                    )
                    item.update(extra)
                result.append(item)
            else:
                result.append(self._parse_scalar(remainder))
        return result, position

    def _parse_block_scalar(self, position: int, parent_indent: int) -> tuple[str, int]:
        block: list[SourceLine] = []
        while position < len(self.lines) and self.lines[position].indent > parent_indent:
            block.append(self.lines[position])
            position += 1
        if not block:
            return "", position
        common_indent = min(line.indent for line in block)
        rendered = [
            " " * (line.indent - common_indent) + line.content for line in block
        ]
        return "\n".join(rendered), position

    @classmethod
    def _parse_scalar(cls, value: str) -> Any:
        if value == "{}":
            return {}
        if value == "[]":
            return []
        if value.startswith("[") and value.endswith("]"):
            return [
                cls._parse_scalar(part.strip())
                for part in value[1:-1].split(",")
                if part.strip()
            ]
        lowered = value.lower()
        if lowered == "true":
            return True
        if lowered == "false":
            return False
        if lowered in {"null", "~"}:
            return None
        return cls._unquote(value)

    @staticmethod
    def _unquote(value: str) -> str:
        if len(value) >= 2 and value[0] == value[-1] and value[0] in {'"', "'"}:
            return value[1:-1]
        return value


@dataclass(frozen=True)
class Invocation:
    event: str
    ref: str = ""
    after: str = ""
    sha: str = ""
    inputs: tuple[tuple[str, str], ...] = ()


class WorkflowModel:
    def __init__(self, path: Path):
        self.path = path
        self.text = path.read_text(encoding="utf-8")
        self.data = WorkflowYaml(self.text).load()
        self.jobs = self._mapping(self.data.get("jobs"), "jobs")

    @staticmethod
    def _mapping(value: Any, label: str) -> dict[str, Any]:
        if value is None:
            return {}
        if not isinstance(value, dict):
            raise AssertionError(f"{label} must be a mapping")
        return value

    @property
    def triggers(self) -> dict[str, Any]:
        value = self.data.get("on")
        if isinstance(value, str):
            return {value: None}
        if isinstance(value, list):
            return {str(item): None for item in value}
        return self._mapping(value, f"{self.path.name} triggers")

    @property
    def concurrency(self) -> dict[str, Any]:
        value = self.data.get("concurrency")
        if isinstance(value, str):
            return {"group": value}
        return self._mapping(value, f"{self.path.name} concurrency")

    def accepts(self, invocation: Invocation) -> bool:
        if invocation.event not in self.triggers:
            return False
        if invocation.event != "push":
            return True
        config = self.triggers["push"]
        if not isinstance(config, dict):
            return True
        branches = config.get("branches")
        if isinstance(branches, str):
            branches = [branches]
        if not branches:
            return True
        branch = invocation.ref.removeprefix("refs/heads/")
        return branch in branches

    def job_needs(self, job_id: str) -> set[str]:
        value = self.jobs[job_id].get("needs")
        if value is None:
            return set()
        if isinstance(value, list):
            return {str(item) for item in value}
        return {str(value)}

    def ancestors(self, job_id: str) -> set[str]:
        found: set[str] = set()
        pending = list(self.job_needs(job_id))
        while pending:
            candidate = pending.pop()
            if candidate in found:
                continue
            if candidate not in self.jobs:
                raise AssertionError(f"{job_id} needs unknown job {candidate}")
            found.add(candidate)
            pending.extend(self.job_needs(candidate))
        return found

    def called_workflows(self) -> dict[str, str]:
        calls: dict[str, str] = {}
        for job_id, job in self.jobs.items():
            uses = str(job.get("uses") or "")
            if uses.endswith(".yml") or ".yml@" in uses:
                calls[job_id] = uses
        return calls

    def all_steps(self) -> Iterable[tuple[str, dict[str, Any]]]:
        for job_id, job in self.jobs.items():
            steps = job.get("steps") or []
            if not isinstance(steps, list):
                raise AssertionError(f"steps for {job_id} must be a list")
            for step in steps:
                if not isinstance(step, dict):
                    raise AssertionError(f"step in {job_id} must be a mapping")
                yield job_id, step

    def job_text(self, job_id: str) -> str:
        job = self.jobs[job_id]
        parts = [job_id, str(job.get("name") or ""), str(job.get("uses") or "")]
        for current_id, step in self.all_steps():
            if current_id != job_id:
                continue
            parts.extend(
                (
                    str(step.get("name") or ""),
                    str(step.get("uses") or ""),
                    str(step.get("run") or ""),
                    str(step.get("with") or ""),
                )
            )
        return "\n".join(parts)

    def mutating_jobs(self) -> set[str]:
        result: set[str] = set()
        for job_id in self.jobs:
            text = self.job_text(job_id)
            if re.search(r"(?:^|/)auto-release\.yml(?:@.+)?$", str(self.jobs[job_id].get("uses") or "")):
                result.add(job_id)
                continue
            if MUTATION_RE.search(text):
                result.add(job_id)
                continue
            for current_id, step in self.all_steps():
                if current_id != job_id:
                    continue
                uses = str(step.get("uses") or "")
                options = step.get("with") or {}
                if (
                    "docker/build-push-action" in uses
                    and isinstance(options, dict)
                    and options.get("push") is True
                ):
                    result.add(job_id)
        return result

    def reachable_after_failure(self, job_id: str, failed_job: str) -> bool:
        memo: dict[str, bool] = {}

        def visit(candidate: str) -> bool:
            if candidate == failed_job:
                return False
            if candidate in memo:
                return memo[candidate]
            needs = self.job_needs(candidate)
            failed_needs = {need for need in needs if not visit(need)}
            if not failed_needs:
                memo[candidate] = True
                return True

            condition = str(self.jobs[candidate].get("if") or "")
            if "always()" not in condition:
                memo[candidate] = False
                return False
            for need in failed_needs:
                success_guard = re.search(
                    rf"needs\.{re.escape(need)}\.result\s*==\s*['\"]success['\"]",
                    condition,
                )
                if success_guard:
                    memo[candidate] = False
                    return False
            memo[candidate] = True
            return True

        return visit(job_id)

    def effective_permissions(self, job_id: str) -> dict[str, str]:
        job_permissions = self.jobs[job_id].get("permissions")
        value = (
            job_permissions
            if job_permissions is not None
            else self.data.get("permissions")
        )
        if value in (None, {}):
            return {}
        if isinstance(value, str):
            return {"*": value}
        if not isinstance(value, dict):
            raise AssertionError(f"invalid permissions for {job_id}: {value!r}")
        return {str(scope): str(access) for scope, access in value.items()}


def find_call(model: WorkflowModel, filename: str) -> str:
    matches = [
        job_id
        for job_id, uses in model.called_workflows().items()
        if re.search(rf"(?:^|/){re.escape(filename)}(?:@.+)?$", uses)
    ]
    if len(matches) != 1:
        raise AssertionError(
            f"expected one {filename} call in {model.path.name}, found {matches}"
        )
    return matches[0]


def assert_full_sha(test: unittest.TestCase, expression: str, field: str) -> None:
    test.assertRegex(expression, rf"\b{re.escape(field)}\b")
    test.assertRegex(expression, r"[0-9a-f]{40}|github\.(?:event\.after|sha)|needs\.")


class WorkflowPolicyTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        cls.validation = WorkflowModel(VALIDATION_PATH)
        cls.sync = WorkflowModel(SYNC_PATH)
        cls.release = WorkflowModel(RELEASE_PATH)
        cls.models = (cls.validation, cls.sync, cls.release)

    def test_yaml_loader_preserves_on_key(self) -> None:
        fixture = WorkflowYaml("on:\n  push:\n    branches: [patchset]\n").load()
        self.assertIn("on", fixture)
        self.assertNotIn(True, fixture)
        self.assertEqual(fixture["on"]["push"]["branches"], ["patchset"])

    def test_executable_event_matrix_and_spoof_resistance(self) -> None:
        sha_a = "1" * 40
        sha_b = "2" * 40
        fixtures = (
            (self.validation, Invocation("push", "refs/heads/patchset", sha_a, sha_b), True),
            (self.validation, Invocation("push", "refs/heads/main", sha_a, sha_b), False),
            (self.validation, Invocation("workflow_dispatch", sha=sha_b), True),
            (self.validation, Invocation("schedule"), False),
            (self.sync, Invocation("schedule"), True),
            (self.sync, Invocation("workflow_dispatch"), True),
            (self.sync, Invocation("push", "refs/heads/patchset", sha_a), False),
            (self.release, Invocation("workflow_dispatch"), False),
            (self.release, Invocation("repository_dispatch"), False),
        )
        for model, invocation, expected in fixtures:
            with self.subTest(workflow=model.path.name, invocation=invocation):
                self.assertEqual(model.accepts(invocation), expected)

        for value in ("", "false", "true", "garbage", "1", "yes"):
            spoof = Invocation(
                "workflow_dispatch",
                sha=sha_b,
                inputs=(("release_after_validation", value),),
            )
            self.assertTrue(self.validation.accepts(spoof))
            self.assertFalse(self.release.accepts(spoof))

        self.assertNotIn("release_after_validation", self.validation.text)
        self.assertFalse(self.validation.mutating_jobs())
        self.assertFalse(self.validation.called_workflows())

    def test_immutable_patchset_and_upstream_checkout(self) -> None:
        text = self.validation.text
        self.assertIn("github.event.after", text)
        self.assertIn("github.sha", text)
        self.assertRegex(text, r"patchset_sha[^\n]*(?:github\.event\.after|github\.sha)")
        self.assertRegex(text, r"git\s+-C\s+[^\n]+rev-parse\s+HEAD")
        self.assertRegex(text, r"patchset (?:checkout|SHA) mismatch|checked[^\n]+patchset")
        patchset_checkouts: list[dict[str, Any]] = []
        for _, step in self.validation.all_steps():
            if not str(step.get("uses") or "").startswith("actions/checkout@"):
                continue
            options = step.get("with") or {}
            self.assertIsInstance(options, dict)
            self.assertNotEqual(options.get("ref"), "patchset")
            if options.get("path") == "sub2api-patch":
                patchset_checkouts.append(options)
        self.assertTrue(patchset_checkouts)
        for checkout in patchset_checkouts:
            self.assertRegex(
                str(checkout.get("ref") or ""),
                r"patchset_sha|event\.after|github\.sha|needs\.|steps\.",
            )
        self.assertIn("Wei-Shaw/sub2api", text)
        self.assertIn("scripts/upstream-ci-ready.sh", text)
        self.assertRegex(text, r"upstream_source_sha")
        self.assertRegex(text, r"EXPECTED_BASE_SHA")

        event_sha = "1" * 40
        moved_branch_sha = "2" * 40
        selected = event_sha
        self.assertNotEqual(event_sha, moved_branch_sha)
        self.assertEqual(selected, event_sha)

    def test_provenance_bundle_has_distinct_typed_identities(self) -> None:
        fields = (
            "patchset_sha",
            "upstream_source_sha",
            "mirror_candidate_sha",
            "replay_tree_sha",
        )
        validation_text = self.validation.text
        for field in fields:
            with self.subTest(field=field):
                self.assertIn(field, validation_text)
        for field in (
            "repository",
            "workflow",
            "run_id",
            "run_attempt",
            "conclusion",
        ):
            self.assertRegex(validation_text, rf"\b{field}\b")
        self.assertRegex(validation_text, r"full_sha[^\n]*\[0-9a-f\]\{40\}")
        self.assertIn("GITHUB_STEP_SUMMARY", validation_text)
        self.assertIn("actions/upload-artifact", validation_text)

        fixture = {
            "patchset_sha": "1" * 40,
            "upstream_source_sha": "2" * 40,
            "mirror_candidate_sha": "3" * 40,
            "replay_tree_sha": "4" * 40,
        }
        self.assertEqual(len(set(fixture.values())), len(fixture))
        for field, value in fixture.items():
            assert_full_sha(self, f"{field}={value}", field)

    def test_trusted_sync_dag_and_gate_failure_reachability(self) -> None:
        validation_job = find_call(self.sync, "upstream-pr-check.yml")
        release_job = find_call(self.sync, "auto-release.yml")
        release_ancestors = self.sync.ancestors(release_job)
        self.assertIn(validation_job, release_ancestors)
        self.assertGreaterEqual(len(release_ancestors), 2)

        release_config = self.sync.jobs[release_job]
        condition = str(release_config.get("if") or "")
        self.assertRegex(condition, r"release_after_validation")
        self.assertRegex(condition, r"(?:==\s*'true'|==\s*true|fromJSON)")
        self.assertNotRegex(condition, r"\|\||\?\?")

        gate_patterns = {
            "provenance": r"provenance|bundle|identity",
            "upstream-policy": r"upstream-ci-ready\.sh",
            "checkout": r"actions/checkout@|checkout --detach",
            "replay": r"apply-patches\.sh",
            "sanitizer": r"check-no-pr-issue-refs\.sh",
            "backend": r"go test \./\.\.\.",
            "go-lint-format": r"golangci|gofmt|goimports|format checks",
            "frontend": r"frozen-lockfile.*typecheck.*lint|frontend checks",
            "tree": r"replay_tree_sha|\^\{tree\}",
            "reference": r"reference sanitizer|check-no-pr-issue-refs",
            "diff": r"git diff --check",
            "version": r"compute-next-version\.sh",
            "tag-release-lookup": r"matching-refs/tags|release list|tag availability",
            "notes": r"release-notes|release notes",
            "image-owner": r"repository_owner|image owner",
            "buildx": r"setup-buildx-action",
            "docker-preflight": r"Preflight.*image|push['\"]?\s*:\s*(?:false|False)",
        }
        preparation = "\n".join(
            [self.validation.text, self.release.text, self.sync.text]
        )
        for gate, pattern in gate_patterns.items():
            with self.subTest(gate=gate):
                self.assertRegex(preparation, re.compile(pattern, re.I | re.S))

        release_mutations = self.release.mutating_jobs()
        self.assertTrue(release_mutations)
        for mutation_job in release_mutations:
            ancestors = self.release.ancestors(mutation_job)
            self.assertTrue(
                ancestors,
                f"release mutation job {mutation_job} has no preparation dependency",
            )
            for failed_gate_job in ancestors:
                self.assertFalse(
                    self.release.reachable_after_failure(
                        mutation_job, failed_gate_job
                    ),
                    f"{mutation_job} remains reachable after {failed_gate_job} fails",
                )

        self.assertEqual(self.sync.mutating_jobs(), {release_job})

    def test_effective_permissions_are_default_deny_and_job_scoped(self) -> None:
        for model in self.models:
            with self.subTest(workflow=model.path.name):
                root_permissions = model.data.get("permissions")
                self.assertIsInstance(root_permissions, dict)
                self.assertFalse(
                    any(str(value) == "write" for value in root_permissions.values())
                )
            mutations = model.mutating_jobs()
            for job_id in model.jobs:
                effective = model.effective_permissions(job_id)
                writes = {
                    scope
                    for scope, access in effective.items()
                    if access == "write"
                }
                if job_id not in mutations:
                    self.assertFalse(
                        writes & WRITE_SCOPES,
                        f"verification job {model.path.name}:{job_id} has {writes}",
                    )
                else:
                    self.assertTrue(writes)
                    self.assertTrue(model.ancestors(job_id))

        for job_id in self.validation.jobs:
            permissions = self.validation.effective_permissions(job_id)
            self.assertFalse(
                {
                    scope
                    for scope, access in permissions.items()
                    if access == "write" and scope in WRITE_SCOPES
                }
            )

    def test_concurrency_fixtures_preserve_running_release_authority(self) -> None:
        sync_concurrency = self.sync.concurrency
        self.assertEqual(sync_concurrency.get("group"), "sub2api-release-mutation")
        self.assertIs(sync_concurrency.get("cancel-in-progress"), False)

        validation_concurrency = self.validation.concurrency
        validation_group = str(validation_concurrency.get("group") or "")
        self.assertIn("sub2api-patch-validation-", validation_group)
        self.assertRegex(validation_group, r"patchset_sha|event\.after|github\.sha")
        self.assertIs(validation_concurrency.get("cancel-in-progress"), True)
        self.assertNotEqual(validation_group, sync_concurrency.get("group"))

        running_sync = {
            "group": sync_concurrency["group"],
            "cancel": sync_concurrency["cancel-in-progress"],
            "state": "running",
        }
        later_sync = dict(running_sync, state="pending")
        push_validation = {
            "group": validation_group.replace("patchset_sha", "1" * 40),
            "cancel": validation_concurrency["cancel-in-progress"],
            "state": "running",
        }
        self.assertFalse(running_sync["cancel"])
        self.assertEqual(running_sync["group"], later_sync["group"])
        self.assertNotEqual(running_sync["group"], push_validation["group"])
        self.assertIn(find_call(self.sync, "auto-release.yml"), self.sync.jobs)

    def test_post_validation_rechecks_and_compare_and_swap_guards(self) -> None:
        mutation_text = "\n".join(
            self.release.job_text(job_id)
            for job_id in self.release.mutating_jobs()
        )
        checks = {
            "patchset": r"patchset_sha|patchset moved",
            "upstream": r"upstream_source_sha|upstream identity",
            "mirror ancestry": r"mirror_candidate_sha|parent chain|rev-parse.*\^2",
            "replay tree": r"replay_tree_sha|\^\{tree\}",
            "target refs": r"expected.*(?:main|patched|mirror)|force-with-lease|old[_ -]sha",
            "tag": r"tag availability|matching-refs/tags|release list",
        }
        for race, pattern in checks.items():
            with self.subTest(race=race):
                self.assertRegex(mutation_text, re.compile(pattern, re.I | re.S))
        self.assertRegex(
            mutation_text,
            r"--force-with-lease|git update-ref|expected[_ -](?:old|sha)|old[_ -]sha",
        )
        self.assertNotRegex(mutation_text, r"git\s+push[^\n]*--force(?:\s|$)")

    def test_release_outputs_and_order_are_preserved(self) -> None:
        text = self.release.text
        positions: list[int] = []
        for topic in TOPICS:
            self.assertEqual(text.count(topic), 1, topic)
            positions.append(text.index(topic))
        self.assertEqual(positions, sorted(positions))

        self.assertIn("scripts/compute-next-version.sh", text)
        for field in (
            "patchset_sha",
            "upstream_source_sha",
            "replay_tree_sha",
        ):
            self.assertIn(field, text)
        self.assertRegex(text, r"sub2api-patch:.*VERSION")
        self.assertIn("sub2api-patch:latest-patch", text)
        self.assertIn("org.opencontainers.image.version", text)
        self.assertIn("org.opencontainers.image.revision", text)
        self.assertIn("type=gha,scope=sub2api-patch-release", text)
        self.assertIn("type=gha,mode=max,scope=sub2api-patch-release", text)
        self.assertRegex(
            text,
            re.compile(
                r"(?:git(?:\s+-C\s+\S+)?\s+push|update-ref|git/refs)"
                r".{0,300}(?:patched|PATCHED_BRANCH)",
                re.S,
            ),
        )
        self.assertRegex(
            text,
            re.compile(
                r"(?:git(?:\s+-C\s+\S+)?\s+push|update-ref|git/refs)"
                r".{0,300}(?:tag|VERSION)",
                re.S,
            ),
        )
        self.assertRegex(text, r"gh release create")

        preflight_steps = [
            step
            for _, step in self.validation.all_steps()
            if "docker/build-push-action" in str(step.get("uses") or "")
        ]
        self.assertTrue(preflight_steps)
        for step in preflight_steps:
            options = step.get("with") or {}
            self.assertIsInstance(options, dict)
            self.assertIs(options.get("push"), False)
            self.assertIs(options.get("load"), False)

    def test_upstream_is_asserted_and_remains_read_only(self) -> None:
        all_text = "\n".join(model.text for model in self.models)
        self.assertIn("UPSTREAM_REPOSITORY: Wei-Shaw/sub2api", all_text)
        self.assertRegex(
            all_text,
            r"(?:assert|verify|unexpected)[^\n]*(?:Wei-Shaw/sub2api|UPSTREAM_REPOSITORY)",
        )
        self.assertRegex(all_text, r"git remote get-url|remote.*Wei-Shaw/sub2api")

        forbidden = (
            r"git\s+push\s+(?:upstream|https://github\.com/Wei-Shaw/sub2api)",
            r"gh\s+api[^\n]*repos/(?:Wei-Shaw/sub2api|\$\{?UPSTREAM_REPOSITORY\}?)[^\n]*"
            r"(?:--method|-X)\s+(?:POST|PUT|PATCH|DELETE)",
            r"gh\s+pr\s+(?:create|edit|merge)[^\n]*Wei-Shaw/sub2api",
            r"gh\s+release\s+create[^\n]*Wei-Shaw/sub2api",
            r"ghcr\.io/wei-shaw/",
        )
        for pattern in forbidden:
            self.assertNotRegex(all_text, re.compile(pattern, re.I))

        upstream_fetches = re.findall(
            r"git(?:\s+-C\s+\S+)?\s+fetch[^\n]+", all_text
        )
        self.assertTrue(upstream_fetches)
        for fetch in upstream_fetches:
            if "upstream" in fetch.lower():
                self.assertNotIn("--upload-pack", fetch)


if __name__ == "__main__":
    unittest.main(verbosity=2)
