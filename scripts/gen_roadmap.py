#!/usr/bin/env python3
"""Render ROADMAP.md into website/roadmap.html, styled to match the site.
ROADMAP.md is the single source of truth; CI runs this and fails if the
page has drifted, so the public roadmap can never fall out of sync with
the repo's plan.

Like gen_changelog.py, this is deliberately dependency-free (stdlib
only) so it runs on any CI runner with no install step. It differs from
the changelog renderer in one way that matters: the roadmap uses ordered
(numbered) lists for its phases and milestones, so this supports both
`- ` and `N. ` list items."""
import html
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
SRC = ROOT / "ROADMAP.md"
OUT = ROOT / "website" / "roadmap.html"


def inline(s: str) -> str:
    """Minimal inline markdown → HTML: escape, then code/bold/italics/links."""
    s = html.escape(s)
    s = re.sub(r"`([^`]+)`", r'<span class="mono">\1</span>', s)
    s = re.sub(r"\*\*(.+?)\*\*", r"<b>\1</b>", s)  # bold first, may wrap inner *italics*
    s = re.sub(r"\*([^*]+)\*", r"<em>\1</em>", s)
    s = re.sub(r"\[([^\]]+)\]\((https?://[^)]+)\)", r'<a href="\2">\1</a>', s)
    return s


def render(md: str) -> str:
    body, para = [], []
    intro_done, lead_used = False, False
    list_kind = None  # None | "ul" | "ol"

    def flush_para():
        nonlocal para, lead_used
        if not para:
            return
        text = inline(" ".join(para))
        cls = "lead" if (not intro_done and not lead_used) else ""
        if cls:
            lead_used = True
        body.append(f'<p class="{cls}">{text}</p>' if cls else f"<p>{text}</p>")
        para = []

    def close_list():
        nonlocal list_kind
        if list_kind:
            body.append(f"</{list_kind}>")
            list_kind = None

    def open_list(kind):
        nonlocal list_kind
        if list_kind != kind:
            close_list()
            body.append(f"<{kind}>")
            list_kind = kind

    # Unwrap soft wraps: an indented continuation line folds onto the
    # line above it (how Markdown continues a list item or paragraph).
    merged = []
    for raw in md.splitlines():
        if raw[:1] in (" ", "\t") and raw.strip() and merged and merged[-1].strip() \
           and not merged[-1].lstrip().startswith("#"):
            merged[-1] = merged[-1].rstrip() + " " + raw.strip()
        else:
            merged.append(raw)

    for raw in merged:
        line = raw.rstrip()
        if re.match(r"^\[[^\]]+\]:\s+https?://", line) or line.startswith("# "):
            continue
        if line.startswith("## "):
            flush_para(); close_list(); intro_done = True
            body.append(f"<h2>{inline(line[3:])}</h2>")
        elif line.startswith("### "):
            flush_para(); close_list()
            body.append(f"<h3>{inline(line[4:])}</h3>")
        elif line.startswith("- "):
            flush_para(); open_list("ul")
            body.append(f"<li>{inline(line[2:])}</li>")
        elif re.match(r"^\d+\.\s", line):
            flush_para(); open_list("ol")
            item = re.sub(r"^\d+\.\s", "", line)
            body.append(f"<li>{inline(item)}</li>")
        elif line.strip() == "":
            flush_para(); close_list()
        else:
            close_list()
            para.append(line.strip())
    flush_para(); close_list()
    return "\n".join(body)


TEMPLATE = """<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Silt roadmap</title>
<meta name="description" content="Where Silt is going: milestones, the prioritized sequence, and what comes next.">
<link rel="canonical" href="https://silthq.com/roadmap.html">
<link rel="preconnect" href="https://fonts.googleapis.com">
<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
<link href="https://fonts.googleapis.com/css2?family=Fraunces:ital,opsz,wght@0,9..144,400;0,9..144,500;1,9..144,400&family=IBM+Plex+Mono:wght@400;500&family=IBM+Plex+Sans:wght@400;500;600&display=swap" rel="stylesheet">
<link rel="stylesheet" href="style.css">
<style>
  .doc ol {{ padding-left:1.2rem; }}
  .doc ol li {{ margin:0.4rem 0; }}
</style>
</head>
<body>
<nav>
  <a href="/" class="wordmark" style="text-decoration:none">Sil<b>t</b></a>
  <span class="spacer"></span>
  <a href="/#how" class="hide-sm">How it works</a>
  <a href="node.html" class="hide-sm">Run a node</a>
  <a href="changelog.html">Changelog</a>
  <a href="docs.html">Docs</a>
  <a href="https://github.com/nerolabs/silt" class="ghost">GitHub</a>
</nav>
<div class="doc">
  <p class="eyebrow">Roadmap</p>
  <h1>Where Silt is going</h1>
{body}
  <p style="margin-top:3rem"><a href="/" class="btn ghost">← Back to silthq.com</a></p>
</div>
<footer><div class="wrap"><div class="meta">
  <span>silthq.com</span><span>·</span><span class="dim">the infrastructure is not the content</span>
</div></div></footer>
</body>
</html>
"""


def main() -> int:
    if not SRC.exists():
        print(f"error: {SRC} not found", file=sys.stderr)
        return 1
    OUT.write_text(TEMPLATE.format(body=render(SRC.read_text())))
    print(f"wrote {OUT.relative_to(ROOT)}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
