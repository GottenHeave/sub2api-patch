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

for arg in sys.argv[1:]:
    path = Path(arg)
    if not path.exists():
        continue
    text = path.read_text(encoding="utf-8")
    lines = text.splitlines(keepends=True)
    out: list[str] = []
    for line in lines:
        if line.startswith("Subject: "):
            line = subject_ref.sub("", line)
        if line.lower().startswith("co-authored-by:"):
            continue
        out.append(line)
    text = "".join(out)
    for pattern in body_patterns:
        if pattern.search(text):
            raise SystemExit(f"blocked pull request or issue reference in {path}")
    path.write_text(text, encoding="utf-8")
