#!/usr/bin/env python3
"""Print the CHANGELOG.md section for a given version tag — used by the
release workflow to fill in GitHub Release notes. Usage:

    release_notes.py v0.1.0
"""
import re
import sys
from pathlib import Path

CHANGELOG = Path(__file__).resolve().parent.parent / "CHANGELOG.md"


def section_for(version: str) -> str:
    want = version.lstrip("v")
    lines = CHANGELOG.read_text().splitlines()
    out, capturing = [], False
    for line in lines:
        if line.startswith("## "):
            if capturing:
                break  # next version — stop
            m = re.match(r"^##\s+\[?([^\]\s]+)\]?", line)
            capturing = bool(m and m.group(1).lstrip("v") == want)
            continue
        if capturing and not re.match(r"^\[[^\]]+\]:\s+https?://", line):
            out.append(line)
    return "\n".join(out).strip()


def main() -> int:
    if len(sys.argv) != 2:
        print("usage: release_notes.py <version>", file=sys.stderr)
        return 2
    notes = section_for(sys.argv[1])
    if not notes:
        print(f"No changelog section for {sys.argv[1]}", file=sys.stderr)
        # Don't fail the release over missing notes; emit a stub.
        notes = f"Release {sys.argv[1]}."
    print(notes)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
