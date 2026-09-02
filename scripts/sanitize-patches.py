#!/usr/bin/env python3
from __future__ import annotations

import argparse
import re
import subprocess
import sys
import tempfile
from pathlib import Path

subject_ref = re.compile(r"\s*\(#[0-9]+\)")
body_patterns = [
    re.compile(r"(^|[^A-Za-z0-9_])#[0-9]+"),
    re.compile(r"PR\s*#[0-9]+", re.IGNORECASE),
    re.compile(r"pull request\s*#[0-9]+", re.IGNORECASE),
    re.compile(r"issue\s*#[0-9]+", re.IGNORECASE),
    re.compile(r"/issues/[0-9]+"),
    re.compile(r"/pull/[0-9]+"),
]
hunk_header = re.compile(
    r"^@@ -[0-9]+(?:,([0-9]+))? \+[0-9]+(?:,([0-9]+))? @@"
)


def sanitize(lines: list[str]) -> list[str]:
    output: list[str] = []
    for line in lines:
        if line.startswith("Subject: "):
            line = subject_ref.sub("", line)
        if line.lower().startswith("co-authored-by:"):
            continue
        output.append(line)
    return output


def git(repo: Path, *arguments: str) -> bytes:
    result = subprocess.run(
        ["git", "-C", str(repo), *arguments],
        capture_output=True,
        check=False,
    )
    if result.returncode != 0:
        detail = result.stderr.decode("utf-8", errors="replace").strip()
        raise SystemExit(f"git {' '.join(arguments)} failed: {detail or 'unknown error'}")
    return result.stdout


def check_text(path: Path, text: str) -> None:
    for pattern in body_patterns:
        if pattern.search(text):
            raise SystemExit(f"blocked pull request or issue reference in {path}")


def destination_paths(repo: Path, parent: str, commit: str) -> list[str]:
    fields = git(
        repo,
        "diff-tree",
        "--no-commit-id",
        "--name-status",
        "-r",
        "-z",
        "-M",
        "-C",
        parent,
        commit,
    ).split(b"\0")
    if fields and not fields[-1]:
        fields.pop()
    destinations: list[str] = []
    index = 0
    while index < len(fields):
        status = fields[index].decode("ascii")
        index += 1
        path_count = 2 if status.startswith(("R", "C")) else 1
        if index + path_count > len(fields):
            raise SystemExit("invalid git diff-tree name-status output")
        paths = fields[index : index + path_count]
        index += path_count
        if not status.startswith("D"):
            try:
                destinations.append(paths[-1].decode("utf-8"))
            except UnicodeDecodeError as error:
                raise SystemExit("Git destination path is not UTF-8") from error
    return destinations


def added_content(repo: Path, parent: str, commit: str) -> str:
    lines = git(
        repo,
        "diff",
        "--unified=0",
        "--no-color",
        "--no-ext-diff",
        "--no-textconv",
        parent,
        commit,
        "--",
    ).decode("utf-8").splitlines(keepends=True)
    added: list[str] = []
    old_remaining = 0
    new_remaining = 0
    for line in lines:
        match = hunk_header.match(line)
        if match:
            old_remaining = int(match.group(1) or "1")
            new_remaining = int(match.group(2) or "1")
            continue
        if old_remaining == 0 and new_remaining == 0:
            continue
        if line.startswith("+"):
            added.append(line[1:])
            new_remaining -= 1
        elif line.startswith("-"):
            old_remaining -= 1
        elif line.startswith(" "):
            old_remaining -= 1
            new_remaining -= 1
        elif line.startswith("\\ No newline at end of file"):
            continue
        else:
            raise SystemExit("invalid git diff hunk output")
        if old_remaining < 0 or new_remaining < 0:
            raise SystemExit("invalid git diff hunk counts")
    if old_remaining != 0 or new_remaining != 0:
        raise SystemExit("incomplete git diff hunk output")
    return "".join(added)


def reject_binary_changes(repo: Path, parent: str, commit: str) -> None:
    records = git(
        repo,
        "diff",
        "--numstat",
        "--no-renames",
        "-z",
        parent,
        commit,
        "--",
    ).split(b"\0")
    if any(record.startswith(b"-\t-\t") for record in records if record):
        raise SystemExit("binary patch content cannot be reference-scanned")


def generate_canonical_series(
    repo: Path, base_sha: str, output: Path
) -> list[Path]:
    generator = Path(__file__).with_name("format-patch-series.py")
    result = subprocess.run(
        [
            sys.executable,
            str(generator),
            "--repo",
            str(repo),
            "--base-ref",
            base_sha,
            "--output",
            str(output),
        ],
        capture_output=True,
        check=False,
    )
    if result.returncode != 0:
        detail = result.stderr.decode("utf-8", errors="replace").strip()
        raise SystemExit(f"canonical patch generation failed: {detail or 'unknown error'}")
    return sorted(output.glob("*.patch"))


def verify_series(repo: Path, base_ref: str, patches: list[Path]) -> None:
    base_sha = git(repo, "rev-parse", f"{base_ref}^{{commit}}").decode().strip()
    with tempfile.TemporaryDirectory() as directory:
        root = Path(directory)
        replay = root / "replay"
        result = subprocess.run(
            ["git", "clone", "--quiet", "--no-checkout", str(repo), str(replay)],
            capture_output=True,
            check=False,
        )
        if result.returncode != 0:
            detail = result.stderr.decode("utf-8", errors="replace").strip()
            raise SystemExit(f"could not clone patch base repository: {detail}")
        git(replay, "config", "user.name", "patch replay validation")
        git(replay, "config", "user.email", "patch-replay@example.invalid")
        git(replay, "config", "core.hooksPath", "/dev/null")
        git(replay, "checkout", "--quiet", "--detach", base_sha)

        for patch in patches:
            parent = git(replay, "rev-parse", "HEAD").decode().strip()
            git(
                replay,
                "-c",
                "core.hooksPath=/dev/null",
                "am",
                "--quiet",
                "--no-3way",
                str(patch.resolve()),
            )
            commit = git(replay, "rev-parse", "HEAD").decode().strip()
            metadata = git(replay, "show", "-s", "--format=%s%n%b", commit).decode(
                "utf-8"
            )
            paths = destination_paths(replay, parent, commit)
            reject_binary_changes(replay, parent, commit)
            additions = added_content(replay, parent, commit)
            check_text(patch, "\n".join([metadata, additions, *paths]))

        canonical = root / "canonical"
        canonical.mkdir()
        generated = generate_canonical_series(replay, base_sha, canonical)
        if len(generated) != len(patches):
            raise SystemExit("canonical patch count differs from input series")
        for supplied, expected in zip(patches, generated, strict=True):
            if supplied.name != expected.name or supplied.read_bytes() != expected.read_bytes():
                raise SystemExit(f"patch is not canonical for applied commit: {supplied}")


parser = argparse.ArgumentParser()
parser.add_argument("--check", action="store_true")
parser.add_argument("--repo", required=True, type=Path)
parser.add_argument("--base-ref", required=True)
parser.add_argument("patches", nargs="+", type=Path)
arguments = parser.parse_args()

for patch_path in arguments.patches:
    original = patch_path.read_text(encoding="utf-8")
    sanitized = "".join(sanitize(original.splitlines(keepends=True)))
    if arguments.check and sanitized != original:
        raise SystemExit(f"patch requires sanitization: {patch_path}")
    if not arguments.check:
        patch_path.write_text(sanitized, encoding="utf-8")

verify_series(arguments.repo.resolve(), arguments.base_ref, arguments.patches)
