#!/usr/bin/env python3
from __future__ import annotations

import re
import subprocess
import sys
import tempfile
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
        elif not in_hunk and line.startswith(("rename to ", "copy to ")):
            selected.append(line)
    return "".join(selected)


def mailinfo_text(text: str) -> str:
    with tempfile.TemporaryDirectory() as directory:
        root = Path(directory)
        message_path = root / "message"
        patch_path = root / "patch"
        result = subprocess.run(
            ["git", "mailinfo", "--encoding=UTF-8", message_path, patch_path],
            input=text,
            text=True,
            capture_output=True,
            check=False,
        )
        if result.returncode != 0:
            detail = result.stderr.strip() or "unknown git mailinfo error"
            raise SystemExit(f"could not parse format-patch message: {detail}")
        metadata = result.stdout + message_path.read_text(encoding="utf-8")
        patch = patch_path.read_text(encoding="utf-8")
    return metadata + added_patch_text(patch.splitlines(keepends=True))


def downstream_text(text: str) -> str:
    message = Parser(policy=policy.default).parsestr(text)
    if message.is_multipart():
        raise SystemExit("could not parse multipart format-patch message")
    payload = message.get_payload()
    if not isinstance(payload, str):
        raise SystemExit("could not decode format-patch message")

    separators = list(re.finditer(r"(?m)^---\r?\n", payload))
    if not separators:
        return mailinfo_text(text)
    separator = separators[-1]
    metadata = "\n".join(f"{name}: {value}" for name, value in message.raw_items())
    metadata += "\n" + payload[: separator.start()]
    patch = payload[separator.end() :]
    return metadata + added_patch_text(patch.splitlines(keepends=True))


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
