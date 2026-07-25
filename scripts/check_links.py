#!/usr/bin/env python3
"""Verify every relative link and asset in website/*.html resolves to a
real file. External URLs, anchors, and the site root are deliberately not
checked — the health of the wider web is not this project's CI gate; the
integrity of what we ship is. Dependency-free so it runs anywhere."""
import re
import sys
from pathlib import Path

WEB = Path(__file__).resolve().parent.parent / "website"


def main() -> int:
    broken = []
    for html in sorted(WEB.glob("*.html")):
        text = html.read_text()
        for m in re.finditer(r'(?:href|src)="([^"]+)"', text):
            u = m.group(1)
            # skip external (http/https/protocol-relative), mailto, pure
            # anchors, and the deploy root "/" (valid live, not a file here)
            if re.match(r"^(https?:)?//|^mailto:|^#|^/", u):
                continue
            path = u.split("#")[0].split("?")[0]
            if path and not (WEB / path).exists():
                broken.append(f"{html.name}  →  {u}")
    for b in broken:
        print("BROKEN INTERNAL LINK:", b)
    if broken:
        return 1
    print("internal links OK")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
