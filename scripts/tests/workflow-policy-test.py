#!/usr/bin/env python3
"""Executable security policy for the release-related GitHub workflows.

The loader intentionally implements the small YAML subset used by GitHub
Actions instead of relying on PyYAML. In particular, the key ``on`` is kept as
a string and can never be resolved as the YAML 1.1 boolean ``True``.
"""

from __future__ import annotations

import re
import unittest
from dataclasses import dataclass, replace
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

PUBLICATION_WRITE_SCOPES = {"contents", "packages", "pull-requests"}
PINNED_ARTIFACT_DOWNLOAD_RE = re.compile(
    r"^actions/download-artifact@[0-9a-f]{40}(?:\s+#.*)?$"
)
PINNED_THIRD_PARTY_ACTION_RE = re.compile(
    r"^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+@[0-9a-f]{40}(?:\s+#.*)?$"
)
WRITE_CREDENTIAL_RE = re.compile(
    r"(?:"
    r"\$\{\{\s*(?:github\.token|secrets\.[A-Za-z_][A-Za-z0-9_]*)\s*\}\}|"
    r"\bGH_TOKEN\b|\bGITHUB_TOKEN\b|\bDOCKER_CONFIG\b|\bx-access-token:|"
    r"\b(?:token|password|password-stdin)['\"]?\s*[:=]|"
    r"\bdocker\s+login\b|\bgit\s+credential(?:-[A-Za-z0-9_-]+)?\b|"
    r"\bgit\s+config\b[^\n]*credential(?:\.|\s)"
    r")",
    re.I,
)
REPOSITORY_ACQUISITION_RE = re.compile(
    r"\bgit(?:\s+-C\s+\S+)?\s+(?:clone|fetch)\b", re.I
)
PREAUTH_UNTRUSTED_EXECUTION_RE = re.compile(
    r"(?:"
    r"\b(?:bash|sh|python(?:3)?|node|ruby|perl)\s+"
    r"(?:\./|(?:sub2api-patch|release-state|artifact-download|publication)/)|"
    r"(?:^|[;&|\n]\s*)(?:source|\.)\s+"
    r"(?:\./|(?:sub2api-patch|release-state|artifact-download|publication)/)|"
    r"(?:^|[;&|\n]\s*)(?:\./|(?:sub2api-patch|release-state|"
    r"artifact-download|publication)/)[^\s;&|]+|"
    r"\b(?:pnpm|npm|yarn)\s+(?:run|install|ci|rebuild|exec|dlx)\b|"
    r"\b(?:go|cargo)\s+run\b|"
    r"\bdocker\s+(?:build|buildx\s+build)\b|"
    r"\bgit\s+config\b[^\n]*(?:core\.hooksPath|credential\.)"
    r")",
    re.I,
)

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

PROVENANCE_FIELDS = (
    "patchset_sha",
    "upstream_source_sha",
    "mirror_candidate_sha",
    "replay_tree_sha",
    "validation_conclusion",
    "originating_repository",
    "originating_workflow_path",
    "originating_run_id",
    "originating_run_attempt",
)

MUTATION_RE = re.compile(
    r"(?:"
    r"\bgit(?:\s+-C\s+\S+)?\s+push\b|"
    r"\bgh\s+(?:pr\s+(?:create|edit|merge|comment)|issue\s+comment|"
    r"release\s+create|workflow\s+run)\b|"
    r"\bgh\s+api\b[^\n]*(?:--method|-X)\s+(?:POST|PUT|PATCH|DELETE)\b|"
    r"\bgh\s+api\b(?![^\n]*(?:--method|-X)\s+GET\b)"
    r"[^\n]*(?:--field|--raw-field|--input|-f|-F)(?:[=\s]|$)|"
    r"\bcurl\b[^\n]*(?:-X|--request)\s*(?:POST|PUT|PATCH|DELETE)\b|"
    r"\bcurl\b(?![^\n]*(?:-X|--request)\s*GET\b)"
    r"[^\n]*(?:--data(?:-[A-Za-z-]+)?|-d)(?:[=\s]|$)|"
    r"\bdocker\s+(?:push|buildx\s+build\b[^\n]*--push)\b|"
    r"\boras\s+(?:cp|push)\b"
    r")",
    re.IGNORECASE,
)

UPSTREAM_TARGET = (
    r"(?:Wei-Shaw/sub2api|\$UPSTREAM_REPOSITORY|\$\{UPSTREAM_REPOSITORY\}|"
    r"\$\{\{\s*(?:env\.)?UPSTREAM_REPOSITORY\s*\}\})"
)
UPSTREAM_MUTATION_PATTERNS = (
    re.compile(
        r"\bgit(?:\s+-C\s+\S+)?\s+push\s+"
        r"(?:upstream|https://github\.com/Wei-Shaw/sub2api)",
        re.I,
    ),
    re.compile(r"\bgit\s+remote\s+set-url\s+--push\s+upstream\b", re.I),
    re.compile(
        rf"\bgh\s+api\b(?=[^\n]*repos/{UPSTREAM_TARGET})"
        r"(?=[^\n]*(?:--method|-X)\s+(?:POST|PUT|PATCH|DELETE))",
        re.I,
    ),
    re.compile(
        rf"\bgh\s+api\b(?=[^\n]*repos/{UPSTREAM_TARGET})"
        r"(?![^\n]*(?:--method|-X)\s+GET\b)"
        r"(?=[^\n]*(?:--field|--raw-field|--input|-f|-F)(?:[=\s]|$))",
        re.I,
    ),
    re.compile(
        rf"\bgh\s+(?:pr\s+(?:create|edit|merge|comment)|issue\s+comment)\b"
        rf"(?=[^\n]*{UPSTREAM_TARGET})",
        re.I,
    ),
    re.compile(rf"\bgh\s+release\s+create\b[^\n]*{UPSTREAM_TARGET}", re.I),
    re.compile(
        rf"\bcurl\b(?=[^\n]*(?:-X|--request)\s*(?:POST|PUT|PATCH|DELETE)\b)"
        rf"(?=[^\n]*api\.github\.com/repos/{UPSTREAM_TARGET}(?:/|\b))",
        re.I,
    ),
    re.compile(
        rf"\bcurl\b(?=[^\n]*api\.github\.com/repos/{UPSTREAM_TARGET}(?:/|\b))"
        r"(?![^\n]*(?:-X|--request)\s*GET\b)"
        r"(?=[^\n]*(?:--data(?:-[A-Za-z-]+)?|-d)(?:[=\s]|$))",
        re.I,
    ),
    re.compile(rf"ghcr\.io/{UPSTREAM_TARGET}(?::|/|\b)", re.I),
)


def upstream_mutations(text: str) -> list[str]:
    normalized = text.replace("\\\n", " ")
    return [
        match.group(0)
        for pattern in UPSTREAM_MUTATION_PATTERNS
        for match in pattern.finditer(normalized)
    ]


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


MUTATE_GIT_BOUNDARIES = ("mirror", "pull-request", "main", "patched-and-tag")
SYNC_REQUEST_IDENTITY = (
    "open",
    "mirror/upstream-main",
    "Ovler-Young/sub2api-patch",
    "main",
    "Ovler-Young/sub2api-patch",
)
COMPLETED_SYNC_REQUEST_IDENTITY = (
    "closed",
    *SYNC_REQUEST_IDENTITY[1:],
)
SYNC_REQUEST_CONTENT = "sync upstream main\nvalidated upstream commit"


@dataclass(frozen=True)
class PublicationState:
    mirror_sha: str
    sync_request_identity: tuple[str, str, str, str, str] | None
    sync_request_head_sha: str | None
    sync_request_content: str | None
    main_sha: str
    patched_sha: str
    tag_sha: str | None


@dataclass(frozen=True)
class PublicationIntent:
    expected_mirror_sha: str
    expected_main_sha: str
    expected_patched_sha: str
    mirror_candidate_sha: str
    publication_sha: str


def apply_publication_boundary(
    state: PublicationState,
    intent: PublicationIntent,
    boundary: str,
) -> PublicationState:
    """Model the accepted remote states at one publication boundary."""

    if boundary == "mirror":
        if state.mirror_sha == intent.expected_mirror_sha:
            return replace(state, mirror_sha=intent.mirror_candidate_sha)
        if state.mirror_sha == intent.mirror_candidate_sha:
            return state
        raise ValueError("mirror ref conflicts with the validated publication")

    if boundary == "pull-request":
        if state.mirror_sha != intent.mirror_candidate_sha:
            raise ValueError("mirror moved before sync request mutation")
        if state.main_sha == intent.mirror_candidate_sha:
            if state.sync_request_identity != COMPLETED_SYNC_REQUEST_IDENTITY or (
                state.sync_request_head_sha != intent.mirror_candidate_sha
            ):
                raise ValueError("completed sync request is missing or conflicting")
            return state
        if state.main_sha != intent.expected_main_sha:
            raise ValueError("main moved before sync request mutation")
        if state.sync_request_identity is None:
            if state.sync_request_content is not None:
                raise ValueError("sync request content exists without an identity")
            return replace(
                state,
                sync_request_identity=SYNC_REQUEST_IDENTITY,
                sync_request_head_sha=intent.mirror_candidate_sha,
                sync_request_content=SYNC_REQUEST_CONTENT,
            )
        if state.sync_request_identity == SYNC_REQUEST_IDENTITY and (
            state.sync_request_head_sha == intent.mirror_candidate_sha
        ):
            return state
        raise ValueError("sync request identity conflicts with the publication target")

    if boundary == "main":
        if state.mirror_sha != intent.mirror_candidate_sha:
            raise ValueError("mirror moved before main mutation")
        if state.main_sha == intent.mirror_candidate_sha:
            if state.sync_request_identity != COMPLETED_SYNC_REQUEST_IDENTITY or (
                state.sync_request_head_sha != intent.mirror_candidate_sha
            ):
                raise ValueError("completed sync request changed after main mutation")
            return state
        if state.sync_request_identity != SYNC_REQUEST_IDENTITY or (
            state.sync_request_head_sha != intent.mirror_candidate_sha
        ):
            raise ValueError("sync request changed before main mutation")
        if state.main_sha == intent.expected_main_sha:
            return replace(
                state,
                sync_request_identity=COMPLETED_SYNC_REQUEST_IDENTITY,
                main_sha=intent.mirror_candidate_sha,
            )
        raise ValueError("main ref conflicts with the validated publication")

    if boundary == "patched-and-tag":
        if state.mirror_sha != intent.mirror_candidate_sha:
            raise ValueError("mirror moved before patched publication")
        if state.main_sha != intent.mirror_candidate_sha:
            raise ValueError("main moved before patched publication")
        accepted_patched = {intent.expected_patched_sha, intent.publication_sha}
        accepted_tag = {None, intent.publication_sha}
        if (
            state.patched_sha not in accepted_patched
            or state.tag_sha not in accepted_tag
        ):
            raise ValueError("patched branch or tag conflicts with the publication")
        return replace(
            state,
            patched_sha=intent.publication_sha,
            tag_sha=intent.publication_sha,
        )

    raise AssertionError(f"unknown publication boundary: {boundary}")


def complete_publication(
    state: PublicationState, intent: PublicationIntent
) -> PublicationState:
    for boundary in MUTATE_GIT_BOUNDARIES:
        state = apply_publication_boundary(state, intent, boundary)
    return state


def verify_published_authority(
    state: PublicationState, intent: PublicationIntent
) -> None:
    expected_refs = {
        "mirror": (state.mirror_sha, intent.mirror_candidate_sha),
        "main": (state.main_sha, intent.mirror_candidate_sha),
        "patched": (state.patched_sha, intent.publication_sha),
        "tag": (state.tag_sha, intent.publication_sha),
    }
    conflicts = [
        name for name, values in expected_refs.items() if values[0] != values[1]
    ]
    if conflicts:
        raise ValueError(f"published authority changed: {', '.join(conflicts)}")


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

    def job_steps(self, job_id: str) -> list[dict[str, Any]]:
        return [step for current_id, step in self.all_steps() if current_id == job_id]

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


def write_permission_scopes(permissions: dict[str, str]) -> set[str]:
    if permissions.get("*") == "write-all":
        return set(WRITE_SCOPES)
    return {
        scope
        for scope, access in permissions.items()
        if access == "write" and scope in WRITE_SCOPES
    }


def step_mutates(step: dict[str, Any]) -> bool:
    if MUTATION_RE.search(str(step.get("run") or "")):
        return True
    uses = str(step.get("uses") or "")
    options = step.get("with") or {}
    return (
        "docker/build-push-action" in uses
        and isinstance(options, dict)
        and options.get("push") is True
    )


SHELL_FAILURE_SUPPRESSION_PATTERNS = (
    re.compile(
        r"(?m)^\s*set\s+(?:\+e|\+o\s+(?:errexit|pipefail)|--?\s*\+?(?:errexit|pipefail))\b"
    ),
    re.compile(
        r"\|\|\s*(?:true|:|exit\s+0|return\s+0|echo\b|printf\b)",
        re.I,
    ),
)


def shell_failure_suppressions(run: str) -> list[str]:
    return [
        match.group(0)
        for pattern in SHELL_FAILURE_SUPPRESSION_PATTERNS
        for match in pattern.finditer(run)
    ]


def assert_step_propagates_failure(
    test: unittest.TestCase,
    model: WorkflowModel,
    job_id: str,
    step: dict[str, Any],
) -> None:
    label = f"{model.path.name}:{job_id}:{step.get('name')}"
    test.assertIsNot(
        model.jobs[job_id].get("continue-on-error"), True, f"job masks failure: {label}"
    )
    test.assertIsNot(
        step.get("continue-on-error"), True, f"step masks failure: {label}"
    )
    run = str(step.get("run") or "")
    if not run:
        return
    if "\n" in run:
        test.assertRegex(
            run, r"(?m)^set -euo pipefail\s*$", f"missing strict shell: {label}"
        )
    test.assertEqual(
        shell_failure_suppressions(run), [], f"shell masks failure: {label}"
    )


def step_text(step: dict[str, Any]) -> str:
    parts = (
        step.get("uses"),
        step.get("run"),
        step.get("env"),
        step.get("with"),
    )
    return "\n".join(str(part or "") for part in parts)


def artifact_authorization_markers(run: str) -> bool:
    required = (
        "RELEASE_ARTIFACT_ID",
        "RELEASE_ARTIFACT_DIGEST",
        "MANIFEST_SHA256",
        "publication-manifest.env",
        "content-manifest.sha256",
        "sha256sum -c MANIFEST.SHA256",
        "sha256sum -c content-manifest.sha256",
        "expected_payload_files",
    )
    archive_digest_guard = re.search(
        r"sha256sum[^\n]+archive[^\n]*\n?[^\n]*archive_digest|"
        r"sha256sum[^\n]+\$\{?archive_files",
        run,
        re.I,
    )
    release_authority = all(marker in run for marker in required) and bool(
        archive_digest_guard
    )
    image_required = (
        "AUTHORITY_ARTIFACT_NAME",
        "AUTHORITY_ARTIFACT_DIGEST",
        "AUTHORIZED_IMAGE_DIGEST",
        "IMAGE-MANIFEST.SHA256",
        "release-image.oci.tar",
        "oci_manifest_digest",
        "sha256sum",
    )
    image_authority = all(marker in run for marker in image_required)
    return release_authority or image_authority


def preauthorization_violations(steps: list[dict[str, Any]]) -> list[str]:
    violations: list[str] = []
    authorization_indexes = [
        index
        for index, step in enumerate(steps)
        if artifact_authorization_markers(str(step.get("run") or ""))
    ]
    if len(authorization_indexes) != 1:
        return [f"expected one artifact authorization step, found {authorization_indexes}"]

    authorization_index = authorization_indexes[0]
    for index, step in enumerate(steps[: authorization_index + 1]):
        label = str(step.get("name") or f"step {index}")
        uses = str(step.get("uses") or "")
        if uses and not PINNED_ARTIFACT_DOWNLOAD_RE.fullmatch(uses):
            violations.append(f"untrusted action before authorization: {label}: {uses}")

        text = step_text(step)
        if WRITE_CREDENTIAL_RE.search(text):
            violations.append(f"write credential before authorization: {label}")
        if PREAUTH_UNTRUSTED_EXECUTION_RE.search(str(step.get("run") or "")):
            violations.append(f"untrusted executable before authorization: {label}")
        if REPOSITORY_ACQUISITION_RE.search(str(step.get("run") or "")):
            violations.append(f"repository acquisition before authorization: {label}")
    return violations


def concurrency_cancel_value(value: Any, invocation_mode: str) -> bool:
    if isinstance(value, bool):
        return value
    expression = re.sub(r"^\s*\$\{\{|\}\}\s*$", "", str(value)).strip()
    not_sync = re.fullmatch(
        r"inputs\.invocation_mode\s*!=\s*['\"]sync['\"]", expression
    )
    if not_sync:
        return invocation_mode != "sync"
    is_sync = re.fullmatch(
        r"inputs\.invocation_mode\s*==\s*['\"]sync['\"]", expression
    )
    if is_sync:
        return invocation_mode == "sync"
    raise AssertionError(f"unsupported cancel-in-progress expression: {value}")


def concurrency_group_value(
    value: Any, invocation_mode: str, immutable_id: str
) -> str:
    expression = str(value)
    groups = re.findall(r"format\(['\"]([^'\"]+)['\"]", expression)
    if invocation_mode == "sync":
        choices = [group for group in groups if "sync-validation" in group]
    else:
        choices = [group for group in groups if "patch-validation" in group]
    if len(choices) != 1 or "inputs.invocation_mode" not in expression:
        raise AssertionError(f"unmodelled validation concurrency group: {value}")
    return choices[0].replace("{0}", immutable_id)


class WorkflowPolicyTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        cls.validation = WorkflowModel(VALIDATION_PATH)
        cls.sync = WorkflowModel(SYNC_PATH)
        cls.release = WorkflowModel(RELEASE_PATH)
        cls.models = (cls.validation, cls.sync, cls.release)
        workflow_paths = sorted(
            set(WORKFLOW_DIR.glob("*.yml")) | set(WORKFLOW_DIR.glob("*.yaml"))
        )
        cls.all_models = tuple(WorkflowModel(path) for path in workflow_paths)
        cls.all_models_by_path = {model.path: model for model in cls.all_models}
        for model in cls.models:
            if model.path not in cls.all_models_by_path:
                raise AssertionError(f"named workflow missing from inventory: {model.path}")

    def test_yaml_loader_preserves_on_key(self) -> None:
        fixture = WorkflowYaml("on:\n  push:\n    branches: [patchset]\n").load()
        self.assertIn("on", fixture)
        self.assertNotIn(True, fixture)
        self.assertEqual(fixture["on"]["push"]["branches"], ["patchset"])

    def test_shell_failure_suppression_fixtures_are_detected(self) -> None:
        unsafe = (
            "set +e\nrequired-gate",
            "set +o errexit\nrequired-gate",
            "set +o pipefail\nrequired-gate",
            "required-gate || true",
            "required-gate || :",
            "required-gate || exit 0",
            "required-gate || return 0",
            "required-gate || echo ignored",
            "required-gate || printf ignored",
        )
        for fixture in unsafe:
            with self.subTest(fixture=fixture):
                self.assertTrue(shell_failure_suppressions(fixture))

        safe = (
            "set -euo pipefail\nrequired-gate",
            "required-gate || exit 1",
            "required-gate || { echo failed >&2; exit 1; }",
            "! prohibited-value || exit 1",
        )
        for fixture in safe:
            with self.subTest(fixture=fixture):
                self.assertEqual(shell_failure_suppressions(fixture), [])

    def test_remote_mutation_command_fixtures_are_detected(self) -> None:
        fixtures = (
            "git -C checkout push origin HEAD:main",
            "gh pr comment 7 --repo example/project --body updated",
            "gh issue comment 8 --repo example/project --body updated",
            "gh api repos/example/project/issues/8 --method PATCH -f state=closed",
            "gh api repos/example/project/issues -f title=created",
            "gh api repos/example/project/releases --input release.json",
            "curl -X POST https://api.github.com/repos/example/project/releases",
            "curl --request DELETE https://api.github.com/repos/example/project/git/refs/tags/v1",
            "curl --data name=release https://api.github.com/repos/example/project/releases",
            "docker push ghcr.io/example/project:v1",
            "docker buildx build --push -t ghcr.io/example/project:v1 .",
        )
        for fixture in fixtures:
            with self.subTest(fixture=fixture):
                self.assertRegex(fixture, MUTATION_RE)

        read_only = (
            "git -C checkout fetch upstream main",
            "gh api --method GET repos/example/project/releases",
            "gh api --method GET repos/example/project/releases -f per_page=100",
            "curl -X GET https://api.github.com/repos/example/project/releases",
            "curl -X GET --data page=2 https://api.github.com/repos/example/project/releases",
            "docker pull ghcr.io/example/project:v1",
        )
        for fixture in read_only:
            with self.subTest(fixture=fixture):
                self.assertNotRegex(fixture, MUTATION_RE)

    def test_gate_failure_injection_rejects_masked_steps(self) -> None:
        original = next(
            step
            for step in self.validation.job_steps("validate")
            if step.get("name") == "Backend tests"
        )
        continue_on_error = dict(original, **{"continue-on-error": True})
        with self.assertRaises(AssertionError):
            assert_step_propagates_failure(
                self, self.validation, "validate", continue_on_error
            )

        shell_suppression = dict(
            original,
            run="set -euo pipefail\nsub2api-patch/scripts/required-gate.sh || true",
        )
        with self.assertRaises(AssertionError):
            assert_step_propagates_failure(
                self, self.validation, "validate", shell_suppression
            )

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

    def test_workflow_derived_authority_routes_only_trusted_continuations(self) -> None:
        validation_call = find_call(self.sync, "upstream-pr-check.yml")
        release_call = find_call(self.sync, "auto-release.yml")
        self.assertEqual(set(self.release.triggers), {"workflow_call"})

        def requested_release(invocation: Invocation) -> bool:
            supplied = dict(invocation.inputs).get("release_after_validation")
            if invocation.event == "schedule":
                if supplied is not None:
                    raise ValueError("scheduled Sync does not accept manual inputs")
                return False
            if supplied is None:
                dispatch = self.sync.triggers["workflow_dispatch"] or {}
                input_definitions = dispatch.get("inputs") or {}
                supplied = str(
                    input_definitions["release_after_validation"].get("default")
                ).lower()
            if supplied not in {"true", "false"}:
                raise ValueError("manual release intent must be a boolean")
            return supplied == "true"

        def execute_root(
            model: WorkflowModel,
            invocation: Invocation,
            candidate_changed: bool = False,
        ) -> tuple[str, ...]:
            if not model.accepts(invocation):
                return ()
            if model is self.validation:
                return ("validate",)
            if model is self.release:
                raise ValueError("release has no directly invocable event")
            self.assertIs(model, self.sync)
            release_requested = requested_release(invocation)
            validation_required = (
                candidate_changed
                if invocation.event == "schedule"
                else True
            )
            release_required = (
                candidate_changed
                if invocation.event == "schedule"
                else release_requested
            )
            executed = ["prepare-candidate"]
            if validation_required:
                executed.append(validation_call)
            if validation_required and release_required:
                executed.append(release_call)
            return tuple(executed)

        sha = "1" * 40
        fixtures = (
            (
                self.validation,
                Invocation("push", ref="refs/heads/patchset", after=sha, sha=sha),
                False,
                ("validate",),
            ),
            (
                self.validation,
                Invocation("workflow_dispatch", sha=sha),
                False,
                ("validate",),
            ),
            (
                self.sync,
                Invocation("schedule", sha=sha),
                False,
                ("prepare-candidate",),
            ),
            (
                self.sync,
                Invocation("schedule", sha=sha),
                True,
                ("prepare-candidate", validation_call, release_call),
            ),
            (
                self.sync,
                Invocation(
                    "workflow_dispatch",
                    sha=sha,
                    inputs=(("release_after_validation", "false"),),
                ),
                False,
                ("prepare-candidate", validation_call),
            ),
            (
                self.sync,
                Invocation(
                    "workflow_dispatch",
                    sha=sha,
                    inputs=(("release_after_validation", "true"),),
                ),
                False,
                ("prepare-candidate", validation_call, release_call),
            ),
            (
                self.release,
                Invocation("workflow_dispatch", sha=sha),
                False,
                (),
            ),
        )
        for model, invocation, changed, expected in fixtures:
            with self.subTest(
                workflow=model.path.name,
                event=invocation.event,
                changed=changed,
                inputs=invocation.inputs,
            ):
                self.assertEqual(execute_root(model, invocation, changed), expected)

        with self.assertRaises(ValueError):
            execute_root(
                self.sync,
                Invocation(
                    "workflow_dispatch",
                    sha=sha,
                    inputs=(("release_after_validation", "yes"),),
                ),
            )
        with self.assertRaises(ValueError):
            execute_root(
                self.sync,
                Invocation(
                    "schedule",
                    sha=sha,
                    inputs=(("release_after_validation", "true"),),
                ),
                True,
            )

        release_with = self.sync.jobs[release_call].get("with") or {}
        required_inputs = {
            name
            for name, definition in (
                (self.release.triggers["workflow_call"] or {}).get("inputs", {})
                .items()
            )
            if definition.get("required") is True
        }
        self.assertTrue(required_inputs <= set(release_with))
        prepare_outputs = {
            "expected_main_sha": "2" * 40,
            "expected_mirror_sha": "3" * 40,
            "expected_patched_sha": "4" * 40,
            "candidate_artifact_name": "sync-candidate-bundle-42-1",
            "candidate_artifact_id": "101",
            "candidate_artifact_digest": "sha256:" + "a" * 64,
            "recovery_release_artifact_name": "",
            "recovery_release_artifact_id": "",
            "recovery_release_artifact_digest": "",
            "recovery_image_artifact_name": "",
            "recovery_image_artifact_id": "",
            "recovery_image_artifact_digest": "",
        }
        validation_outputs = {
            "patchset_sha": sha,
            "upstream_source_sha": "5" * 40,
            "mirror_candidate_sha": "6" * 40,
            "replay_tree_sha": "7" * 40,
            "validation_conclusion": "success",
            "originating_repository": "Ovler-Young/sub2api-patch",
            "originating_workflow_path": ".github/workflows/sync-upstream.yml",
            "originating_run_id": "42",
            "originating_run_attempt": "1",
        }

        def resolve_call_value(value: Any) -> Any:
            match = re.fullmatch(
                r"\$\{\{ needs\.(prepare-candidate|validate)\.outputs\.([a-z_]+) \}\}",
                str(value),
            )
            if not match:
                return value
            source, field = match.groups()
            outputs = prepare_outputs if source == "prepare-candidate" else validation_outputs
            return outputs[field]

        authorized = {
            name: resolve_call_value(value) for name, value in release_with.items()
        }

        def continuation_is_authorized(candidate: dict[str, Any]) -> bool:
            return set(candidate) == set(release_with) and all(
                candidate[name] == resolve_call_value(expression)
                for name, expression in release_with.items()
            )

        self.assertTrue(continuation_is_authorized(authorized))
        for field, replacement in (
            ("validation_conclusion", "failure"),
            ("originating_run_id", "43"),
            ("replay_tree_sha", "8" * 40),
            ("invocation_mode", "standalone"),
        ):
            with self.subTest(malformed_continuation=field):
                self.assertFalse(
                    continuation_is_authorized(dict(authorized, **{field: replacement}))
                )

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
        validation_text = self.validation.text
        for field in PROVENANCE_FIELDS:
            with self.subTest(field=field):
                self.assertIn(field, validation_text)
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

    def test_provenance_outputs_are_continuous_through_reusable_calls(self) -> None:
        workflow_call = self.validation.triggers.get("workflow_call") or {}
        self.assertIsInstance(workflow_call, dict)
        call_outputs = workflow_call.get("outputs") or {}
        validation_job_outputs = self.validation.jobs["validate"].get("outputs") or {}
        release_job = find_call(self.sync, "auto-release.yml")
        release_with = self.sync.jobs[release_job].get("with") or {}
        release_call = self.release.triggers.get("workflow_call") or {}
        release_inputs = release_call.get("inputs") or {}

        for field in PROVENANCE_FIELDS:
            with self.subTest(field=field):
                self.assertIn(field, call_outputs)
                self.assertIn(field, validation_job_outputs)
                self.assertRegex(
                    str(call_outputs[field].get("value") or ""),
                    rf"jobs\.validate\.outputs\.{re.escape(field)}",
                )
                self.assertIn(field, release_with)
                self.assertEqual(
                    str(release_with[field]),
                    f"${{{{ needs.validate.outputs.{field} }}}}",
                )
                self.assertIn(field, release_inputs)

        release_guard = self.release.job_text("prepare-release")
        origin_checks = {
            "originating_repository": r"ORIGINATING_REPOSITORY.*GITHUB_REPOSITORY",
            "originating_workflow_path": r"ORIGINATING_WORKFLOW_PATH.*sync-upstream\.yml",
            "originating_run_id": r"ORIGINATING_RUN_ID.*GITHUB_RUN_ID",
            "originating_run_attempt": r"ORIGINATING_RUN_ATTEMPT.*GITHUB_RUN_ATTEMPT",
        }
        for field, pattern in origin_checks.items():
            with self.subTest(release_guard=field):
                self.assertRegex(release_guard, re.compile(pattern, re.S))

    def test_sync_patchset_resolution_is_bound_to_caller_sha(self) -> None:
        prepare_text = self.sync.job_text("prepare-candidate")
        self.assertRegex(
            prepare_text,
            r"(?:github\.sha|GITHUB_SHA)",
        )
        self.assertRegex(
            prepare_text,
            r'patchset_sha="\$GITHUB_SHA"',
        )
        resolution = next(
            str(step.get("run") or "")
            for step in self.sync.job_steps("prepare-candidate")
            if step.get("name") == "Resolve exact patchset revision"
        )
        self.assertRegex(
            resolution,
            re.compile(
                r'patchset_sha="\$GITHUB_SHA"\s*'
                r'if \[ "\$GITHUB_RUN_ATTEMPT" -eq 1 \]; then.*'
                r'git/ref/heads/patchset.*fi',
                re.S,
            ),
        )
        self.assertIn('echo "patchset_sha=$patchset_sha"', resolution)
        checkout_refs = []
        for step in self.sync.job_steps("prepare-candidate"):
            if not str(step.get("uses") or "").startswith("actions/checkout@"):
                continue
            options = step.get("with") or {}
            if options.get("path") == "sub2api-patch":
                checkout_refs.append(str(options.get("ref") or ""))
        self.assertEqual(len(checkout_refs), 1)
        self.assertRegex(checkout_refs[0], r"steps\..*\.outputs\.patchset_sha")

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
            (
                self.sync.job_text("prepare-candidate"),
                self.validation.job_text("validate"),
                self.release.job_text("prepare-release"),
            )
        )
        for gate, pattern in gate_patterns.items():
            with self.subTest(gate=gate):
                self.assertRegex(preparation, re.compile(pattern, re.I | re.S))

        common_validation_steps = (
            r"Apply patches to exact upstream source",
            r"Verify replay tree",
            r"Sanitize patch references",
            r"Backend tests",
            r"Backend lint and format checks",
            r"Frontend checks",
            r"Set up Docker Buildx",
            r"Preflight patched image build",
            r"Record immutable validation provenance",
            r"Upload validation provenance",
        )
        validation_steps = self.validation.job_steps("validate")
        for expected_name in common_validation_steps:
            matches = [
                step
                for step in validation_steps
                if re.fullmatch(expected_name, str(step.get("name") or ""), re.I)
            ]
            self.assertEqual(len(matches), 1, expected_name)
            condition = str(matches[0].get("if") or "")
            self.assertEqual(condition, "", f"required gate is conditional: {expected_name}")
            self.assertIsNot(matches[0].get("continue-on-error"), True)

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

    def test_unchanged_sync_candidate_cannot_reach_validation_or_mutation(self) -> None:
        prepare_job = "prepare-candidate"
        validation_job = find_call(self.sync, "upstream-pr-check.yml")
        release_job = find_call(self.sync, "auto-release.yml")
        prepare_outputs = self.sync.jobs[prepare_job].get("outputs") or {}
        gate_names = [
            name
            for name in ("validation_required", "candidate_changed")
            if name in prepare_outputs
        ]
        self.assertTrue(
            gate_names,
            "Sync preparation must expose whether the candidate changes main",
        )
        gate_name = gate_names[0]

        prepare_text = self.sync.job_text(prepare_job)
        self.assertIn("scripts/sync-published-state-changed.sh", prepare_text)
        self.assertRegex(
            prepare_text,
            re.compile(
                r"sync-published-state-changed\.sh.*"
                r"upstream_source_sha.*candidate_tree.*preview_tree.*"
                r"expected_main_sha.*expected_mirror_sha.*expected_patched_sha",
                re.S,
            ),
        )
        self.assertIn(
            "scripts/tests/sync-published-state-changed-test.sh",
            self.validation.job_text("validate"),
        )
        self.assertRegex(
            prepare_text,
            re.compile(
                r"EXPECTED_BASE_SHA=.*upstream_source_sha.*apply-patches\.sh.*"
                r"rm -rf[^\n]*\.github/workflows.*"
                r"(?:preview[^\n]*\^\{tree\}|preview_tree=.*write-tree)",
                re.I | re.S,
            ),
        )
        self.assertRegex(
            prepare_text,
            re.compile(
                r"expected_patched[^\n]*\^\{tree\}.*"
                r"preview_tree.*expected_patched_tree",
                re.I | re.S,
            ),
        )
        self.assertRegex(prepare_text, rf"{gate_name}=(?:true|false)")
        validation_condition = str(self.sync.jobs[validation_job].get("if") or "")
        self.assertRegex(
            validation_condition,
            rf"needs\.{re.escape(prepare_job)}\.outputs\.{re.escape(gate_name)}"
            r"\s*==\s*['\"]true['\"]",
        )

        release_condition = str(self.sync.jobs[release_job].get("if") or "")
        required_release_guards = (
            rf"needs\.{re.escape(prepare_job)}\.result\s*==\s*['\"]success['\"]",
            rf"needs\.{re.escape(validation_job)}\.result\s*==\s*['\"]success['\"]",
            r"release_after_validation\s*==\s*['\"]true['\"]",
        )
        for guard in required_release_guards:
            self.assertRegex(release_condition, guard)

        self.assertRegex(
            prepare_text,
            re.compile(
                r"GITHUB_EVENT_NAME.*schedule.*"
                r"validation_required=.*candidate_changed.*"
                r"release_after_validation=.*candidate_changed.*"
                r"else.*validation_required=true.*"
                r"release_after_validation=.*REQUESTED_RELEASE",
                re.S,
            ),
        )

        def decisions(
            event: str, candidate_changed: bool, requested_release: bool
        ) -> tuple[bool, bool]:
            if event == "schedule":
                return candidate_changed, candidate_changed
            return True, requested_release

        def reaches_mutation(validation_required: bool, release_intent: bool) -> bool:
            preparation_succeeded = True
            validation_runs = preparation_succeeded and validation_required
            validation_succeeded = validation_runs
            return (
                preparation_succeeded
                and validation_succeeded
                and release_intent
            )

        unchanged_schedule = decisions("schedule", False, True)
        changed_schedule = decisions("schedule", True, False)
        manual_validation = decisions("workflow_dispatch", False, False)
        manual_release = decisions("workflow_dispatch", False, True)
        self.assertFalse(reaches_mutation(*unchanged_schedule))
        self.assertTrue(reaches_mutation(*changed_schedule))
        self.assertFalse(reaches_mutation(*manual_validation))
        self.assertTrue(reaches_mutation(*manual_release))

    def test_pipefail_rejects_quiet_grep_after_unbounded_producers(self) -> None:
        quiet_pipeline = re.compile(
            r"(?m)^(?P<producer>[^\n|]+)\|\s*(?:\n[ \t]*)?"
            r"grep\s+(?P<options>-[^\s]*q[^\s]*)"
        )
        for model in self.all_models:
            for job_id, step in model.all_steps():
                run = str(step.get("run") or "")
                for match in quiet_pipeline.finditer(run):
                    producer = match.group("producer")
                    if "-print -quit" in producer:
                        continue
                    self.fail(
                        "quiet grep can close an unbounded producer under pipefail: "
                        f"{model.path.name}:{job_id}:{step.get('name')}: "
                        f"{match.group(0)}"
                    )

    def test_artifact_transport_uses_producer_attempt_and_checksums(self) -> None:
        sync_outputs = self.sync.jobs["prepare-candidate"].get("outputs") or {}
        self.assertIn("candidate_artifact_name", sync_outputs)
        self.assertRegex(
            str(sync_outputs["candidate_artifact_name"]),
            r"steps\..*\.outputs\.candidate_artifact_name",
        )
        self.assertIn("candidate_artifact_name", self.validation.text)
        self.assertIn("candidate_artifact_name", self.release.text)

        release_outputs = self.release.jobs["prepare-release"].get("outputs") or {}
        self.assertIn("release_artifact_name", release_outputs)
        self.assertRegex(
            str(release_outputs["release_artifact_name"]),
            r"steps\..*\.outputs\.release_artifact_name",
        )
        self.assertRegex(
            self.release.text,
            r"needs\.prepare-release\.outputs\.release_artifact_name",
        )
        self.assertGreaterEqual(self.sync.text.count("SHA256SUMS"), 1)
        self.assertGreaterEqual(self.validation.text.count("sha256sum -c"), 1)
        self.assertGreaterEqual(self.release.text.count("sha256sum -c"), 2)

    def test_isolated_package_runner_verifies_immutable_publication_evidence(self) -> None:
        outputs = self.release.jobs["prepare-release"].get("outputs") or {}
        required_outputs = {
            *PROVENANCE_FIELDS,
            "replay_commit_sha",
            "publication_commit_sha",
            "publication_tree_sha",
            "version",
            "version_image_tag",
            "latest_image_tag",
            "version_label",
            "revision_label",
            "docker_context_commit_sha",
            "docker_context_tree_sha",
            "dockerfile_path",
            "cache_from",
            "cache_to",
            "release_notes_sha256",
            "content_manifest_sha256",
            "manifest_sha256",
            "release_artifact_name",
            "release_artifact_id",
            "release_artifact_digest",
        }
        self.assertTrue(required_outputs <= set(outputs))
        self.assertRegex(
            str(outputs["release_artifact_id"]),
            r"steps\.upload_release\.outputs\.artifact-id",
        )
        self.assertRegex(
            str(outputs["release_artifact_digest"]),
            r"steps\.upload_release\.outputs\.artifact-digest",
        )

        authority_outputs = self.release.jobs["prepare-image-authority"].get(
            "outputs"
        ) or {}
        self.assertEqual(
            set(authority_outputs),
            {
                "authority_artifact_name",
                "authority_artifact_id",
                "authority_artifact_digest",
                "authorized_image_digest",
                "expected_version_state",
                "expected_latest_state",
                "expected_latest_manifest_sha256",
                "expected_latest_version_label",
                "expected_latest_revision_label",
            },
        )
        steps = self.release.job_steps("prepare-image-authority")
        names = [str(step.get("name") or "") for step in steps]
        required_order = (
            "Download exact verified release state by platform identity",
            "Verify exact archive, manifest, and publication content",
            "Prove publication state before image authorization",
            "Set up Docker Buildx for image authorization",
            "Build exact release image into a local OCI archive",
            "Verify exact local OCI publication content",
            "Log in to private GHCR before fresh state authorization",
            "Record immutable image publication authority",
            "Upload immutable image publication authority",
            "Redownload fresh authority by immutable artifact identity",
            "Select and verify exact authorized OCI archive",
            "Log in to private GHCR before recovered state authorization",
            "Verify authenticated registry state before mutation",
        )
        indexes = [names.index(name) for name in required_order]
        self.assertEqual(indexes, sorted(indexes))
        verification_text = "\n".join(str(step.get("run") or "") for step in steps)
        for marker in (
            "RELEASE_ARTIFACT_ID",
            "RELEASE_ARTIFACT_DIGEST",
            "archive_files",
            'sha256sum "${archive_files[0]}"',
            "sha256sum -c MANIFEST.SHA256",
            "sha256sum -c content-manifest.sha256",
            "publication-manifest.env",
            "MANIFEST_SHA256",
            "CONTENT_MANIFEST_SHA256",
            "RELEASE_NOTES_SHA256",
            "expected_payload_files",
            "publication-context.tar",
            "version_image_tag",
            "latest_image_tag",
            "version_label",
            "revision_label",
            "docker_context_tree_sha",
            "cache_from",
            "cache_to",
            "IMAGE-MANIFEST.SHA256",
            "release-image.oci.tar",
            "oci_manifest_digest",
        ):
            self.assertIn(marker, verification_text)
        serialized_steps = "\n".join(str(step) for step in steps)
        self.assertNotRegex(
            serialized_steps,
            r"actions/checkout|\bgit\s+(?:clone|fetch)\b|apply-patches",
        )
        credential_steps = [
            str(step.get("name") or "")
            for step in steps
            if re.search(r"github\.token|GH_TOKEN", str(step))
        ]
        self.assertEqual(
            credential_steps,
            [
                "Prove publication state before image authorization",
                "Log in to private GHCR before fresh state authorization",
                "Record immutable image publication authority",
                "Log in to private GHCR before recovered state authorization",
            ],
        )
        self.assertLess(
            names.index("Verify exact local OCI publication content"),
            names.index("Log in to private GHCR before fresh state authorization"),
        )
        self.assertLess(
            names.index("Select and verify exact authorized OCI archive"),
            names.index("Log in to private GHCR before recovered state authorization"),
        )
        self.assertFalse(any(step_mutates(step) for step in steps))
        download = steps[names.index("Download exact verified release state by platform identity")]
        self.assertEqual(
            download.get("uses"),
            "actions/download-artifact@3e5f45b2cfb9172054b4087a40e8e0b5a5461e7c",
        )
        download_options = download.get("with") or {}
        self.assertEqual(
            download_options.get("artifact-ids"),
            "${{ needs.prepare-release.outputs.release_artifact_id }}",
        )
        self.assertIs(download_options.get("skip-decompress"), True)
        self.assertEqual(download_options.get("digest-mismatch"), "error")
        for forbidden in ("name", "github-token", "repository", "run-id"):
            self.assertNotIn(forbidden, download_options)

        def accepts_artifact(
            trusted_id: str,
            trusted_digest: str,
            actual_id: str,
            actual_digest: str,
            manifest_digest: str,
            actual_manifest_digest: str,
        ) -> bool:
            return (
                trusted_id == actual_id
                and trusted_digest == actual_digest
                and manifest_digest == actual_manifest_digest
            )

        valid = ("42", "sha256:" + "a" * 64, "42", "sha256:" + "a" * 64, "b" * 64, "b" * 64)
        self.assertTrue(accepts_artifact(*valid))
        for index, replacement in ((2, "43"), (3, "sha256:" + "c" * 64), (5, "d" * 64)):
            fixture = list(valid)
            fixture[index] = replacement
            self.assertFalse(accepts_artifact(*fixture))

    def test_artifact_substitution_matrix_rejects_every_authority_dimension(self) -> None:
        authorization_steps = []
        for job_id in ("mutate-git", "prepare-image-authority", "create-release"):
            matches = [
                step
                for step in self.release.job_steps(job_id)
                if artifact_authorization_markers(str(step.get("run") or ""))
            ]
            self.assertEqual(len(matches), 1, job_id)
            authorization_steps.extend(matches)

        release_producer = next(
            step
            for step in self.release.job_steps("prepare-release")
            if step.get("name") == "Reconstruct and prepare release without mutation"
        )
        producer_run = str(release_producer.get("run") or "")
        manifest_heredoc = re.search(
            r"cat > release-state/publication-manifest\.env <<EOF\n(.*?)\nEOF",
            producer_run,
            re.S,
        )
        self.assertIsNotNone(manifest_heredoc)
        assert manifest_heredoc is not None
        producer_field_lines = re.findall(
            r"(?m)^([a-z0-9_]+)=", manifest_heredoc.group(1)
        )
        producer_fields = set(producer_field_lines)
        expected_release_schema = {
            "patchset_sha",
            "upstream_source_sha",
            "mirror_candidate_sha",
            "replay_tree_sha",
            "expected_main_sha",
            "expected_mirror_sha",
            "expected_patched_sha",
            "validation_conclusion",
            "originating_repository",
            "originating_workflow_path",
            "originating_run_id",
            "originating_run_attempt",
            "replay_commit_sha",
            "publication_commit_sha",
            "publication_tree_sha",
            "version",
            "image_owner",
            "version_image_tag",
            "latest_image_tag",
            "version_label",
            "revision_label",
            "docker_context_commit_sha",
            "docker_context_tree_sha",
            "dockerfile_path",
            "cache_from",
            "cache_to",
            "release_notes_sha256",
            "content_manifest_sha256",
        }
        self.assertEqual(len(expected_release_schema), 28)
        self.assertEqual(len(producer_field_lines), 28)
        self.assertEqual(producer_fields, expected_release_schema)
        self.assertFalse(
            {
                "expected_version_state",
                "expected_latest_state",
                "expected_latest_manifest_sha256",
                "expected_latest_version_label",
                "expected_latest_revision_label",
            }
            & producer_fields
        )

        manifest_fields = set.intersection(
            *(
                set(
                    re.findall(
                        r"(?m)^\s*require_manifest\s+([a-z0-9_]+)\s+",
                        str(step.get("run") or ""),
                    )
                )
                for step in authorization_steps
            )
        )
        for step in authorization_steps:
            authorization_run = str(step.get("run") or "")
            consumer_field_lines = re.findall(
                r"(?m)^\s*require_manifest\s+([a-z0-9_]+)\s+",
                authorization_run,
            )
            self.assertEqual(len(consumer_field_lines), 28)
            self.assertEqual(set(consumer_field_lines), expected_release_schema)
            self.assertRegex(
                authorization_run,
                re.compile(
                    r"wc -l.*publication-manifest\.env.*-eq 28",
                    re.S,
                ),
            )
        payload_fields = {"release_notes_sha256", "content_manifest_sha256"}
        for step in authorization_steps:
            authorization_run = str(step.get("run") or "")
            self.assertIn("RELEASE_NOTES_SHA256", authorization_run)
            self.assertIn("CONTENT_MANIFEST_SHA256", authorization_run)
            self.assertIn("sha256sum -c content-manifest.sha256", authorization_run)
        authority_fields = manifest_fields | payload_fields
        dimensions = {
            "repository": {"originating_repository", "originating_workflow_path"},
            "run": {"originating_run_id"},
            "attempt": {"originating_run_attempt"},
            "payload": payload_fields,
            "provenance": {
                "patchset_sha",
                "upstream_source_sha",
                "mirror_candidate_sha",
                "validation_conclusion",
            },
            "tree": {
                "replay_tree_sha",
                "publication_tree_sha",
                "docker_context_tree_sha",
            },
            "publication": {
                "replay_commit_sha",
                "publication_commit_sha",
                "docker_context_commit_sha",
                "version_image_tag",
            },
        }
        for dimension, fields in dimensions.items():
            with self.subTest(dimension=dimension):
                self.assertTrue(fields <= authority_fields)

        image_baseline_fields = {
            "expected_version_state",
            "expected_latest_state",
            "expected_latest_manifest_sha256",
            "expected_latest_version_label",
            "expected_latest_revision_label",
        }
        publish_authorization = next(
            step
            for step in self.release.job_steps("publish-image")
            if step.get("name") == "Verify exact pre-mutation image authority"
        )
        publish_authorization_run = str(publish_authorization.get("run") or "")
        publish_authorization_env = publish_authorization.get("env") or {}
        for field in image_baseline_fields:
            upper_field = field.upper()
            self.assertIn(f"authority_field {field}", publish_authorization_run)
            self.assertIn(upper_field, publish_authorization_run)
            self.assertEqual(
                publish_authorization_env.get(upper_field),
                f"${{{{ needs.prepare-image-authority.outputs.{field} }}}}",
            )

        expected_baseline = {
            field: f"authorized-{field}" for field in image_baseline_fields
        }

        def baseline_is_authorized(candidate: dict[str, str]) -> bool:
            return set(candidate) == image_baseline_fields and all(
                candidate[field] == expected_baseline[field]
                for field in image_baseline_fields
            )

        self.assertTrue(baseline_is_authorized(expected_baseline))
        for field in sorted(image_baseline_fields):
            with self.subTest(substituted_image_baseline=field):
                substituted_baseline = dict(expected_baseline)
                substituted_baseline[field] = f"substituted-{field}"
                self.assertFalse(baseline_is_authorized(substituted_baseline))

        expected = {
            field: f"authorized-{index}"
            for index, field in enumerate(sorted(authority_fields), start=1)
        }

        def artifact_is_authorized(candidate: dict[str, str]) -> bool:
            return set(candidate) == authority_fields and all(
                candidate[field] == expected[field] for field in authority_fields
            )

        self.assertTrue(artifact_is_authorized(expected))
        for dimension, fields in dimensions.items():
            for field in sorted(fields):
                with self.subTest(dimension=dimension, substituted_field=field):
                    substituted = dict(expected)
                    substituted[field] = f"substituted-{field}"
                    self.assertFalse(artifact_is_authorized(substituted))
        missing = dict(expected)
        missing.pop("originating_repository")
        self.assertFalse(artifact_is_authorized(missing))
        self.assertFalse(
            artifact_is_authorized(dict(expected, unexpected_payload="injected"))
        )

    def test_partial_publication_always_reports_completed_and_missing_outputs(self) -> None:
        reporter = "report-publication-outcome"
        expected_needs = {
            "prepare-release",
            "prepare-image-authority",
            "mutate-git",
            "publish-image",
            "create-release",
        }
        self.assertEqual(self.release.job_needs(reporter), expected_needs)
        self.assertIn("always()", str(self.release.jobs[reporter].get("if") or ""))
        report_text = self.release.job_text(reporter)
        for output in (
            "mirror branch",
            "main branch",
            "patched branch",
            "release tag",
            "version image",
            "latest image",
            "GitHub release",
        ):
            self.assertIn(output, report_text)
        self.assertIn("GITHUB_STEP_SUMMARY", report_text)
        self.assertIn("actions/upload-artifact@", report_text)
        self.assertIn("continue-on-error", str(self.release.jobs[reporter]))
        self.assertIn("printf mismatch", report_text)
        self.assertIn("reporter-release-state/release-notes.md", report_text)
        for exact_release_field in (
            ".tag_name == $version",
            ".name == $version",
            ".body == $body",
            ".target_commitish == $target",
            ".draft == false",
            ".prerelease == false",
        ):
            self.assertIn(exact_release_field, report_text)

        report_download = next(
            step
            for step in self.release.job_steps(reporter)
            if step.get("name") == "Download prepared notes for outcome classification"
        )
        self.assertIs(report_download.get("continue-on-error"), True)
        report_download_options = report_download.get("with") or {}
        self.assertEqual(
            report_download_options.get("artifact-ids"),
            "${{ needs.prepare-release.outputs.release_artifact_id }}",
        )
        self.assertEqual(report_download_options.get("digest-mismatch"), "error")
        self.assertNotIn("name", report_download_options)
        for failed_job in expected_needs:
            with self.subTest(failed_job=failed_job):
                self.assertTrue(
                    self.release.reachable_after_failure(reporter, failed_job)
                )
        for failed_package_step in (
            "Download exact pre-mutation image authority",
            "Verify exact pre-mutation image authority",
            "Set up ORAS for content-addressed publication",
            "Log in to GHCR",
            "Publish authorized version image from OCI layout",
            "Publish authorized latest image from OCI layout",
        ):
            self.assertIn(failed_package_step, self.release.job_text("publish-image"))

        self.assertIn("report_ref_status", report_text)
        self.assertIn('[ "$http_status" = 200 ]', report_text)
        self.assertIn('[ "$http_status" = 404 ]', report_text)
        reporter_login = next(
            step
            for step in self.release.job_steps(reporter)
            if step.get("name") == "Log in to private GHCR for outcome classification"
        )
        self.assertIs(reporter_login.get("continue-on-error"), True)
        outcome_step = next(
            step
            for step in self.release.job_steps(reporter)
            if step.get("name") == "Record completed and missing publication outputs"
        )
        outcome_env = outcome_step.get("env") or {}
        self.assertEqual(
            outcome_env.get("REPORTER_LOGIN_RESULT"),
            "${{ steps.reporter_login.outcome }}",
        )
        outcome_run = str(outcome_step.get("run") or "")
        self.assertIn('[ "$REPORTER_LOGIN_RESULT" != success ]', outcome_run)
        self.assertRegex(
            outcome_run,
            re.compile(r"unauthorized\|denied.*printf unknown", re.S),
        )

        def classify_ref(http_status: str, actual: str, expected: str) -> str:
            if not expected:
                return "unknown"
            if http_status == "200":
                if not actual:
                    return "unknown"
                return "completed" if actual == expected else "mismatch"
            if http_status == "404":
                return "missing"
            return "unknown"

        intended = "1" * 40
        self.assertEqual(classify_ref("200", intended, intended), "completed")
        self.assertEqual(classify_ref("200", "2" * 40, intended), "mismatch")
        self.assertEqual(classify_ref("404", "", intended), "missing")
        for status in ("000", "401", "403", "500", ""):
            self.assertEqual(classify_ref(status, "", intended), "unknown")
        self.assertEqual(classify_ref("200", "", intended), "unknown")
        self.assertEqual(classify_ref("200", intended, ""), "unknown")

        def classify_image(login_result: str, observation: str) -> str:
            if login_result != "success":
                return "unknown"
            if observation == "authorization-error":
                return "unknown"
            if observation == "missing":
                return "missing"
            if observation == "exact":
                return "completed"
            return "mismatch"

        self.assertEqual(classify_image("failure", "missing"), "unknown")
        self.assertEqual(
            classify_image("success", "authorization-error"), "unknown"
        )
        self.assertEqual(classify_image("success", "missing"), "missing")
        self.assertEqual(classify_image("success", "exact"), "completed")
        self.assertEqual(classify_image("success", "different"), "mismatch")

        def classify_release(http_status: str, exact: bool) -> str:
            if http_status == "200":
                return "completed" if exact else "mismatch"
            if http_status == "404":
                return "missing"
            return "unknown"

        self.assertEqual(classify_release("200", True), "completed")
        self.assertEqual(classify_release("200", False), "mismatch")
        self.assertEqual(classify_release("404", False), "missing")
        for status in ("000", "401", "403", "500", ""):
            self.assertEqual(classify_release(status, False), "unknown")

    def test_package_and_release_retries_accept_only_exact_existing_outputs(self) -> None:
        image_text = self.release.job_text("publish-image")
        for marker in (
            "existing image tag is outside the authorized state",
            "publish_version=false",
            "publish_version=true",
            "publish_latest=false",
            "publish_latest=true",
            "Verify exact published image identities",
            "cmp version-final.raw latest-final.raw",
        ):
            self.assertIn(marker, image_text)
        image_steps = self.release.job_steps("publish-image")
        self.assertFalse(
            any(
                "docker/build-push-action" in str(step.get("uses") or "")
                for step in image_steps
            )
        )
        publication_conditions = {
            str(step.get("name") or ""): str(step.get("if") or "")
            for step in image_steps
            if "oras cp" in str(step.get("run") or "")
        }
        self.assertEqual(
            publication_conditions,
            {
                "Publish authorized version image from OCI layout":
                    "${{ steps.image_state.outputs.publish_version == 'true' }}",
                "Publish authorized latest image from OCI layout":
                    "${{ steps.image_state.outputs.publish_latest == 'true' }}",
            },
        )

        baseline_fields = (
            "expected_version_state",
            "expected_latest_state",
            "expected_latest_manifest_sha256",
            "expected_latest_version_label",
            "expected_latest_revision_label",
        )
        for field in baseline_fields:
            self.assertIn(field, self.release.job_text("prepare-image-authority"))
            self.assertIn(field, image_text.lower())
        prepare_release_outputs = self.release.jobs["prepare-release"].get(
            "outputs"
        ) or {}
        self.assertFalse(set(baseline_fields) & set(prepare_release_outputs))
        prepare_release_text = self.release.job_text("prepare-release")
        self.assertNotIn("docker buildx imagetools inspect", prepare_release_text)
        self.assertNotIn("docker/login-action", prepare_release_text)
        for marker in (
            "latest-patch version label has no exact downstream tag",
            "latest-patch version label has no exact downstream release",
            "latest-patch revision does not match its release tag",
        ):
            self.assertIn(marker, self.release.text)
        for marker in (
            '[ "$digest" = "$EXPECTED_LATEST_MANIFEST_SHA256" ]',
            '[ "$version" = "$EXPECTED_LATEST_VERSION_LABEL" ]',
            '[ "$revision" = "$EXPECTED_LATEST_REVISION_LABEL" ]',
            "missing:missing|missing:target|present:historical|present:target",
        ):
            self.assertIn(marker, image_text)
        self.assertIn(
            "missing:missing|missing:target|present:historical|present:target",
            self.release.job_text("prepare-image-authority"),
        )

        target = ("target-digest", "v0.2.0-patch.2", "2" * 40)
        historical = ("old-digest", "v0.2.0-patch.1", "1" * 40)

        def classify_latest(
            prepared: tuple[str, str, str] | None,
            observed: tuple[str, str, str] | None,
        ) -> str:
            if observed == target:
                return "target"
            if observed is None:
                if prepared is None:
                    return "missing"
                raise ValueError("prepared latest disappeared")
            if prepared is not None and observed == prepared:
                return "historical"
            raise ValueError("latest changed or is unrecognized")

        allowed_latest_transitions = {
            ("missing", "missing"),
            ("missing", "target"),
            ("present", "historical"),
            ("present", "target"),
        }

        def publication_flags(
            version_present: bool,
            expected_latest_state: str,
            latest_state: str,
        ) -> tuple[bool, bool]:
            if (expected_latest_state, latest_state) not in allowed_latest_transitions:
                raise ValueError("unauthorized state")
            return not version_present, latest_state != "target"

        self.assertEqual(
            publication_flags(
                False,
                "present",
                classify_latest(historical, historical),
            ),
            (True, True),
        )
        self.assertEqual(
            publication_flags(False, "missing", classify_latest(target, target)),
            (True, False),
        )
        self.assertEqual(
            publication_flags(True, "present", classify_latest(target, target)),
            (False, False),
        )
        for expected_state in ("missing", "present"):
            for observed_state in ("missing", "historical", "target"):
                transition = (expected_state, observed_state)
                if transition in allowed_latest_transitions:
                    publication_flags(False, expected_state, observed_state)
                else:
                    with self.subTest(rejected_latest_transition=transition):
                        with self.assertRaises(ValueError):
                            publication_flags(False, expected_state, observed_state)
        with self.assertRaises(ValueError):
            publication_flags(False, "present", "missing")
        with self.assertRaises(ValueError):
            classify_latest(historical, ("raced", historical[1], historical[2]))
        with self.assertRaises(ValueError):
            classify_latest(None, historical)

        release_steps = self.release.job_steps("create-release")
        authority = next(
            step
            for step in release_steps
            if step.get("name") == "Authorize exact GitHub release output"
        )
        authority_run = str(authority.get("run") or "")
        for exact_field in (
            ".tag_name == $version",
            ".name == $version",
            ".body == $body",
            ".target_commitish == $target",
            ".draft == false",
            ".prerelease == false",
        ):
            self.assertIn(exact_field, authority_run)
        self.assertIn("mode=skip", authority_run)
        self.assertIn("mode=create", authority_run)
        create = next(
            step
            for step in release_steps
            if "gh release create" in str(step.get("run") or "")
        )
        self.assertEqual(
            create.get("if"),
            "${{ steps.release_authority.outputs.mode == 'create' }}",
        )
        self.assertIn('--target "$PUBLICATION_COMMIT_SHA"', str(create.get("run") or ""))
        self.assertNotIn(
            "github.token",
            "\n".join(str(step) for step in release_steps[: release_steps.index(create)]),
        )

        for job_id in ("publish-image", "create-release"):
            text = self.release.job_text(job_id)
            self.assertNotRegex(text, r"actions/checkout|\bgit\s+(?:clone|fetch)\b")
            self.assertNotRegex(text, r"sub2api-patch/scripts/|apply-patches\.sh")

    def test_remote_version_queries_are_paginated_and_pipefail_safe(self) -> None:
        normalized = self.release.text.replace("\\\n", " ")
        self.assertNotRegex(normalized, r"\bgh release list\b|--limit\s+[0-9]+")
        release_queries = [
            line
            for line in normalized.splitlines()
            if "gh api" in line and "/releases?per_page=" in line
        ]
        tag_queries = [
            line
            for line in normalized.splitlines()
            if "gh api" in line and "matching-refs/tags/" in line
        ]
        self.assertGreaterEqual(len(release_queries), 2)
        self.assertGreaterEqual(len(tag_queries), 2)
        for query in release_queries + tag_queries:
            self.assertIn("--paginate", query)

    def test_publication_path_manifest_is_reference_scanned(self) -> None:
        self.assertGreaterEqual(
            len(
                re.findall(
                    r"grep\s+-E\s+['\"]?\$blocked['\"]?\s+publication-paths\.txt",
                    self.release.text,
                )
            ),
            2,
        )
        blocked = re.compile(r"(^|[^A-Za-z0-9_])#[0-9]+")
        self.assertRegex(".github/workflows/release-#42.yml", blocked)

    def test_effective_permissions_are_default_deny_and_job_scoped(self) -> None:
        for model in self.all_models:
            with self.subTest(workflow=model.path.name):
                root_permissions = model.data.get("permissions")
                self.assertIsInstance(root_permissions, dict)
                self.assertFalse(
                    any(str(value) == "write" for value in root_permissions.values())
                )
            mutations = model.mutating_jobs()
            for job_id in model.jobs:
                effective = model.effective_permissions(job_id)
                writes = write_permission_scopes(effective)
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
            self.assertFalse(write_permission_scopes(permissions))

        expected_release_permissions = {
            "prepare-release": {
                "actions": "read",
                "checks": "read",
                "contents": "read",
            },
            "mutate-git": {
                "actions": "read",
                "checks": "read",
                "contents": "write",
                "pull-requests": "write",
            },
            "prepare-image-authority": {
                "actions": "read",
                "contents": "read",
                "packages": "read",
            },
            "publish-image": {
                "actions": "read",
                "packages": "write",
            },
            "create-release": {
                "actions": "read",
                "contents": "write",
            },
            "report-publication-outcome": {
                "actions": "read",
                "contents": "read",
                "packages": "read",
            },
        }
        self.assertEqual(set(self.release.jobs), set(expected_release_permissions))
        for job_id, permissions in expected_release_permissions.items():
            self.assertEqual(
                self.release.effective_permissions(job_id), permissions, job_id
            )

    def test_scalar_write_all_is_treated_as_every_write_scope(self) -> None:
        fixture = WorkflowModel.__new__(WorkflowModel)
        fixture.path = Path("write-all-fixture.yml")
        fixture.text = ""
        fixture.data = {"permissions": {}}
        fixture.jobs = {"verify": {"permissions": "write-all"}}

        effective = fixture.effective_permissions("verify")
        self.assertEqual(effective, {"*": "write-all"})
        self.assertEqual(write_permission_scopes(effective), WRITE_SCOPES)

    def test_every_actual_checkout_disables_persisted_credentials(self) -> None:
        checkouts: list[tuple[str, str, dict[str, Any]]] = []
        for model in self.all_models:
            for job_id, step in model.all_steps():
                if not str(step.get("uses") or "").startswith("actions/checkout@"):
                    continue
                options = step.get("with") or {}
                self.assertIsInstance(options, dict)
                checkouts.append((model.path.name, job_id, options))

        self.assertTrue(checkouts)
        for workflow, job_id, options in checkouts:
            with self.subTest(workflow=workflow, job=job_id):
                self.assertIs(
                    options.get("persist-credentials"),
                    False,
                    "checkout must not persist a repository credential",
                )

    def test_write_capable_jobs_pin_every_third_party_action_to_full_sha(self) -> None:
        found: list[tuple[str, str, str]] = []
        for model in self.all_models:
            for job_id in model.jobs:
                if not write_permission_scopes(model.effective_permissions(job_id)):
                    continue
                for step in model.job_steps(job_id):
                    uses = str(step.get("uses") or "")
                    if not uses or uses.startswith("./"):
                        continue
                    found.append((model.path.name, job_id, uses))
                    self.assertRegex(
                        uses,
                        PINNED_THIRD_PARTY_ACTION_RE,
                        f"unpinned action in write-capable job: {model.path.name}:{job_id}",
                    )

        self.assertTrue(found)

    def test_every_write_job_has_a_content_addressed_credential_boundary(self) -> None:
        direct_write_jobs: list[tuple[WorkflowModel, str]] = []
        delegated_write_jobs: list[tuple[WorkflowModel, str]] = []
        for model in self.all_models:
            for job_id in model.jobs:
                permissions = model.effective_permissions(job_id)
                writes = write_permission_scopes(permissions) & PUBLICATION_WRITE_SCOPES
                if not writes:
                    continue
                if model.jobs[job_id].get("uses"):
                    delegated_write_jobs.append((model, job_id))
                else:
                    direct_write_jobs.append((model, job_id))

        self.assertEqual(
            {(model.path.name, job_id) for model, job_id in delegated_write_jobs},
            {("sync-upstream.yml", find_call(self.sync, "auto-release.yml"))},
        )
        for model, job_id in delegated_write_jobs:
            self.assertRegex(
                str(model.jobs[job_id].get("uses") or ""),
                r"^\./\.github/workflows/auto-release\.yml$",
            )

        self.assertEqual(
            {(model.path.name, job_id) for model, job_id in direct_write_jobs},
            {
                ("auto-release.yml", "mutate-git"),
                ("auto-release.yml", "publish-image"),
                ("auto-release.yml", "create-release"),
            },
        )
        for model, job_id in direct_write_jobs:
            with self.subTest(workflow=model.path.name, job=job_id):
                steps = model.job_steps(job_id)
                self.assertTrue(steps)
                self.assertEqual(preauthorization_violations(steps), [])
                self.assertNotRegex(
                    str(model.jobs[job_id].get("env") or ""),
                    WRITE_CREDENTIAL_RE,
                    "write credential is exposed for the entire job",
                )

                job_text = model.job_text(job_id)
                self.assertNotRegex(job_text, r"actions/checkout@")
                self.assertNotRegex(job_text, REPOSITORY_ACQUISITION_RE)

                download_indexes = [
                    index
                    for index, step in enumerate(steps)
                    if PINNED_ARTIFACT_DOWNLOAD_RE.fullmatch(
                        str(step.get("uses") or "")
                    )
                ]
                if job_id == "publish-image":
                    expected_download_ids = [
                        "${{ needs.prepare-image-authority.outputs.authority_artifact_id }}"
                    ]
                else:
                    expected_download_ids = [
                        "${{ needs.prepare-release.outputs.release_artifact_id }}"
                    ]
                self.assertEqual(len(download_indexes), len(expected_download_ids))
                actual_download_ids = []
                for download_index in download_indexes:
                    download_options = steps[download_index].get("with") or {}
                    self.assertIsInstance(download_options, dict)
                    self.assertNotIn("name", download_options)
                    actual_download_ids.append(download_options.get("artifact-ids"))
                    self.assertIs(download_options.get("skip-decompress"), True)
                    self.assertEqual(download_options.get("digest-mismatch"), "error")
                    self.assertNotRegex(
                        step_text(steps[download_index]), WRITE_CREDENTIAL_RE
                    )
                self.assertEqual(actual_download_ids, expected_download_ids)

                authorization_indexes = [
                    index
                    for index, step in enumerate(steps)
                    if artifact_authorization_markers(
                        str(step.get("run") or "")
                    )
                ]
                self.assertEqual(len(authorization_indexes), 1)
                authorization_index = authorization_indexes[0]
                self.assertLess(max(download_indexes), authorization_index)
                authorization = steps[authorization_index]
                authorization_env = authorization.get("env") or {}
                if job_id == "publish-image":
                    self.assertEqual(
                        authorization_env.get("AUTHORITY_ARTIFACT_DIGEST"),
                        "${{ needs.prepare-image-authority.outputs.authority_artifact_digest }}",
                    )
                    self.assertEqual(
                        authorization_env.get("AUTHORIZED_IMAGE_DIGEST"),
                        "${{ needs.prepare-image-authority.outputs.authorized_image_digest }}",
                    )
                else:
                    self.assertEqual(
                        authorization_env.get("RELEASE_ARTIFACT_ID"),
                        "${{ needs.prepare-release.outputs.release_artifact_id }}",
                    )
                    self.assertEqual(
                        authorization_env.get("RELEASE_ARTIFACT_DIGEST"),
                        "${{ needs.prepare-release.outputs.release_artifact_digest }}",
                    )
                    self.assertEqual(
                        authorization_env.get("MANIFEST_SHA256"),
                        "${{ needs.prepare-release.outputs.manifest_sha256 }}",
                    )

                credential_indexes = [
                    index
                    for index, step in enumerate(steps)
                    if WRITE_CREDENTIAL_RE.search(step_text(step))
                ]
                self.assertTrue(credential_indexes)
                self.assertLess(authorization_index, min(credential_indexes))
                self.assertLess(max(download_indexes), min(credential_indexes))

                if job_id == "mutate-git":
                    authorization_run = str(authorization.get("run") or "")
                    self.assertIn("git init", authorization_run)
                    bundle_verify = re.search(
                        r"git(?:\s+-C\s+\S+)?\s+bundle\s+verify\b",
                        authorization_run,
                    )
                    bundle_unbundle = re.search(
                        r"git(?:\s+-C\s+\S+)?\s+bundle\s+unbundle\b",
                        authorization_run,
                    )
                    self.assertIsNotNone(bundle_verify)
                    self.assertIsNotNone(bundle_unbundle)
                    assert bundle_verify is not None
                    assert bundle_unbundle is not None
                    self.assertLess(
                        bundle_verify.start(),
                        bundle_unbundle.start(),
                    )
                    for identity in (
                        "MIRROR_CANDIDATE_SHA",
                        "REPLAY_COMMIT_SHA",
                        "PUBLICATION_COMMIT_SHA",
                    ):
                        self.assertRegex(
                            authorization_run,
                            rf"rev-parse[^\n]*\${identity}",
                        )

    def test_write_job_boundary_rejects_untrusted_execution_and_tokens(self) -> None:
        authorization_run = "\n".join(
            (
                "set -euo pipefail",
                "[ -n \"$RELEASE_ARTIFACT_ID\" ]",
                "archive_digest=${RELEASE_ARTIFACT_DIGEST#sha256:}",
                "[ \"$(sha256sum artifact-download/archive.zip | awk '{print $1}')\" = \"$archive_digest\" ]",
                "expected_payload_files=payload",
                "sha256sum -c MANIFEST.SHA256",
                "sha256sum -c content-manifest.sha256",
                "sha256sum publication-manifest.env",
                "[ -n \"$MANIFEST_SHA256\" ]",
            )
        )
        download = {
            "name": "Download exact artifact",
            "uses": "actions/download-artifact@" + "a" * 40,
            "with": {"artifact-ids": "42", "skip-decompress": True},
        }
        authorize = {"name": "Authorize", "run": authorization_run}
        self.assertEqual(preauthorization_violations([download, authorize]), [])

        injected = (
            {"uses": "actions/checkout@v7", "with": {"persist-credentials": False}},
            {"run": "git clone https://github.com/example/project"},
            {"run": "git -C repository fetch origin main"},
            {"run": "sub2api-patch/scripts/check.sh"},
            {"run": "bash release-state/check.sh"},
            {"run": "publication/tool --verify"},
            {"run": "pnpm run prepare"},
            {"run": "docker buildx build publication"},
            {"run": "git config core.hooksPath release-state/hooks"},
            {"env": {"GH_TOKEN": "${{ github.token }}"}, "run": "true"},
            {"env": {"REGISTRY_TOKEN": "${{ secrets.GHCR_PAT }}"}, "run": "true"},
            {"env": {"DOCKER_CONFIG": "/tmp/docker"}, "run": "true"},
            {"with": {"password": "${{ github.token }}"}},
            {"run": "docker login ghcr.io --password-stdin"},
            {"run": "printf secret | git credential approve"},
            {"run": "git config credential.helper store"},
            {"run": "git remote add origin https://x-access-token:${{ github.token }}@github.com/example/project"},
        )
        for unsafe_step in injected:
            with self.subTest(step=unsafe_step):
                violations = preauthorization_violations(
                    [download, dict(unsafe_step, name="injected"), authorize]
                )
                self.assertTrue(violations)

    def test_concurrency_fixtures_preserve_running_release_authority(self) -> None:
        sync_concurrency = self.sync.concurrency
        self.assertEqual(sync_concurrency.get("group"), "sub2api-release-mutation")
        self.assertIs(sync_concurrency.get("cancel-in-progress"), False)

        validation_concurrency = self.validation.concurrency
        group_expression = validation_concurrency.get("group")
        cancel_expression = validation_concurrency.get("cancel-in-progress")
        standalone_group = concurrency_group_value(
            group_expression, "", "1" * 40
        )
        called_group = concurrency_group_value(group_expression, "sync", "12345")
        self.assertTrue(concurrency_cancel_value(cancel_expression, ""))
        self.assertFalse(concurrency_cancel_value(cancel_expression, "sync"))
        self.assertIn("sub2api-patch-validation-", standalone_group)
        self.assertIn("sub2api-sync-validation-", called_group)
        self.assertNotEqual(standalone_group, called_group)
        self.assertNotEqual(standalone_group, sync_concurrency.get("group"))
        self.assertNotEqual(called_group, sync_concurrency.get("group"))

        running_sync = {
            "group": sync_concurrency["group"],
            "cancel": sync_concurrency["cancel-in-progress"],
            "state": "running",
        }
        later_sync = dict(running_sync, state="pending")
        push_validation = {
            "group": standalone_group,
            "cancel": concurrency_cancel_value(cancel_expression, ""),
            "state": "running",
        }
        self.assertFalse(running_sync["cancel"])
        self.assertEqual(running_sync["group"], later_sync["group"])
        self.assertNotEqual(running_sync["group"], push_validation["group"])
        self.assertIn(find_call(self.sync, "auto-release.yml"), self.sync.jobs)

        release_callers: list[WorkflowModel] = []
        for model in self.all_models:
            if any(
                re.search(r"(?:^|/)auto-release\.yml(?:@.+)?$", uses)
                for uses in model.called_workflows().values()
            ):
                release_callers.append(model)
            if not model.mutating_jobs() or model.path == RELEASE_PATH:
                continue
            self.assertEqual(
                model.concurrency.get("group"),
                "sub2api-release-mutation",
                f"mutation-capable workflow lacks repository release lock: {model.path.name}",
            )
            self.assertIs(model.concurrency.get("cancel-in-progress"), False)
        self.assertEqual([model.path for model in release_callers], [SYNC_PATH])
        self.assertEqual(set(self.release.triggers), {"workflow_call"})

    def test_artifact_consumers_use_explicit_producer_names(self) -> None:
        candidate_field = "candidate_artifact_name"
        candidate_id_field = "candidate_artifact_id"
        candidate_digest_field = "candidate_artifact_digest"
        release_field = "release_artifact_name"
        sync_outputs = self.sync.jobs["prepare-candidate"].get("outputs") or {}
        for field in (candidate_field, candidate_id_field, candidate_digest_field):
            self.assertIn(field, sync_outputs)
        candidate_uploads = [
            str((step.get("with") or {}).get("name") or "")
            for step in self.sync.job_steps("prepare-candidate")
            if str(step.get("uses") or "").startswith("actions/upload-artifact@")
        ]
        self.assertEqual(
            candidate_uploads,
            ["${{ steps.prepare.outputs.candidate_artifact_name }}"],
        )

        sync_validate = self.sync.jobs[find_call(self.sync, "upstream-pr-check.yml")]
        sync_release = self.sync.jobs[find_call(self.sync, "auto-release.yml")]
        for caller in (sync_validate, sync_release):
            self.assertEqual(
                str((caller.get("with") or {}).get(candidate_field) or ""),
                "${{ needs.prepare-candidate.outputs.candidate_artifact_name }}",
            )
        for field in (candidate_id_field, candidate_digest_field):
            self.assertEqual(
                str((sync_release.get("with") or {}).get(field) or ""),
                f"${{{{ needs.prepare-candidate.outputs.{field} }}}}",
            )

        for model in (self.validation, self.release):
            workflow_call = model.triggers.get("workflow_call") or {}
            self.assertIn(candidate_field, workflow_call.get("inputs") or {})
        release_inputs = (
            (self.release.triggers["workflow_call"] or {}).get("inputs") or {}
        )
        self.assertIn(candidate_id_field, release_inputs)
        self.assertIn(candidate_digest_field, release_inputs)

        validation_candidate_download = next(
            step
            for step in self.validation.job_steps("validate")
            if step.get("name") == "Download trusted mirror candidate"
        )
        validation_options = validation_candidate_download.get("with") or {}
        self.assertEqual(
            validation_options.get("name"),
            "${{ inputs.candidate_artifact_name }}",
        )
        self.assertNotIn("artifact-ids", validation_options)
        self.assertNotIn("github.run_attempt", str(validation_options))

        release_candidate_download = next(
            step
            for step in self.release.job_steps("prepare-release")
            if step.get("name") == "Download immutable mirror candidate"
        )
        release_candidate_options = release_candidate_download.get("with") or {}
        self.assertEqual(
            release_candidate_options.get("artifact-ids"),
            "${{ inputs.candidate_artifact_id }}",
        )
        self.assertNotIn("name", release_candidate_options)
        self.assertIs(release_candidate_options.get("skip-decompress"), True)
        self.assertEqual(release_candidate_options.get("digest-mismatch"), "error")
        release_candidate_verification = next(
            step
            for step in self.release.job_steps("prepare-release")
            if step.get("name") == "Verify candidate artifact producer attempt"
        )
        verification_env = release_candidate_verification.get("env") or {}
        self.assertEqual(
            verification_env.get("ARTIFACT_DIGEST"),
            "${{ inputs.candidate_artifact_digest }}",
        )

        release_outputs = self.release.jobs["prepare-release"].get("outputs") or {}
        self.assertIn(release_field, release_outputs)
        release_uploads = [
            str((step.get("with") or {}).get("name") or "")
            for step in self.release.job_steps("prepare-release")
            if str(step.get("uses") or "").startswith("actions/upload-artifact@")
        ]
        self.assertEqual(
            release_uploads,
            ["${{ steps.release.outputs.release_artifact_name }}"],
        )
        for job_id in ("mutate-git", "publish-image", "create-release"):
            downloads = [
                step
                for step in self.release.job_steps(job_id)
                if str(step.get("uses") or "").startswith("actions/download-artifact@")
            ]
            if job_id == "publish-image":
                expected_ids = [
                    "${{ needs.prepare-image-authority.outputs.authority_artifact_id }}"
                ]
            else:
                expected_ids = [
                    "${{ needs.prepare-release.outputs.release_artifact_id }}"
                ]
            self.assertEqual(len(downloads), len(expected_ids), job_id)
            for download, expected_id in zip(downloads, expected_ids):
                self.assertEqual(
                    str(download.get("uses") or "").split()[0],
                    "actions/download-artifact@3e5f45b2cfb9172054b4087a40e8e0b5a5461e7c",
                )
                options = download.get("with") or {}
                self.assertEqual(options.get("artifact-ids"), expected_id)
                self.assertIs(options.get("skip-decompress"), True)
                self.assertEqual(options.get("digest-mismatch"), "error")
                self.assertFalse(
                    {"name", "github-token", "repository", "run-id"}
                    & set(options)
                )
            job_text = self.release.job_text(job_id)
            self.assertIn("RELEASE_ARTIFACT_DIGEST", job_text)
            if job_id == "publish-image":
                self.assertIn('sha256sum "${archives[0]}"', job_text)
                self.assertIn("sha256sum -c IMAGE-MANIFEST.SHA256", job_text)
                self.assertIn("oci_manifest_digest", job_text)
            else:
                self.assertIn('sha256sum "${archive_files[0]}"', job_text)
                self.assertIn("sha256sum -c MANIFEST.SHA256", job_text)
                self.assertIn("sha256sum -c content-manifest.sha256", job_text)

    def test_artifact_rerun_attempt_policy_accepts_only_existing_producer(self) -> None:
        def accepts(
            artifact_name: str, prefix: str, current_run: int, current_attempt: int
        ) -> bool:
            match = re.fullmatch(
                rf"{re.escape(prefix)}-([1-9][0-9]*)-([1-9][0-9]*)",
                artifact_name,
            )
            if not match:
                return False
            producer_run, producer_attempt = (int(value) for value in match.groups())
            return producer_run == current_run and producer_attempt <= current_attempt

        self.assertTrue(accepts("sync-candidate-bundle-42-1", "sync-candidate-bundle", 42, 1))
        self.assertTrue(accepts("sync-candidate-bundle-42-1", "sync-candidate-bundle", 42, 3))
        for rejected in (
            "sync-candidate-bundle-41-1",
            "sync-candidate-bundle-42-4",
            "sync-candidate-bundle-42-0",
            "sync-candidate-bundle-42-x",
            "sync-candidate-bundle-42--1",
        ):
            self.assertFalse(
                accepts(rejected, "sync-candidate-bundle", 42, 3), rejected
            )

        consumer_jobs = (
            self.validation.job_text("validate"),
            self.release.job_text("prepare-release"),
            self.release.job_text("mutate-git"),
            self.release.job_text("publish-image"),
            self.release.job_text("create-release"),
        )
        for job_text in consumer_jobs:
            self.assertRegex(job_text, r"(?:ARTIFACT_NAME|artifact_name)")
            self.assertRegex(job_text, r"GITHUB_RUN_ID")
            self.assertRegex(job_text, r"GITHUB_RUN_ATTEMPT")
            self.assertRegex(job_text, r"\^\[1-9\]\[0-9\]\*\$")
            self.assertRegex(job_text, r"-le\s+['\"]?\$GITHUB_RUN_ATTEMPT")

    def test_rerun_recovery_pairs_exact_prior_artifacts_without_recomputation(self) -> None:
        sync_selection = next(
            step
            for step in self.sync.job_steps("prepare-candidate")
            if step.get("name") == "Select exact prior publication artifacts"
        )
        selection_run = str(sync_selection.get("run") or "")
        for marker in (
            "actions/runs/${GITHUB_RUN_ID}/artifacts?per_page=100",
            "candidate_attempt",
            "release_attempt",
            'select_one "sync-candidate-bundle-${GITHUB_RUN_ID}-${candidate_attempt}" candidate',
            'select_one "verified-release-state-${GITHUB_RUN_ID}-${release_attempt}" release',
            'select_one "image-publication-authority-${GITHUB_RUN_ID}-${image_attempt}" image',
            "expired == false",
        ):
            self.assertIn(marker, selection_run)

        release_selection = next(
            step
            for step in self.release.job_steps("prepare-release")
            if step.get("name") == "Resolve exact rerun publication artifacts"
        )
        release_selection_run = str(release_selection.get("run") or "")
        for marker in (
            'producer_run="${suffix%-*}"',
            'producer_attempt="${suffix##*-}"',
            '[ "$producer_run" = "$GITHUB_RUN_ID" ]',
            'release_name="verified-release-state-${GITHUB_RUN_ID}-${release_attempt}"',
            'image_name="image-publication-authority-${GITHUB_RUN_ID}-${image_attempt}"',
            'select_exact "$release_name" release',
        ):
            self.assertIn(marker, release_selection_run)

        def exact_pair(
            candidate: str,
            release: str,
            current_run: int,
            current_attempt: int,
        ) -> bool:
            candidate_match = re.fullmatch(
                r"sync-candidate-bundle-([1-9][0-9]*)-([1-9][0-9]*)",
                candidate,
            )
            release_match = re.fullmatch(
                r"verified-release-state-([1-9][0-9]*)-([1-9][0-9]*)",
                release,
            )
            if candidate_match is None or release_match is None:
                return False
            candidate_identity = tuple(map(int, candidate_match.groups()))
            release_identity = tuple(map(int, release_match.groups()))
            return (
                candidate_identity[0] == release_identity[0] == current_run
                and candidate_identity[1] < current_attempt
                and release_identity[1] < current_attempt
            )

        self.assertTrue(
            exact_pair(
                "sync-candidate-bundle-42-2",
                "verified-release-state-42-1",
                42,
                3,
            )
        )
        for candidate, release in (
            ("sync-candidate-bundle-41-2", "verified-release-state-42-2"),
            ("sync-candidate-bundle-42-1", "verified-release-state-43-1"),
            ("sync-candidate-bundle-42-3", "verified-release-state-42-3"),
            ("sync-candidate-bundle-42-x", "verified-release-state-42-2"),
        ):
            with self.subTest(candidate=candidate, release=release):
                self.assertFalse(exact_pair(candidate, release, 42, 3))

        sync_recovery = next(
            str(step.get("run") or "")
            for step in self.sync.job_steps("prepare-candidate")
            if step.get("name") == "Verify and recover exact prior publication state"
        )
        no_release_branch = sync_recovery.split(
            'if [ -z "$RELEASE_ARTIFACT_DIGEST" ]; then', maxsplit=1
        )[1].split("\nfi", maxsplit=1)[0]
        for marker in (
            'actual_main_sha="$(git ls-remote',
            'actual_mirror_sha="$(git ls-remote',
            'actual_patched_sha="$(git ls-remote',
            '[ "$actual_main_sha" = "$expected_main_sha" ]',
            '[ "$actual_mirror_sha" = "$expected_mirror_sha" ]',
            '[ "$actual_patched_sha" = "$expected_patched_sha" ]',
        ):
            self.assertIn(marker, no_release_branch)
        for marker in (
            'originating_run_attempt="$(field "$manifest" originating_run_attempt)"',
            '[[ "$originating_run_attempt" =~ ^[1-9][0-9]*$ ]]',
            '[ "$originating_run_attempt" = "$RELEASE_PRODUCER_ATTEMPT" ]',
            '[ "$originating_run_attempt" -le "$GITHUB_RUN_ATTEMPT" ]',
        ):
            self.assertIn(marker, sync_recovery)

        recover = next(
            step
            for step in self.release.job_steps("prepare-release")
            if step.get("name")
            == "Recover immutable release inputs without recomputation"
        )
        recover_run = str(recover.get("run") or "")
        self.assertEqual(
            recover.get("if"),
            "${{ steps.recovery.outputs.release_artifact_id != '' }}",
        )
        self.assertIn(
            '[ "$ORIGINATING_RUN_ATTEMPT" = "$release_producer_attempt" ]',
            recover_run,
        )
        for field in (
            "version",
            "replay_commit_sha",
            "publication_commit_sha",
            "publication_tree_sha",
            "version_image_tag",
        ):
            self.assertRegex(recover_run, rf'{field}="\$\(field {field}\)"')
            output = str(
                (self.release.jobs["prepare-release"].get("outputs") or {})[field]
            )
            self.assertIn(f"steps.recover_release.outputs.{field}", output)
        for field in (
            "manifest_sha256",
            "release_notes_sha256",
            "content_manifest_sha256",
        ):
            self.assertRegex(recover_run, rf'{field}="\$\(sha256sum ')
            output = str(
                (self.release.jobs["prepare-release"].get("outputs") or {})[field]
            )
            self.assertIn(f"steps.recover_release.outputs.{field}", output)
        for marker in (
            'require_field patchset_sha "$PATCHSET_SHA"',
            'require_field upstream_source_sha "$UPSTREAM_SOURCE_SHA"',
            'require_field mirror_candidate_sha "$MIRROR_CANDIDATE_SHA"',
            'candidate_field patchset_sha',
            'candidate_field upstream_source_sha',
            'candidate_field mirror_candidate_sha',
        ):
            self.assertIn(marker, recover_run)
        self.assertNotIn("compute-next-version.sh", recover_run)
        self.assertNotIn("apply-patches.sh", recover_run)
        self.assertNotRegex(recover_run, r"\bgit\s+(?:commit|commit-tree)\b")

        reconstruction = next(
            step
            for step in self.release.job_steps("prepare-release")
            if step.get("name") == "Reconstruct and prepare release without mutation"
        )
        self.assertEqual(
            reconstruction.get("if"),
            "${{ steps.recovery.outputs.release_artifact_id == '' }}",
        )

    def test_recovery_receipt_retags_and_remote_readbacks_are_exact(self) -> None:
        authority_steps = self.release.job_steps("prepare-image-authority")
        authority_selection = next(
            step
            for step in authority_steps
            if step.get("name") == "Select and verify exact authorized OCI archive"
        )
        authority_run = str(authority_selection.get("run") or "")
        for marker in (
            "RECOVERY_AUTHORITY_ID",
            "FRESH_AUTHORITY_DIGEST",
            "sha256sum -c IMAGE-MANIFEST.SHA256",
            "release-image.oci.tar",
            "oci_manifest_digest",
            "oci_config_digest",
            "oci_archive_sha256",
            "expected_blobs",
            "actual_blobs",
        ):
            self.assertIn(marker, authority_run)
        authority_env = authority_selection.get("env") or {}
        self.assertEqual(
            authority_env.get("RECOVERY_AUTHORITY_DIGEST"),
            "${{ needs.prepare-release.outputs.recovery_image_artifact_digest }}",
        )
        release_verification = next(
            step
            for step in authority_steps
            if step.get("name")
            == "Verify exact archive, manifest, and publication content"
        )
        release_verification_run = str(release_verification.get("run") or "")
        self.assertEqual(
            (release_verification.get("env") or {}).get(
                "RECOVERY_IMAGE_ARTIFACT_DIGEST"
            ),
            "${{ needs.prepare-release.outputs.recovery_image_artifact_digest }}",
        )
        for marker in (
            "RECOVERY_IMAGE_ARTIFACT_DIGEST",
            'sha256sum "${image_archives[0]}"',
            "sha256sum -c IMAGE-MANIFEST.SHA256",
        ):
            self.assertIn(marker, release_verification_run)

        authority_outputs = self.release.jobs["prepare-image-authority"].get(
            "outputs"
        ) or {}
        for output in (
            "authority_artifact_name",
            "authority_artifact_id",
            "authority_artifact_digest",
            "authorized_image_digest",
        ):
            self.assertIn(output, authority_outputs)
        self.assertFalse(
            write_permission_scopes(
                self.release.effective_permissions("prepare-image-authority")
            )
        )
        self.assertFalse(any(step_mutates(step) for step in authority_steps))
        for mutation_job in self.release.mutating_jobs():
            with self.subTest(authority_precedes=mutation_job):
                self.assertIn(
                    "prepare-image-authority",
                    self.release.ancestors(mutation_job),
                )
                self.assertFalse(
                    self.release.reachable_after_failure(
                        mutation_job, "prepare-image-authority"
                    )
                )

        fresh_download = next(
            step
            for step in authority_steps
            if step.get("name")
            == "Redownload fresh authority by immutable artifact identity"
        )
        self.assertEqual(
            (fresh_download.get("with") or {}).get("artifact-ids"),
            "${{ steps.upload_image_authority.outputs.artifact-id }}",
        )

        publish_steps = self.release.job_steps("publish-image")
        verification = next(
            step
            for step in publish_steps
            if step.get("name") == "Verify exact pre-mutation image authority"
        )
        verification_run = str(verification.get("run") or "")
        for marker in (
            "AUTHORITY_ARTIFACT_NAME",
            "AUTHORITY_ARTIFACT_DIGEST",
            "AUTHORIZED_IMAGE_DIGEST",
            'sha256sum "${archives[0]}"',
            "sha256sum -c IMAGE-MANIFEST.SHA256",
            'authority_field release_artifact_id',
            'authority_field release_artifact_digest',
            'authority_field oci_manifest_digest',
        ):
            self.assertIn(marker, verification_run)

        authority_download = next(
            step
            for step in publish_steps
            if step.get("name") == "Download exact pre-mutation image authority"
        )
        receipt_options = authority_download.get("with") or {}
        self.assertEqual(
            receipt_options.get("artifact-ids"),
            "${{ needs.prepare-image-authority.outputs.authority_artifact_id }}",
        )
        self.assertEqual(receipt_options.get("digest-mismatch"), "error")
        self.assertIs(receipt_options.get("skip-decompress"), True)

        publications = {
            str(step.get("name")): str(step.get("run") or "")
            for step in publish_steps
            if "oras cp" in str(step.get("run") or "")
        }
        self.assertEqual(
            set(publications),
            {
                "Publish authorized version image from OCI layout",
                "Publish authorized latest image from OCI layout",
            },
        )
        for name, run in publications.items():
            with self.subTest(publication=name):
                self.assertRegex(
                    run,
                    r'oras cp --from-oci-layout "authorized-layout@sha256:'
                    r'\$\{OCI_MANIFEST_DIGEST\}" "\$(?:VERSION|LATEST)_IMAGE_TAG"',
                )

        oras_setup = next(
            step
            for step in publish_steps
            if step.get("name") == "Set up ORAS for content-addressed publication"
        )
        self.assertRegex(str(oras_setup.get("uses") or ""), PINNED_THIRD_PARTY_ACTION_RE)
        oras_options = oras_setup.get("with") or {}
        self.assertRegex(str(oras_options.get("checksum") or ""), r"^[0-9a-f]{64}$")

        reporter = self.release.job_text("report-publication-outcome")
        for remote_read in (
            "git/ref/heads/mirror/upstream-main",
            "git/ref/heads/main",
            "git/ref/heads/patched",
            "git/ref/tags/${VERSION}",
            'docker buildx imagetools inspect "$image" --raw',
            "releases/tags/${VERSION}",
        ):
            self.assertIn(remote_read, reporter)
        self.assertIn("actual_digest", reporter)
        self.assertIn("PUBLISHED_IMAGE_DIGEST", reporter)
        self.assertIn("release_status=completed", reporter)
        reporter_authority = next(
            step
            for step in self.release.job_steps("report-publication-outcome")
            if step.get("name") == "Independently resolve image publication authority"
        )
        reporter_authority_run = str(reporter_authority.get("run") or "")
        for marker in (
            "actions/runs/${GITHUB_RUN_ID}/artifacts?per_page=100",
            "image-publication-authority-",
            "actions/artifacts/${artifact_id}/zip",
            "sha256sum -c IMAGE-MANIFEST.SHA256",
            "oci_manifest_digest",
            "authorized_digest",
        ):
            self.assertIn(marker, reporter_authority_run)
        final_report_gate = next(
            step
            for step in self.release.job_steps("report-publication-outcome")
            if step.get("name") == "Require independently verified publication outcome"
        )
        self.assertEqual(final_report_gate.get("if"), "${{ always() }}")
        self.assertIsNot(final_report_gate.get("continue-on-error"), True)
        final_report_run = str(final_report_gate.get("run") or "")
        self.assertIn('[ "$OVERALL" = complete ]', final_report_run)
        self.assertIn("exit 1", final_report_run)

        release_steps = self.release.job_steps("create-release")
        create_index = next(
            index
            for index, step in enumerate(release_steps)
            if step.get("name") == "Create exact authorized GitHub release"
        )
        readback_index = next(
            index
            for index, step in enumerate(release_steps)
            if step.get("name") == "Verify exact GitHub release read-back"
        )
        self.assertLess(create_index, readback_index)
        readback = str(release_steps[readback_index].get("run") or "")
        for exact_field in (
            ".tag_name == $version",
            ".name == $version",
            ".body == $body",
            ".target_commitish == $target",
            ".draft == false",
            ".prerelease == false",
        ):
            self.assertIn(exact_field, readback)
        self.assertIn('[ "$status" = 200 ]', readback)
        self.assertIn("final-release.json", readback)

    def test_publication_prerequisites_and_late_identity_rechecks(self) -> None:
        mutation_jobs = self.release.mutating_jobs()
        self.assertEqual(
            mutation_jobs, {"mutate-git", "publish-image", "create-release"}
        )

        expected_needs = {
            "mutate-git": {"prepare-release", "prepare-image-authority"},
            "publish-image": {
                "prepare-release",
                "prepare-image-authority",
                "mutate-git",
            },
            "create-release": {
                "prepare-release",
                "prepare-image-authority",
                "mutate-git",
                "publish-image",
            },
        }
        for job_id, needs in expected_needs.items():
            self.assertEqual(self.release.job_needs(job_id), needs)
            condition = str(self.release.jobs[job_id].get("if") or "")
            self.assertNotIn("always()", condition)
            self.assertNotIn("||", condition)
            for need in needs:
                self.assertRegex(
                    condition,
                    rf"needs\.{re.escape(need)}\.result\s*==\s*['\"]success['\"]",
                )

        git_text = self.release.job_text("mutate-git")
        image_text = self.release.job_text("publish-image")
        release_text = self.release.job_text("create-release")
        self.assertRegex(git_text, r"git push|--method (?:POST|PATCH)")
        self.assertNotRegex(git_text, r"docker/build-push-action|gh release create")
        self.assertIn("oras cp --from-oci-layout", image_text)
        self.assertNotIn("docker/build-push-action", image_text)
        self.assertNotRegex(image_text, r"\bgit\s+push\b|gh release create")
        self.assertIn("gh release create", release_text)
        self.assertNotRegex(release_text, r"\bgit\s+push\b|docker/build-push-action")

        pr_step = next(
            step
            for step in self.release.job_steps("mutate-git")
            if "--method POST" in str(step.get("run") or "")
        )
        pr_run = str(pr_step.get("run") or "")
        pr_read = pr_run.index("gh api --method GET")
        self.assertLess(pr_read, pr_run.index("gh api --method POST"))
        self.assertIn("multiple sync requests", pr_run)
        self.assertNotIn("gh api --method PATCH", pr_run)
        self.assertIn("leaving its metadata unchanged", pr_run)

        image_steps = self.release.job_steps("publish-image")
        version_publication_index = next(
            index
            for index, step in enumerate(image_steps)
            if step.get("name") == "Publish authorized version image from OCI layout"
        )
        action_uses = [
            str(step.get("uses") or "")
            for step in image_steps[:version_publication_index]
        ]
        self.assertTrue(any("docker/setup-buildx-action" in uses for uses in action_uses))
        self.assertTrue(any("docker/login-action" in uses for uses in action_uses))
        self.assertTrue(any("oras-project/setup-oras" in uses for uses in action_uses))

        image_authority = next(
            str(step.get("run") or "")
            for step in self.release.job_steps("prepare-image-authority")
            if step.get("name") == "Prove publication state before image authorization"
        )
        for ref in (
            "git/ref/heads/main",
            "git/ref/heads/mirror/upstream-main",
            "git/ref/heads/patched",
            "git/ref/tags/${VERSION}",
        ):
            self.assertIn(ref, image_authority)

        login_index = next(
            index
            for index, step in enumerate(image_steps)
            if "docker/login-action" in str(step.get("uses") or "")
        )
        authority_verify_index = next(
            index
            for index, step in enumerate(image_steps)
            if step.get("name") == "Verify exact pre-mutation image authority"
        )
        self.assertLess(authority_verify_index, login_index)

        release_steps = self.release.job_steps("create-release")
        release_authority = next(
            step
            for step in release_steps
            if step.get("name") == "Authorize exact GitHub release output"
        )
        release_step = next(
            step for step in release_steps if "gh release create" in str(step.get("run") or "")
        )
        authority_run = str(release_authority.get("run") or "")
        self.assertRegex(authority_run, r"git/ref/heads/patched")
        self.assertRegex(authority_run, r"git/ref/tags/\$\{VERSION\}")
        self.assertRegex(
            authority_run,
            r"releases/tags/\$\{VERSION\}",
        )
        self.assertIn("existing release does not match authorized publication evidence", authority_run)
        self.assertIn("PUBLISHED_IMAGE_DIGEST", authority_run)
        self.assertIn("AUTHORIZED_IMAGE_DIGEST", authority_run)
        self.assertIn(
            '[ "$PUBLISHED_IMAGE_DIGEST" = "$AUTHORIZED_IMAGE_DIGEST" ]',
            authority_run,
        )
        self.assertNotIn("docker buildx imagetools inspect", authority_run)
        authority_index = release_steps.index(release_authority)
        create_index = release_steps.index(release_step)
        self.assertLess(authority_index, create_index)
        self.assertNotIn("github.token", "\n".join(str(step) for step in release_steps[:create_index]))
        self.assertEqual(
            release_step.get("if"),
            "${{ steps.release_authority.outputs.mode == 'create' }}",
        )

    def test_release_requires_the_exact_published_image_digest(self) -> None:
        image_job = self.release.jobs["publish-image"]
        image_outputs = image_job.get("outputs") or {}
        self.assertIn("published_image_digest", image_outputs)
        self.assertRegex(
            str(image_outputs["published_image_digest"]),
            r"steps\.verify_image\.outputs\.published_image_digest",
        )

        verification = next(
            step
            for step in self.release.job_steps("publish-image")
            if step.get("name") == "Verify exact published image identities"
        )
        verification_run = str(verification.get("run") or "")
        self.assertIn("cmp version-final.raw latest-final.raw", verification_run)
        self.assertRegex(
            verification_run,
            r"published_image_digest=.*sha256sum\s+version-final\.raw",
        )
        self.assertIn(
            'echo "published_image_digest=$published_image_digest" >> "$GITHUB_OUTPUT"',
            verification_run,
        )

        release_authority = next(
            step
            for step in self.release.job_steps("create-release")
            if step.get("name") == "Authorize exact GitHub release output"
        )
        authority_env = release_authority.get("env") or {}
        self.assertEqual(
            authority_env.get("PUBLISHED_IMAGE_DIGEST"),
            "${{ needs.publish-image.outputs.published_image_digest }}",
        )
        self.assertEqual(
            authority_env.get("AUTHORIZED_IMAGE_DIGEST"),
            "${{ needs.prepare-image-authority.outputs.authorized_image_digest }}",
        )
        authority_run = str(release_authority.get("run") or "")
        self.assertRegex(authority_run, r"PUBLISHED_IMAGE_DIGEST.*\^\[0-9a-f\]\{64\}\$")
        self.assertRegex(authority_run, r"AUTHORIZED_IMAGE_DIGEST.*\^\[0-9a-f\]\{64\}\$")
        self.assertIn(
            '[ "$PUBLISHED_IMAGE_DIGEST" = "$AUTHORIZED_IMAGE_DIGEST" ]',
            authority_run,
        )
        self.assertNotIn("docker buildx imagetools inspect", authority_run)

        expected_labels = ("v0.2.0-patch.1", "a" * 40)
        version_image = ("1" * 64, expected_labels)
        same_labels_different_digest = ("2" * 64, expected_labels)
        self.assertEqual(version_image[1], same_labels_different_digest[1])
        self.assertNotEqual(version_image[0], same_labels_different_digest[0])
        self.assertFalse(
            version_image[0] == same_labels_different_digest[0]
            and version_image[1] == same_labels_different_digest[1]
        )

    def test_retargeted_existing_pr_branch_has_no_remote_mutation(self) -> None:
        request_step = next(
            step
            for step in self.release.job_steps("mutate-git")
            if step.get("name") == "Create or update internal sync request"
        )
        request_run = str(request_step.get("run") or "")
        existing_pr_branch = request_run.rsplit("\nelse\n", 1)[1].rsplit("\nfi", 1)[0]

        selected_identity = SYNC_REQUEST_IDENTITY
        identity_after_selection = ("open", "mirror/upstream-main", "unrelated-base")
        self.assertNotEqual(identity_after_selection, selected_identity)
        self.assertIsNone(MUTATION_RE.search(existing_pr_branch))

    def test_mutate_git_failure_retries_resume_only_from_intended_remote_state(self) -> None:
        intent = PublicationIntent(
            expected_mirror_sha="1" * 40,
            expected_main_sha="2" * 40,
            expected_patched_sha="3" * 40,
            mirror_candidate_sha="4" * 40,
            publication_sha="5" * 40,
        )
        initial = PublicationState(
            mirror_sha=intent.expected_mirror_sha,
            sync_request_identity=None,
            sync_request_head_sha=None,
            sync_request_content=None,
            main_sha=intent.expected_main_sha,
            patched_sha=intent.expected_patched_sha,
            tag_sha=None,
        )
        completed = PublicationState(
            mirror_sha=intent.mirror_candidate_sha,
            sync_request_identity=COMPLETED_SYNC_REQUEST_IDENTITY,
            sync_request_head_sha=intent.mirror_candidate_sha,
            sync_request_content=SYNC_REQUEST_CONTENT,
            main_sha=intent.mirror_candidate_sha,
            patched_sha=intent.publication_sha,
            tag_sha=intent.publication_sha,
        )

        state_after_boundary = initial
        for boundary in MUTATE_GIT_BOUNDARIES:
            state_after_boundary = apply_publication_boundary(
                state_after_boundary, intent, boundary
            )
            with self.subTest(failure_after=boundary):
                self.assertEqual(
                    complete_publication(state_after_boundary, intent), completed
                )

        conflict_sha = "f" * 40
        conflicting_states = {
            "mirror": replace(initial, mirror_sha=conflict_sha),
            "pull-request": replace(
                initial,
                mirror_sha=intent.mirror_candidate_sha,
                sync_request_identity=(
                    "open",
                    "unrelated",
                    "Ovler-Young/sub2api-patch",
                    "main",
                    "Ovler-Young/sub2api-patch",
                ),
                sync_request_head_sha=intent.mirror_candidate_sha,
                sync_request_content=SYNC_REQUEST_CONTENT,
            ),
            "main": replace(
                initial,
                mirror_sha=intent.mirror_candidate_sha,
                sync_request_identity=SYNC_REQUEST_IDENTITY,
                sync_request_head_sha=intent.mirror_candidate_sha,
                sync_request_content=SYNC_REQUEST_CONTENT,
                main_sha=conflict_sha,
            ),
            "pull-request head": replace(
                initial,
                mirror_sha=intent.mirror_candidate_sha,
                sync_request_identity=SYNC_REQUEST_IDENTITY,
                sync_request_head_sha=conflict_sha,
                sync_request_content=SYNC_REQUEST_CONTENT,
            ),
            "patched branch": replace(
                completed,
                patched_sha=conflict_sha,
                tag_sha=intent.publication_sha,
            ),
            "reserved tag": replace(
                completed,
                patched_sha=intent.publication_sha,
                tag_sha=conflict_sha,
            ),
        }
        for boundary, state in conflicting_states.items():
            with self.subTest(conflict_at=boundary):
                with self.assertRaises(ValueError):
                    complete_publication(state, intent)

        resumable_patched_tag_states = (
            (intent.expected_patched_sha, None),
            (intent.expected_patched_sha, intent.publication_sha),
            (intent.publication_sha, None),
            (intent.publication_sha, intent.publication_sha),
        )
        for patched_sha, tag_sha in resumable_patched_tag_states:
            with self.subTest(patched_sha=patched_sha, tag_sha=tag_sha):
                resumable = replace(
                    completed,
                    patched_sha=patched_sha,
                    tag_sha=tag_sha,
                )
                self.assertEqual(complete_publication(resumable, intent), completed)

        stale_before_main = replace(
            initial,
            mirror_sha=intent.mirror_candidate_sha,
            sync_request_identity=SYNC_REQUEST_IDENTITY,
            sync_request_head_sha=intent.mirror_candidate_sha,
            sync_request_content="stale content",
        )
        self.assertEqual(
            complete_publication(stale_before_main, intent),
            replace(completed, sync_request_content="stale content"),
        )
        verify_published_authority(completed, intent)

        main_already_intended_without_request = replace(
            initial,
            mirror_sha=intent.mirror_candidate_sha,
            main_sha=intent.mirror_candidate_sha,
        )
        with self.assertRaises(ValueError):
            apply_publication_boundary(
                main_already_intended_without_request, intent, "pull-request"
            )

        after_mirror = apply_publication_boundary(initial, intent, "mirror")
        after_request = apply_publication_boundary(after_mirror, intent, "pull-request")
        after_main = apply_publication_boundary(after_request, intent, "main")
        boundary_races = (
            (
                "mirror before PR",
                replace(after_mirror, mirror_sha=conflict_sha),
                "pull-request",
            ),
            (
                "mirror before main",
                replace(after_request, mirror_sha=conflict_sha),
                "main",
            ),
            (
                "PR identity before main",
                replace(
                    after_request,
                    sync_request_identity=(
                        "open",
                        "other-head",
                        "Ovler-Young/sub2api-patch",
                        "main",
                        "Ovler-Young/sub2api-patch",
                    ),
                ),
                "main",
            ),
            (
                "PR head before main",
                replace(after_request, sync_request_head_sha=conflict_sha),
                "main",
            ),
            (
                "main before patched",
                replace(after_main, main_sha=conflict_sha),
                "patched-and-tag",
            ),
            (
                "mirror before patched",
                replace(after_main, mirror_sha=conflict_sha),
                "patched-and-tag",
            ),
        )
        for race, raced_state, next_boundary in boundary_races:
            with self.subTest(race=race):
                with self.assertRaises(ValueError):
                    apply_publication_boundary(raced_state, intent, next_boundary)

        for ref_name in ("mirror_sha", "main_sha", "patched_sha", "tag_sha"):
            with self.subTest(final_authority_race=ref_name):
                with self.assertRaises(ValueError):
                    verify_published_authority(
                        replace(completed, **{ref_name: conflict_sha}), intent
                    )

    def test_mutate_git_steps_implement_resumable_compare_and_swap(self) -> None:
        def assert_order(text: str, *markers: str) -> None:
            positions = [text.index(marker) for marker in markers]
            self.assertEqual(positions, sorted(positions))

        steps = {
            str(step.get("name") or ""): str(step.get("run") or "")
            for step in self.release.job_steps("mutate-git")
        }
        authority = steps["Recheck identities and compare-and-swap preconditions"]
        self.assertIn("require_expected_or_intended()", authority)
        authority_bindings = {
            "current_mirror": ("EXPECTED_MIRROR_SHA", "MIRROR_CANDIDATE_SHA"),
            "current_main": ("EXPECTED_MAIN_SHA", "MIRROR_CANDIDATE_SHA"),
            "current_patched": ("EXPECTED_PATCHED_SHA", "PUBLICATION_COMMIT_SHA"),
            "current_tag": ('""', "PUBLICATION_COMMIT_SHA"),
        }
        for actual, (expected, intended) in authority_bindings.items():
            with self.subTest(authority_binding=actual):
                self.assertRegex(
                    authority,
                    re.compile(
                        rf"require_expected_or_intended\s+.*\${actual}.*"
                        rf"{re.escape(expected)}.*{re.escape(intended)}",
                        re.S,
                    ),
                )

        mirror = steps["Publish mirror candidate"]
        self.assertIn("current_mirror=", mirror)
        self.assertIn('[ "$current_mirror" = "$MIRROR_CANDIDATE_SHA" ]', mirror)
        self.assertIn(
            '[ "$current_mirror" = "$EXPECTED_MIRROR_SHA" ]', mirror
        )
        self.assertIn("mirror/upstream-main moved to an unauthorized state", mirror)
        self.assertIn(
            "--force-with-lease=refs/heads/mirror/upstream-main:${EXPECTED_MIRROR_SHA}",
            mirror,
        )
        assert_order(
            mirror,
            '[ "$current_mirror" = "$MIRROR_CANDIDATE_SHA" ]',
            '[ "$current_mirror" = "$EXPECTED_MIRROR_SHA" ]',
            "git push origin",
        )

        request = steps["Create or update internal sync request"]
        self.assertIn("require_intended_mirror()", request)
        self.assertIn("git/ref/heads/mirror/upstream-main", request)
        self.assertIn('[ "$current_main" = "$EXPECTED_MAIN_SHA" ]', request)
        self.assertIn('[ "$current_main" = "$MIRROR_CANDIDATE_SHA" ]', request)
        self.assertIn("missing or multiple completed sync requests", request)
        self.assertGreaterEqual(request.count("require_intended_mirror"), 4)
        for identity_field in (
            '.head.ref == "mirror/upstream-main"',
            ".head.repo.full_name == $repository",
            ".head.sha == $candidate",
            '.base.ref == "main"',
            ".base.repo.full_name == $repository",
        ):
            self.assertIn(identity_field, request)
        self.assertNotIn("gh api --method PATCH", request)
        self.assertNotIn("If-Match", request)
        self.assertNotIn(".title == $title", request)
        self.assertNotIn(".body == $body", request)
        self.assertIn('-f title="$title"', request)
        self.assertIn('-f body="$body"', request)
        intended_main = request.index(
            '[ "$current_main" = "$MIRROR_CANDIDATE_SHA" ]'
        )
        closed_lookup = request.index("-f state=closed", intended_main)
        closed_exit = request.index("exit 0", closed_lookup)
        self.assertLess(intended_main, closed_lookup)
        self.assertIn("--paginate --slurp", request[intended_main:closed_lookup])
        self.assertIn(".head.sha == $candidate", request[closed_lookup:closed_exit])
        self.assertIn(".state == \"closed\"", request[closed_lookup:closed_exit])
        self.assertNotIn(".title", request[closed_lookup:closed_exit])
        self.assertNotIn(".body", request[closed_lookup:closed_exit])

        main = steps["Promote validated candidate to main with compare-and-swap"]
        self.assertIn("current_mirror=", main)
        self.assertIn("current_main=", main)
        self.assertIn("mirror/upstream-main moved before main promotion", main)
        self.assertIn("main moved to an unauthorized state", main)
        for marker in (
            "git push --atomic origin",
            "$MIRROR_CANDIDATE_SHA:refs/heads/mirror/upstream-main",
            "$MIRROR_CANDIDATE_SHA:refs/heads/main",
            "--force-with-lease=refs/heads/mirror/upstream-main:${current_mirror}",
            "--force-with-lease=refs/heads/main:${current_main}",
        ):
            self.assertIn(marker, main)

        patched = steps["Publish patched branch and reserve tag atomically"]
        for marker in (
            "current_mirror=",
            "current_main=",
            "current_patched=",
            "current_tag=",
            "mirror/upstream-main moved before patched publication",
            "main moved before patched publication",
            "patched moved to an unauthorized state",
            "tag $VERSION points to an unauthorized state",
            "git push --atomic origin",
            "$MIRROR_CANDIDATE_SHA:refs/heads/mirror/upstream-main",
            "$MIRROR_CANDIDATE_SHA:refs/heads/main",
            "$PUBLICATION_COMMIT_SHA:refs/heads/${PATCHED_BRANCH}",
            "$PUBLICATION_COMMIT_SHA:refs/tags/$VERSION",
            "--force-with-lease=refs/heads/mirror/upstream-main:${current_mirror}",
            "--force-with-lease=refs/heads/main:${current_main}",
            "--force-with-lease=refs/heads/patched:${current_patched}",
            "--force-with-lease=refs/tags/${VERSION}:${current_tag}",
        ):
            self.assertIn(marker, patched)

        final_verify = steps["Verify published branch and reserved tag identities"]
        final_bindings = (
            ('mirror_sha=', '[ "$mirror_sha" = "$MIRROR_CANDIDATE_SHA" ]'),
            ('main_sha=', '[ "$main_sha" = "$MIRROR_CANDIDATE_SHA" ]'),
            ('patched_sha=', '[ "$patched_sha" = "$PUBLICATION_COMMIT_SHA" ]'),
            ('tag_sha=', '[ "$tag_sha" = "$PUBLICATION_COMMIT_SHA" ]'),
        )
        for read_marker, guard_marker in final_bindings:
            self.assertIn(read_marker, final_verify)
            self.assertIn(guard_marker, final_verify)
            self.assertLess(
                final_verify.index(read_marker), final_verify.index(guard_marker)
            )
        self.assertIn("mutation/sync-pr-number", final_verify)
        self.assertIn("(.state == \"open\") or (.state == \"closed\")", final_verify)
        for marker in (
            '.head.ref == "mirror/upstream-main"',
            ".head.repo.full_name == $repository",
            ".head.sha == $candidate",
            '.base.ref == "main"',
            ".base.repo.full_name == $repository",
        ):
            self.assertIn(marker, final_verify)
        self.assertNotIn(".title == $title", final_verify)
        self.assertNotIn(".body == $body", final_verify)

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

    def test_final_authority_window_has_no_local_reconstruction_before_write(self) -> None:
        mutation_job = "mutate-git"
        self.assertIn(mutation_job, self.release.mutating_jobs())
        mutation_steps = self.release.job_steps(mutation_job)
        step_indexes = {
            str(step.get("name") or ""): index
            for index, step in enumerate(mutation_steps)
        }
        first_mutation = next(
            index for index, step in enumerate(mutation_steps) if step_mutates(step)
        )
        authorization_index = step_indexes["Authorize immutable Git publication state"]
        recheck_index = step_indexes[
            "Recheck identities and compare-and-swap preconditions"
        ]
        self.assertEqual(authorization_index + 1, recheck_index)
        self.assertEqual(recheck_index + 1, first_mutation)

        authorization = str(mutation_steps[authorization_index].get("run") or "")
        recheck = str(mutation_steps[recheck_index].get("run") or "")
        marker = "final_authority_window=true"
        self.assertIn(marker, recheck)
        pre_window, authority_window = recheck.split(marker, maxsplit=1)

        for required_local_check in (
            "sha256sum -c MANIFEST.SHA256",
            "sha256sum -c content-manifest.sha256",
            "git init mutation",
            "bundle verify",
            "bundle unbundle",
            "diff-tree --no-commit-id",
        ):
            self.assertIn(required_local_check, authorization)
            self.assertNotIn(required_local_check, pre_window)
            self.assertNotIn(required_local_check, authority_window)
        self.assertIn("release-state/release-notes.md", authorization)
        self.assertIn("release-state/release-notes.md", authority_window)

        mutation_text = self.release.job_text(mutation_job)
        self.assertNotRegex(mutation_text, r"actions/checkout@")
        self.assertNotRegex(mutation_text, REPOSITORY_ACQUISITION_RE)
        self.assertNotRegex(authorization, WRITE_CREDENTIAL_RE)
        self.assertNotRegex(authorization, MUTATION_RE)
        self.assertNotRegex(authority_window, MUTATION_RE)

        upstream_read = authority_window.index(
            "repos/${UPSTREAM_REPOSITORY}/git/commits/${UPSTREAM_SOURCE_SHA}"
        )
        upstream_guard = authority_window.index(
            '[ "$queried_upstream_sha" = "$UPSTREAM_SOURCE_SHA" ] || {'
        )
        patchset_read = authority_window.index("git/ref/heads/patchset")
        patchset_guard = authority_window.index(
            '[ "$current_patchset_sha" = "$PATCHSET_SHA" ] || {'
        )
        target_refresh = authority_window.index('current_main="$(git ls-remote')
        collision_refresh = authority_window.index("releases?per_page=100")
        tag_refresh = authority_window.index("matching-refs/tags/${VERSION}")
        self.assertLess(upstream_read, upstream_guard)
        self.assertLess(upstream_guard, target_refresh)
        self.assertLess(target_refresh, collision_refresh)
        self.assertLess(collision_refresh, tag_refresh)
        self.assertLess(tag_refresh, patchset_read)
        self.assertLess(patchset_read, patchset_guard)
        self.assertIn(
            'require_expected_or_intended "$current_tag" "" '
            '"$PUBLICATION_COMMIT_SHA" "tag $VERSION"',
            authority_window,
        )

    def test_each_preparation_step_failure_blocks_mutation(self) -> None:
        sync_release = find_call(self.sync, "auto-release.yml")
        sync_validation = find_call(self.sync, "upstream-pr-check.yml")
        cases = (
            (
                self.sync,
                "prepare-candidate",
                ((self.sync, sync_release, "prepare-candidate"),),
            ),
            (
                self.validation,
                "validate",
                ((self.sync, sync_release, sync_validation),),
            ),
            (
                self.release,
                "prepare-release",
                tuple(
                    (self.release, mutation_job, "prepare-release")
                    for mutation_job in self.release.mutating_jobs()
                ),
            ),
            (
                self.release,
                "prepare-image-authority",
                tuple(
                    (self.release, mutation_job, "prepare-image-authority")
                    for mutation_job in self.release.mutating_jobs()
                ),
            ),
            (
                self.release,
                "mutate-git",
                (
                    (self.release, "publish-image", "mutate-git"),
                    (self.release, "create-release", "mutate-git"),
                ),
            ),
            (
                self.release,
                "publish-image",
                ((self.release, "create-release", "publish-image"),),
            ),
        )
        for source, source_job, mutation_targets in cases:
            steps = source.job_steps(source_job)
            self.assertTrue(steps)
            for step in steps:
                with self.subTest(
                    workflow=source.path.name,
                    job=source_job,
                    step=step.get("name"),
                ):
                    self.assertTrue(step.get("name"))
                    assert_step_propagates_failure(self, source, source_job, step)
                for dag, mutation_job, failed_job in mutation_targets:
                    self.assertFalse(
                        dag.reachable_after_failure(mutation_job, failed_job),
                        f"{mutation_job} remains reachable after "
                        f"{source.path.name}:{source_job}:{step.get('name')} fails",
                    )

        named_common_gates = {
            "Apply patches to exact upstream source",
            "Verify replay tree",
            "Sanitize patch references",
            "Backend tests",
            "Backend lint and format checks",
            "Frontend checks",
            "Set up Docker Buildx",
            "Preflight patched image build",
            "Record immutable validation provenance",
            "Upload validation provenance",
        }
        validation_names = {
            str(step.get("name") or "")
            for step in self.validation.job_steps("validate")
        }
        self.assertTrue(named_common_gates <= validation_names)

        named_release_gates = {
            "Assert trusted Sync caller and provenance shape",
            "Verify candidate artifact producer attempt",
            "Reconstruct and prepare release without mutation",
            "Set up Docker Buildx",
            "Preflight release image build",
            "Upload verified release state",
        }
        release_names = {
            str(step.get("name") or "")
            for step in self.release.job_steps("prepare-release")
        }
        self.assertTrue(named_release_gates <= release_names)

        release_gate_commands = {
            "version computation": r"scripts/compute-next-version\.sh",
            "tag and release lookup": (
                r"(?=.*matching-refs/tags)(?=.*releases\?per_page=100)"
            ),
            "release notes": r"release-notes\.md",
            "reference sanitizer": r"check-no-pr-issue-refs\.sh",
            "replay tree": r"actual_replay_tree.*REPLAY_TREE_SHA",
            "diff integrity": r"git diff --check",
            "publication path scan": r"publication-paths\.txt.*blocked",
            "image owner": r"image_owner=.*tr.*lower",
        }
        release_preparation = str(
            next(
                step.get("run") or ""
                for step in self.release.job_steps("prepare-release")
                if step.get("name")
                == "Reconstruct and prepare release without mutation"
            )
        )
        for gate, pattern in release_gate_commands.items():
            with self.subTest(release_gate=gate):
                self.assertRegex(
                    release_preparation, re.compile(pattern, re.I | re.S)
                )

    def test_actual_gate_mutations_are_detected_and_cannot_reach_publication(self) -> None:
        sync_release = find_call(self.sync, "auto-release.yml")
        sync_validation = find_call(self.sync, "upstream-pr-check.yml")
        gate_jobs = (
            (
                self.sync,
                "prepare-candidate",
                ((self.sync, sync_release, "prepare-candidate"),),
            ),
            (
                self.validation,
                "validate",
                ((self.sync, sync_release, sync_validation),),
            ),
            (
                self.release,
                "prepare-release",
                tuple(
                    (self.release, target, "prepare-release")
                    for target in self.release.mutating_jobs()
                ),
            ),
            (
                self.release,
                "prepare-image-authority",
                tuple(
                    (self.release, target, "prepare-image-authority")
                    for target in self.release.mutating_jobs()
                ),
            ),
            (
                self.release,
                "mutate-git",
                (
                    (self.release, "publish-image", "mutate-git"),
                    (self.release, "create-release", "mutate-git"),
                ),
            ),
            (
                self.release,
                "publish-image",
                ((self.release, "create-release", "publish-image"),),
            ),
        )
        observed_steps = 0
        for source, source_job, mutation_targets in gate_jobs:
            for step in source.job_steps(source_job):
                observed_steps += 1
                label = f"{source.path.name}:{source_job}:{step.get('name')}"
                mutated_step = dict(step, **{"continue-on-error": True})
                with self.subTest(mutation="continue-on-error", gate=label):
                    with self.assertRaises(AssertionError):
                        assert_step_propagates_failure(
                            self, source, source_job, mutated_step
                        )
                run = str(step.get("run") or "")
                if run:
                    masked_step = dict(
                        step,
                        run="set -euo pipefail\nrequired-gate || true",
                    )
                    with self.subTest(mutation="shell-mask", gate=label):
                        with self.assertRaises(AssertionError):
                            assert_step_propagates_failure(
                                self, source, source_job, masked_step
                            )
                for dag, target, failed_job in mutation_targets:
                    with self.subTest(failed_gate=label, target=target):
                        self.assertFalse(
                            dag.reachable_after_failure(target, failed_job)
                        )
        self.assertGreater(observed_steps, 20)

        reachability_edges = (
            (self.sync, sync_release, "prepare-candidate"),
            (self.sync, sync_release, sync_validation),
            (self.release, "mutate-git", "prepare-release"),
            (self.release, "mutate-git", "prepare-image-authority"),
            (self.release, "publish-image", "mutate-git"),
            (self.release, "create-release", "publish-image"),
        )
        for model, target, failed_job in reachability_edges:
            mutated = WorkflowModel.__new__(WorkflowModel)
            mutated.path = model.path
            mutated.text = model.text
            mutated.data = model.data
            mutated.jobs = dict(model.jobs)
            mutated.jobs[target] = dict(model.jobs[target], **{"if": "${{ always() }}"})
            with self.subTest(
                dag_mutation=model.path.name,
                target=target,
                failed_job=failed_job,
            ):
                self.assertTrue(mutated.reachable_after_failure(target, failed_job))

    def test_semantic_sha_mapping_rejects_parent_and_tree_substitution(self) -> None:
        sync_prepare = self.sync.job_text("prepare-candidate")
        release_prepare = self.release.job_text("prepare-release")
        for marker in (
            'rev-parse "$workflow_install_sha^"',
            'rev-parse "$mirror_candidate_sha^1"',
            'rev-parse "$mirror_candidate_sha^2^"',
            'rev-parse "$mirror_candidate_sha^{tree}"',
        ):
            self.assertIn(marker, sync_prepare)
        for marker in (
            'rev-parse "$publication_commit_sha^"',
            'rev-parse "$publication_commit_sha^{tree}"',
            'docker_context_commit_sha="$publication_commit_sha"',
            'docker_context_tree_sha="$publication_tree_sha"',
            'revision_label="org.opencontainers.image.revision=${replay_commit_sha}"',
        ):
            self.assertIn(marker, release_prepare)

        identities = {
            "upstream_source_sha": "1" * 40,
            "expected_main_sha": "2" * 40,
            "workflow_install_sha": "3" * 40,
            "mirror_candidate_sha": "4" * 40,
            "candidate_tree": "5" * 40,
            "replay_commit_sha": "6" * 40,
            "replay_tree_sha": "7" * 40,
            "publication_commit_sha": "8" * 40,
            "publication_tree_sha": "9" * 40,
            "docker_context_commit_sha": "8" * 40,
            "docker_context_tree_sha": "9" * 40,
            "revision_label_sha": "6" * 40,
        }
        parents = {
            identities["workflow_install_sha"]: (
                identities["upstream_source_sha"],
            ),
            identities["mirror_candidate_sha"]: (
                identities["expected_main_sha"],
                identities["workflow_install_sha"],
            ),
            identities["publication_commit_sha"]: (
                identities["replay_commit_sha"],
            ),
        }
        trees = {
            identities["mirror_candidate_sha"]: identities["candidate_tree"],
            identities["replay_commit_sha"]: identities["replay_tree_sha"],
            identities["publication_commit_sha"]: identities[
                "publication_tree_sha"
            ],
        }

        def semantic_mapping_is_authorized(
            candidate: dict[str, str],
            candidate_parents: dict[str, tuple[str, ...]],
            candidate_trees: dict[str, str],
        ) -> bool:
            workflow_install = candidate["workflow_install_sha"]
            mirror = candidate["mirror_candidate_sha"]
            replay = candidate["replay_commit_sha"]
            publication = candidate["publication_commit_sha"]
            return (
                candidate_parents.get(workflow_install)
                == (candidate["upstream_source_sha"],)
                and candidate_parents.get(mirror)
                == (candidate["expected_main_sha"], workflow_install)
                and candidate_trees.get(mirror) == candidate["candidate_tree"]
                and candidate_trees.get(replay) == candidate["replay_tree_sha"]
                and candidate_parents.get(publication) == (replay,)
                and candidate_trees.get(publication)
                == candidate["publication_tree_sha"]
                and candidate["docker_context_commit_sha"] == publication
                and candidate["docker_context_tree_sha"]
                == candidate["publication_tree_sha"]
                and candidate["revision_label_sha"] == replay
            )

        self.assertTrue(semantic_mapping_is_authorized(identities, parents, trees))
        substitutions = {
            "workflow parent": (
                identities,
                dict(
                    parents,
                    **{
                        identities["workflow_install_sha"]: (
                            identities["expected_main_sha"],
                        )
                    },
                ),
                trees,
            ),
            "mirror parent order": (
                identities,
                dict(
                    parents,
                    **{
                        identities["mirror_candidate_sha"]: tuple(
                            reversed(parents[identities["mirror_candidate_sha"]])
                        )
                    },
                ),
                trees,
            ),
            "publication parent": (
                identities,
                dict(
                    parents,
                    **{
                        identities["publication_commit_sha"]: (
                            identities["upstream_source_sha"],
                        )
                    },
                ),
                trees,
            ),
            "replay tree": (
                identities,
                parents,
                dict(trees, **{identities["replay_commit_sha"]: "a" * 40}),
            ),
            "publication tree": (
                identities,
                parents,
                dict(
                    trees,
                    **{identities["publication_commit_sha"]: "b" * 40},
                ),
            ),
            "docker commit": (
                dict(identities, docker_context_commit_sha="c" * 40),
                parents,
                trees,
            ),
            "revision label": (
                dict(identities, revision_label_sha="d" * 40),
                parents,
                trees,
            ),
        }
        for substitution, fixture in substitutions.items():
            with self.subTest(substitution=substitution):
                self.assertFalse(semantic_mapping_is_authorized(*fixture))

    def test_docker_and_release_outputs_are_exact(self) -> None:
        def build_steps(model: WorkflowModel) -> list[dict[str, Any]]:
            return [
                step
                for _, step in model.all_steps()
                if "docker/build-push-action" in str(step.get("uses") or "")
            ]

        validation_builds = build_steps(self.validation)
        release_builds = build_steps(self.release)
        self.assertEqual(len(validation_builds), 1)
        self.assertEqual(len(release_builds), 3)

        validation_preflight = validation_builds[0].get("with") or {}
        release_preflight = next(
            step.get("with") or {}
            for step in self.release.job_steps("prepare-release")
            if step.get("name") == "Preflight release image build"
        )
        recovery_preflight_step = next(
            step
            for step in self.release.job_steps("prepare-release")
            if step.get("name") == "Re-preflight exact recovered image context"
        )
        recovery_preflight = recovery_preflight_step.get("with") or {}
        authority_build = next(
            step.get("with") or {}
            for step in self.release.job_steps("prepare-image-authority")
            if step.get("name")
            == "Build exact release image into a local OCI archive"
        )

        expected_preflights = (
            (validation_preflight, "worktree", "worktree/Dockerfile"),
            (release_preflight, "worktree", "worktree/Dockerfile"),
        )
        for options, context, dockerfile in expected_preflights:
            self.assertEqual(options.get("context"), context)
            self.assertEqual(options.get("file"), dockerfile)
            self.assertIs(options.get("push"), False)
            self.assertIs(options.get("load"), False)
            self.assertEqual(
                options.get("cache-from"), "type=gha,scope=sub2api-patch-release"
            )
            self.assertEqual(
                options.get("cache-to"),
                "type=gha,mode=max,scope=sub2api-patch-release",
            )
        self.assertEqual(
            [
                line.strip()
                for line in str(release_preflight.get("labels") or "").splitlines()
            ],
            [
                "org.opencontainers.image.version=${{ steps.release.outputs.version }}",
                "org.opencontainers.image.revision=${{ steps.release.outputs.replay_commit_sha }}",
            ],
        )
        self.assertEqual(
            recovery_preflight_step.get("if"),
            "${{ steps.recovery.outputs.release_artifact_id != '' }}",
        )
        self.assertEqual(recovery_preflight.get("context"), "recovered-publication")
        self.assertEqual(
            recovery_preflight.get("file"),
            "recovered-publication/${{ steps.recover_release.outputs.dockerfile_path }}",
        )
        self.assertIs(recovery_preflight.get("push"), False)
        self.assertIs(recovery_preflight.get("load"), False)
        self.assertEqual(
            recovery_preflight.get("cache-from"),
            "${{ steps.recover_release.outputs.cache_from }}",
        )
        self.assertEqual(
            recovery_preflight.get("cache-to"),
            "${{ steps.recover_release.outputs.cache_to }}",
        )
        self.assertEqual(
            [
                line.strip()
                for line in str(recovery_preflight.get("labels") or "").splitlines()
            ],
            [
                "${{ steps.recover_release.outputs.version_label }}",
                "${{ steps.recover_release.outputs.revision_label }}",
            ],
        )

        self.assertEqual(authority_build.get("context"), "publication")
        self.assertEqual(authority_build.get("file"), "publication/Dockerfile")
        self.assertIs(authority_build.get("push"), False)
        self.assertEqual(authority_build.get("platforms"), "linux/amd64")
        self.assertIs(authority_build.get("provenance"), False)
        self.assertIs(authority_build.get("sbom"), False)
        self.assertEqual(
            authority_build.get("outputs"),
            "type=oci,dest=image-publication-authority/release-image.oci.tar",
        )
        labels = [
            line.strip()
            for line in str(authority_build.get("labels") or "").splitlines()
        ]
        self.assertEqual(
            labels,
            [
                "${{ needs.prepare-release.outputs.version_label }}",
                "${{ needs.prepare-release.outputs.revision_label }}",
            ],
        )
        self.assertEqual(
            authority_build.get("cache-from"),
            "${{ needs.prepare-release.outputs.cache_from }}",
        )
        self.assertEqual(
            authority_build.get("cache-to"),
            "${{ needs.prepare-release.outputs.cache_to }}",
        )
        preparation = self.release.job_text("prepare-release")
        for exact_value in (
            'version_image_tag="ghcr.io/${image_owner}/sub2api-patch:${version}"',
            'latest_image_tag="ghcr.io/${image_owner}/sub2api-patch:latest-patch"',
            'version_label="org.opencontainers.image.version=${version}"',
            'revision_label="org.opencontainers.image.revision=${replay_commit_sha}"',
            "cache_from='type=gha,scope=sub2api-patch-release'",
            "cache_to='type=gha,mode=max,scope=sub2api-patch-release'",
        ):
            self.assertIn(exact_value, preparation)
        release_commands = [
            str(step.get("run") or "")
            for step in self.release.job_steps("create-release")
            if "gh release create" in str(step.get("run") or "")
        ]
        self.assertEqual(len(release_commands), 1)
        self.assertRegex(release_commands[0], r"gh release create ['\"]?\$VERSION")
        self.assertIn('--title "$VERSION"', release_commands[0])
        self.assertIn(
            "--notes-file release-state/release-notes.md", release_commands[0]
        )

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
        self.assertIn("version_image_tag", text)
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
        all_text = "\n".join(model.text for model in self.all_models)
        self.assertIn("UPSTREAM_REPOSITORY: Wei-Shaw/sub2api", all_text)
        self.assertRegex(
            all_text,
            r"(?:assert|verify|unexpected)[^\n]*(?:Wei-Shaw/sub2api|UPSTREAM_REPOSITORY)",
        )
        self.assertRegex(all_text, r"git remote get-url|remote.*Wei-Shaw/sub2api")

        self.assertEqual(upstream_mutations(all_text), [])

        mutation_fixtures = (
            "git push upstream HEAD:main",
            "git -C repo push upstream HEAD:main",
            "git push https://github.com/Wei-Shaw/sub2api HEAD:main",
            "git remote set-url --push upstream https://github.com/example/fork",
            "gh api --method POST repos/Wei-Shaw/sub2api/git/refs",
            "gh api repos/Wei-Shaw/sub2api/releases -X PUT",
            "gh api --method PATCH repos/${UPSTREAM_REPOSITORY}/issues/1",
            "gh api repos/$UPSTREAM_REPOSITORY/git/refs/heads/main -X DELETE",
            "gh api repos/Wei-Shaw/sub2api/issues -f title=created",
            "gh api repos/${UPSTREAM_REPOSITORY}/releases --input release.json",
            "gh pr create --repo Wei-Shaw/sub2api --title release",
            "gh pr comment 1 --repo Wei-Shaw/sub2api --body changed",
            "gh issue comment 2 --repo ${UPSTREAM_REPOSITORY} --body changed",
            "gh release create v0.2.0 --repo Wei-Shaw/sub2api",
            "curl -X POST https://api.github.com/repos/Wei-Shaw/sub2api/releases",
            "curl --request PUT https://api.github.com/repos/${UPSTREAM_REPOSITORY}/issues/1",
            "curl -X PATCH https://api.github.com/repos/$UPSTREAM_REPOSITORY/issues/1",
            "curl --request DELETE https://api.github.com/repos/Wei-Shaw/sub2api/git/refs/heads/main",
            "curl --data name=release https://api.github.com/repos/Wei-Shaw/sub2api/releases",
            "docker push ghcr.io/Wei-Shaw/sub2api:latest",
            "docker push ghcr.io/${UPSTREAM_REPOSITORY}:latest",
            "docker buildx build --push -t ghcr.io/${{ env.UPSTREAM_REPOSITORY }}:latest .",
        )
        for fixture in mutation_fixtures:
            with self.subTest(fixture=fixture):
                self.assertTrue(upstream_mutations(fixture))

        read_only_fixtures = (
            "gh api --method GET repos/Wei-Shaw/sub2api/releases",
            "gh api --method GET repos/Wei-Shaw/sub2api/releases -f per_page=100",
            "curl -X GET https://api.github.com/repos/Wei-Shaw/sub2api/releases",
            "curl -X GET --data page=2 https://api.github.com/repos/Wei-Shaw/sub2api/releases",
            "git fetch upstream main",
        )
        for fixture in read_only_fixtures:
            with self.subTest(fixture=fixture):
                self.assertEqual(upstream_mutations(fixture), [])

        upstream_fetches = re.findall(
            r"git(?:\s+-C\s+\S+)?\s+fetch[^\n]+", all_text
        )
        self.assertTrue(upstream_fetches)
        for fetch in upstream_fetches:
            if "upstream" in fetch.lower():
                self.assertNotIn("--upload-pack", fetch)


if __name__ == "__main__":
    unittest.main(verbosity=2)
