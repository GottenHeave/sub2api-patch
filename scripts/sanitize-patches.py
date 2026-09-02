#!/usr/bin/env python3
from __future__ import annotations

import re
import sys
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


def downstream_text(lines: list[str]) -> str:
    selected: list[str] = []
    in_diff = False
    for line in lines:
        if line.startswith("diff --git "):
            in_diff = True
            selected.append(line)
        elif not in_diff:
            selected.append(line)
        elif line.startswith("+") and not line.startswith("+++ "):
            selected.append(line[1:])
        elif line.startswith(("+++ ", "rename to ", "copy to ")):
            selected.append(line)
    return "".join(selected)


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
    added_text = downstream_text(lines)
    for pattern in body_patterns:
        if pattern.search(added_text):
            raise SystemExit(f"blocked pull request or issue reference in {path}")
    if not check_only:
        path.write_text(text, encoding="utf-8")
