#!/usr/bin/env python3
"""Render docs/buildlog/*.md into website/buildlog.html, styled to match
the site. The dated entry files under docs/buildlog/ are the single
source of truth; CI runs this and fails if the page has drifted, so the
published build log can never fall out of sync with the repo — the same
pipeline as gen_changelog.py and gen_roadmap.py.

Deliberately dependency-free (stdlib only) so it runs on any CI runner
with no install step.

An entry is a file named `YYYY-MM-DD-slug.md` whose first line is an
`# H1` title; the rest is the body. Entries render newest first."""
import html
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
SRC = ROOT / "docs" / "buildlog"
OUT = ROOT / "website" / "buildlog.html"

ENTRY_RE = re.compile(r"^(\d{4}-\d{2}-\d{2})-.*\.md$")


def inline(s: str) -> str:
    """Minimal inline markdown → HTML: escape, then code/bold/italics/links."""
    s = html.escape(s)
    s = re.sub(r"`([^`]+)`", r'<span class="mono">\1</span>', s)
    s = re.sub(r"\*\*(.+?)\*\*", r"<b>\1</b>", s)  # bold first, may wrap inner *italics*
    s = re.sub(r"\*([^*]+)\*", r"<em>\1</em>", s)
    s = re.sub(r"\[([^\]]+)\]\((https?://[^)]+)\)", r'<a href="\2">\1</a>', s)
    return s


def render_body(md: str) -> str:
    """Render an entry body (everything after the # title): paragraphs,
    `##`/`###` subheads, and `-` lists. Soft-wrapped lines are joined."""
    body, para = [], []
    in_list = False

    def flush_para():
        nonlocal para
        if para:
            body.append(f"<p>{inline(' '.join(para))}</p>")
            para = []

    def close_list():
        nonlocal in_list
        if in_list:
            body.append("</ul>")
            in_list = False

    # Unwrap soft wraps: an indented continuation folds onto the line
    # above (how Markdown continues a list item or paragraph).
    merged = []
    for raw in md.splitlines():
        if raw[:1] in (" ", "\t") and raw.strip() and merged and merged[-1].strip() \
           and not merged[-1].lstrip().startswith("#"):
            merged[-1] = merged[-1].rstrip() + " " + raw.strip()
        else:
            merged.append(raw)

    for raw in merged:
        line = raw.rstrip()
        if line.startswith("### "):
            flush_para(); close_list()
            body.append(f"<h3>{inline(line[4:])}</h3>")
        elif line.startswith("## "):
            flush_para(); close_list()
            body.append(f"<h3>{inline(line[3:])}</h3>")
        elif line.startswith("- "):
            flush_para()
            if not in_list:
                body.append("<ul>"); in_list = True
            body.append(f"<li>{inline(line[2:])}</li>")
        elif line.strip() == "":
            flush_para(); close_list()
        else:
            close_list()
            para.append(line.strip())
    flush_para(); close_list()
    return "\n".join(body)


def load_entries():
    """Return (date, title, body_html) per dated entry, newest first."""
    entries = []
    for path in SRC.glob("*.md"):
        m = ENTRY_RE.match(path.name)
        if not m:
            continue  # README.md and anything undated is not an entry
        date = m.group(1)
        text = path.read_text()
        title, _, rest = text.partition("\n")
        title = title.lstrip("# ").strip() or path.stem
        entries.append((date, title, render_body(rest)))
    entries.sort(key=lambda e: e[0], reverse=True)
    return entries


def render(entries) -> str:
    out = []
    for date, title, body in entries:
        out.append(
            '<article class="entry">\n'
            f'<h2 class="rel"><span class="v">{inline(title)}</span>'
            f'<span class="d">{inline(date)}</span></h2>\n'
            f"{body}\n</article>")
    return "\n".join(out)


TEMPLATE = """<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Silt build log</title>
<meta name="description" content="How Silt was built and why — the design forks, the reasoning, the dead ends.">
<link rel="canonical" href="https://silthq.com/buildlog.html">
<link rel="preconnect" href="https://fonts.googleapis.com">
<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
<link href="https://fonts.googleapis.com/css2?family=Fraunces:ital,opsz,wght@0,9..144,400;0,9..144,500;1,9..144,400&family=IBM+Plex+Mono:wght@400;500&family=IBM+Plex+Sans:wght@400;500;600&display=swap" rel="stylesheet">
<link rel="stylesheet" href="style.css">
<style>
  .doc article.entry {{ margin:0 0 2.6rem; padding:0 0 2.2rem; border-bottom:1px solid var(--line); }}
  .doc article.entry:last-of-type {{ border-bottom:none; }}
  .doc h2.rel {{ display:flex; align-items:baseline; gap:1rem; flex-wrap:wrap; }}
  .doc h2.rel .v {{ font-family:var(--display); }}
  .doc h2.rel .d {{ font-family:var(--mono); font-size:0.8rem; color:var(--drab); letter-spacing:0.04em; }}
</style>
</head>
<body>
<nav>
  <a href="/" class="wordmark" style="text-decoration:none">Sil<b>t</b></a>
  <span class="spacer"></span>
  <a href="/#how" class="hide-sm">How it works</a>
  <a href="node.html" class="hide-sm">Run a node</a>
  <a href="roadmap.html" class="hide-sm">Roadmap</a>
  <a href="docs.html">Docs</a>
  <a href="https://github.com/nerolabs/silt" class="ghost">GitHub</a>
</nav>
<div class="doc">
  <p class="eyebrow">Build log</p>
  <h1>How it was built</h1>
  <p class="lead">The story and reasoning behind Silt — the design forks, the
  decisions, and the dead ends. Distinct from the <a href="changelog.html">changelog</a>
  (what shipped) and the <a href="roadmap.html">roadmap</a> (what's next):
  this is the <em>why</em>.</p>
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
    if not SRC.is_dir():
        print(f"error: {SRC} not found", file=sys.stderr)
        return 1
    entries = load_entries()
    if not entries:
        print(f"error: no dated entries in {SRC}", file=sys.stderr)
        return 1
    OUT.write_text(TEMPLATE.format(body=render(entries)))
    print(f"wrote {OUT.relative_to(ROOT)} ({len(entries)} entries)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
