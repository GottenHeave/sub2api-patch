#!/usr/bin/env python3
from __future__ import annotations

import re
import subprocess
import sys
from email import policy
from email.parser import Parser
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


def sanitize(lines: list[str]) -> list[str]:
    out: list[str] = []
    for line in lines:
        if line.startswith("Subject: "):
            line = subject_ref.sub("", line)
        if line.lower().startswith("co-authored-by:"):
            continue
        out.append(line)
    return out


def prefixed_path(path: str, prefix: str) -> str:
    if path.startswith('"'):
        return f'"{prefix}{path[1:]}'
    return prefix + path


def replaced_prefix(path: str, old: str, new: str) -> str:
    quoted_old = f'"{old}'
    if path.startswith(quoted_old):
        return f'"{new}{path[len(quoted_old):]}'
    if path.startswith(old):
        return new + path[len(old) :]
    raise SystemExit("invalid Git diff path prefix")


def validate_diff_header_paths(lines: list[str]) -> None:
    payload: str | None = None
    old_path: str | None = None
    new_path: str | None = None
    rename_from: str | None = None
    rename_to: str | None = None
    copy_from: str | None = None
    copy_to: str | None = None
    in_headers = False

    def validate_block() -> None:
        if payload is None:
            return
        expected: str | None = None
        if old_path is not None and new_path is not None:
            source = (
                replaced_prefix(new_path, "b/", "a/")
                if old_path == "/dev/null"
                else old_path
            )
            destination = (
                replaced_prefix(old_path, "a/", "b/")
                if new_path == "/dev/null"
                else new_path
            )
            expected = f"{source} {destination}"
        elif rename_from is not None and rename_to is not None:
            expected = (
                f"{prefixed_path(rename_from, 'a/')} "
                f"{prefixed_path(rename_to, 'b/')}"
            )
        elif copy_from is not None and copy_to is not None:
            expected = (
                f"{prefixed_path(copy_from, 'a/')} "
                f"{prefixed_path(copy_to, 'b/')}"
            )
        elif not payload.startswith('"') and payload.count(" b/") != 1:
            raise SystemExit("invalid Git diff destination header")
        if expected is not None and payload != expected:
            raise SystemExit("invalid Git diff destination header")

    for line in lines:
        if line.startswith("diff --git "):
            validate_block()
            payload = line.removeprefix("diff --git ").rstrip("\r\n")
            old_path = None
            new_path = None
            rename_from = None
            rename_to = None
            copy_from = None
            copy_to = None
            in_headers = True
        elif in_headers and line.startswith("--- "):
            old_path = line.removeprefix("--- ").rstrip("\r\n\t")
        elif in_headers and line.startswith("+++ "):
            new_path = line.removeprefix("+++ ").rstrip("\r\n\t")
        elif in_headers and line.startswith("rename from "):
            rename_from = line.removeprefix("rename from ").rstrip("\r\n")
        elif in_headers and line.startswith("rename to "):
            rename_to = line.removeprefix("rename to ").rstrip("\r\n")
        elif in_headers and line.startswith("copy from "):
            copy_from = line.removeprefix("copy from ").rstrip("\r\n")
        elif in_headers and line.startswith("copy to "):
            copy_to = line.removeprefix("copy to ").rstrip("\r\n")
        elif line.startswith(("@@ ", "GIT binary patch")):
            in_headers = False
    validate_block()


def added_patch_text(lines: list[str]) -> str:
    selected: list[str] = []
    in_hunk = False
    for line in lines:
        if line.startswith("diff --git "):
            in_hunk = False
        elif line.startswith("@@ "):
            in_hunk = True
        elif in_hunk and line.startswith("+"):
            selected.append(line[1:])
        elif in_hunk and not line.startswith(("-", " ", "\\ No newline")):
            in_hunk = False
        elif not in_hunk and line.startswith(("+++ ", "rename to ", "copy to ")):
            selected.append(line)
    return "".join(selected)


def validated_destination_paths(patch: str) -> list[str]:
    if not re.search(r"(?m)^diff --git ", patch):
        raise SystemExit("canonical format-patch message contains no Git diff")
    patch_lines = patch.splitlines(keepends=True)
    validate_diff_header_paths(patch_lines)
    result = subprocess.run(
        ["git", "apply", "--numstat", "-z", "--"],
        input=patch.encode("utf-8"),
        capture_output=True,
        check=False,
    )
    if result.returncode != 0:
        detail = result.stderr.decode("utf-8", errors="replace").strip()
        raise SystemExit(f"invalid Git patch structure: {detail or 'git apply failed'}")

    fields = result.stdout.split(b"\0")
    if fields and not fields[-1]:
        fields.pop()
    destinations: list[str] = []
    deleted_blocks: list[bool] = []
    for line in patch_lines:
        if line.startswith("diff --git "):
            deleted_blocks.append(False)
        elif deleted_blocks and (
            line.startswith("deleted file mode ")
            or line.rstrip("\r\n") == "+++ /dev/null"
        ):
            deleted_blocks[-1] = True
    index = 0
    record_index = 0
    while index < len(fields):
        record = fields[index]
        index += 1
        parts = record.split(b"\t", 2)
        if len(parts) != 3:
            raise SystemExit("invalid Git numstat record")
        path = parts[2]
        if path:
            destination = path
        else:
            if index + 1 >= len(fields):
                raise SystemExit("invalid Git rename numstat record")
            index += 1
            destination = fields[index]
            index += 1
        if record_index >= len(deleted_blocks):
            raise SystemExit("Git numstat contains an unmatched path")
        if not deleted_blocks[record_index]:
            try:
                destinations.append(destination.decode("utf-8"))
            except UnicodeDecodeError as error:
                raise SystemExit("Git destination path is not UTF-8") from error
        record_index += 1
    if record_index != len(deleted_blocks):
        raise SystemExit("Git diff contains a path missing from numstat")
    if not deleted_blocks:
        raise SystemExit("canonical format-patch message contains no changed paths")
    return destinations


def downstream_text(text: str) -> str:
    message = Parser(policy=policy.default).parsestr(text)
    if message.is_multipart():
        raise SystemExit("could not parse multipart format-patch message")
    payload = message.get_payload()
    if not isinstance(payload, str):
        raise SystemExit("could not decode format-patch message")

    separators = list(re.finditer(r"(?m)^---\r?\n", payload))
    if not separators:
        raise SystemExit("format-patch message lacks canonical format-patch separator")
    separator = separators[-1]
    metadata = "\n".join(f"{name}: {value}" for name, value in message.raw_items())
    metadata += "\n" + payload[: separator.start()]
    patch = payload[separator.end() :]
    destinations = validated_destination_paths(patch)
    return "\n".join(
        [metadata, added_patch_text(patch.splitlines(keepends=True)), *destinations]
    )


check_only = False
args = sys.argv[1:]
if args and args[0] == "--check":
    check_only = True
    args = args[1:]

for arg in args:
    path = Path(arg)
    if not path.exists():
        continue
    original = path.read_text(encoding="utf-8")
    lines = sanitize(original.splitlines(keepends=True))
    text = "".join(lines)
    if check_only and text != original:
        raise SystemExit(f"patch requires sanitization: {path}")
    added_text = downstream_text(text)
    for pattern in body_patterns:
        if pattern.search(added_text):
            raise SystemExit(f"blocked pull request or issue reference in {path}")
    if not check_only:
        path.write_text(text, encoding="utf-8")
