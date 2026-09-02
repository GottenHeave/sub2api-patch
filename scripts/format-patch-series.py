#!/usr/bin/env python3
from __future__ import annotations

import argparse
import os
import re
import subprocess
import tempfile
from pathlib import Path

REALTIME_PATCH = "0008-feat-proxy-OpenAI-realtime-REST-endpoints.patch"
BUILD_CACHE_PATCH = "0002-build-cache-pnpm-dependencies-with-BuildKit.patch"
ROUTE_AUDIT_DIFF = (
    "diff --git a/backend/internal/server/routes/prompt_audit_route_coverage_test.go "
    "b/backend/internal/server/routes/prompt_audit_route_coverage_test.go"
)
HUNK_HEADER = re.compile(
    r"^@@ -([0-9]+),([0-9]+) \+([0-9]+),([0-9]+) @@(.*)$"
)


def trim_realtime_optional_context(path: Path) -> None:
    lines = path.read_text(encoding="utf-8").splitlines(keepends=True)
    in_route_audit = False
    trimmed = 0
    output: list[str] = []
    index = 0
    while index < len(lines):
        line = lines[index]
        if line.startswith("diff --git "):
            in_route_audit = line.rstrip("\r\n") == ROUTE_AUDIT_DIFF
        match = HUNK_HEADER.match(line.rstrip("\r\n")) if in_route_audit else None
        if match and index + 1 < len(lines) and '"/transcribe":' in lines[index + 1]:
            end = index + 1
            while end < len(lines) and not lines[end].startswith(("@@ ", "diff --git ")):
                end += 1
            body = lines[index + 1 : end]
            old_start, old_count, new_start, new_count = map(int, match.groups()[:4])
            deletions = [item for item in body if item.startswith("-")]
            additions = [item for item in body if item.startswith("+")]
            contexts = [item for item in body if item.startswith(" ")]
            if (
                (old_count, new_count) != (3, 13)
                or len(deletions) != 1
                or len(additions) != 11
                or len(contexts) != 2
                or '"/transcribe":' not in contexts[0]
                or contexts[-1].strip() != "}"
                or '"/custom-voices":' not in deletions[0]
                or '"/custom-voices":' not in additions[-1]
            ):
                raise SystemExit(f"unexpected Realtime optional-context hunk in {path}")
            suffix = match.group(5)
            newline = "\r\n" if line.endswith("\r\n") else "\n"
            output.append(
                f"@@ -{old_start + 1},{old_count - 1} "
                f"+{new_start + 1},{new_count - 1} @@{suffix}{newline}"
            )
            output.extend(additions[:-1])
            output.extend((deletions[0], additions[-1], contexts[-1]))
            index = end
            trimmed += 1
            continue
        output.append(line)
        index += 1
    if trimmed != 1:
        raise SystemExit(
            f"expected one Realtime optional-context hunk in {path}, found {trimmed}"
        )
    path.write_text("".join(output), encoding="utf-8")


def extend_build_cache_eof_context(path: Path) -> None:
    lines = path.read_text(encoding="utf-8").splitlines(keepends=True)
    if not lines or lines[-1].strip() or not lines[-1].startswith(" "):
        raise SystemExit(f"expected trailing blank Dockerfile context in {path}")
    header_index = next(
        (
            index
            for index in range(len(lines) - 2, -1, -1)
            if lines[index].startswith("@@ ")
        ),
        None,
    )
    if header_index is None:
        raise SystemExit(f"expected Dockerfile hunk in {path}")
    line = lines[header_index]
    match = HUNK_HEADER.match(line.rstrip("\r\n"))
    if match is None:
        raise SystemExit(f"unexpected Dockerfile hunk header in {path}")
    old_start, old_count, new_start, new_count = map(int, match.groups()[:4])
    suffix = match.group(5)
    newline = "\r\n" if line.endswith("\r\n") else "\n"
    lines[header_index] = (
        f"@@ -{old_start},{old_count + 1} +{new_start},{new_count + 1} "
        f"@@{suffix}{newline}"
    )
    lines.append(f" # -----------------------------------------------------------------------------{newline}")
    path.write_text("".join(lines), encoding="utf-8")


parser = argparse.ArgumentParser()
parser.add_argument("--repo", required=True, type=Path)
parser.add_argument("--base-ref", required=True)
parser.add_argument("--output", required=True, type=Path)
arguments = parser.parse_args()
arguments.output.mkdir(parents=True, exist_ok=True)

environment = os.environ.copy()
for name in list(environment):
    if name.startswith("GIT_CONFIG_") or name in {
        "GIT_ALTERNATE_OBJECT_DIRECTORIES",
        "GIT_COMMON_DIR",
        "GIT_DIR",
        "GIT_INDEX_FILE",
        "GIT_OBJECT_DIRECTORY",
        "GIT_WORK_TREE",
    }:
        environment.pop(name)
environment["LC_ALL"] = "C"
environment["GIT_CONFIG_NOSYSTEM"] = "1"
environment["GIT_CONFIG_GLOBAL"] = os.devnull


def run_git(*command: str) -> subprocess.CompletedProcess[bytes]:
    return subprocess.run(
        ["git", *command],
        env=environment,
        capture_output=True,
        check=False,
    )


source_repo = arguments.repo.resolve()
head = run_git("-C", str(source_repo), "rev-parse", "HEAD^{commit}")
base = run_git(
    "-C", str(source_repo), "rev-parse", f"{arguments.base_ref}^{{commit}}"
)
common_dir = run_git(
    "-C", str(source_repo), "rev-parse", "--path-format=absolute", "--git-common-dir"
)
object_format = run_git("-C", str(source_repo), "rev-parse", "--show-object-format")
for name, probe in (
    ("HEAD", head),
    ("base ref", base),
    ("Git common directory", common_dir),
    ("Git object format", object_format),
):
    if probe.returncode != 0:
        detail = probe.stderr.decode("utf-8", errors="replace").strip()
        raise SystemExit(f"could not resolve {name}: {detail or 'unknown error'}")

with tempfile.TemporaryDirectory() as directory:
    neutral_repo = Path(directory) / "repository.git"
    init = run_git(
        "init",
        "--quiet",
        "--bare",
        "--template=",
        f"--object-format={object_format.stdout.decode().strip()}",
        str(neutral_repo),
    )
    if init.returncode != 0:
        detail = init.stderr.decode("utf-8", errors="replace").strip()
        raise SystemExit(f"could not initialize neutral Git repository: {detail}")
    alternates = neutral_repo / "objects" / "info" / "alternates"
    source_objects = Path(common_dir.stdout.decode().strip()) / "objects"
    alternates.write_text(f"{source_objects}\n", encoding="utf-8")
    result = run_git(
        "--git-dir",
        str(neutral_repo),
        "-c",
        "core.attributesFile=/dev/null",
        "-c",
        "core.quotePath=true",
        "-c",
        "diff.algorithm=default",
        "-c",
        "diff.indentHeuristic=false",
        "format-patch",
        "--zero-commit",
        "--no-signature",
        "--numbered",
        "--no-numbered-files",
        "--subject-prefix=PATCH",
        "--suffix=.patch",
        "--filename-max-length=64",
        "--no-thread",
        "--no-cover-letter",
        "--no-base",
        "--stat",
        "--unified=1",
        "--full-index",
        "--no-renames",
        "--no-ext-diff",
        "--no-textconv",
        "--src-prefix=a/",
        "--dst-prefix=b/",
        "-o",
        str(arguments.output.resolve()),
        f"{base.stdout.decode().strip()}..{head.stdout.decode().strip()}",
    )
if result.returncode != 0:
    detail = result.stderr.decode("utf-8", errors="replace").strip()
    raise SystemExit(f"git format-patch failed: {detail or 'unknown error'}")

target = arguments.output / REALTIME_PATCH
if target.exists():
    trim_realtime_optional_context(target)
target = arguments.output / BUILD_CACHE_PATCH
if target.exists():
    extend_build_cache_eof_context(target)
