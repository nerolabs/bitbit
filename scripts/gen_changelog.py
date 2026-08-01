#!/usr/bin/env python3
"""Render CHANGELOG.md into website/changelog.html, styled to match the
site. CHANGELOG.md is the single source of truth; CI runs this before
every deploy so the published page can never drift from the log.

Deliberately dependency-free (stdlib only) so it runs on any CI runner
without an install step."""
import html
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
SRC = ROOT / "CHANGELOG.md"
OUT = ROOT / "website" / "changelog.html"


GH_BLOB = "https://github.com/nerolabs/silt/blob/main/"


def _href(url: str) -> str:
    """Keep absolute / anchor / root links as-is; map repo-relative paths
    (docs/…, ROADMAP.md) to their GitHub blob URL so they resolve on the
    published site and pass the internal link-check, which skips externals."""
    if url.startswith(("http://", "https://", "//", "#", "mailto:", "/")):
        return url
    return GH_BLOB + url


def inline(s: str) -> str:
    """Minimal inline markdown → HTML: escape, then code/bold/links."""
    s = html.escape(s)
    s = re.sub(r"`([^`]+)`", r'<span class="mono">\1</span>', s)
    s = re.sub(r"\*\*(.+?)\*\*", r"<b>\1</b>", s)  # bold first, may wrap inner *italics*
    s = re.sub(r"\*([^*]+)\*", r"<em>\1</em>", s)  # single-asterisk italics
    s = re.sub(r"\[([^\]]+)\]\(([^)]+)\)",
               lambda m: f'<a href="{_href(m.group(2))}">{m.group(1)}</a>', s)
    return s


def render(md: str) -> str:
    body, para, quote = [], [], []
    in_list, intro_done, lead_used = False, False, False

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

    def flush_quote():
        nonlocal quote
        if not quote:
            return
        body.append(f"<blockquote>{inline(' '.join(quote))}</blockquote>")
        quote = []

    def close_list():
        nonlocal in_list
        if in_list:
            body.append("</ul>"); in_list = False

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
        if line.startswith("> ") or line == ">":
            flush_para(); close_list()
            quote.append(line[2:] if len(line) > 1 else "")
        elif line.startswith("## "):
            flush_para(); flush_quote(); close_list(); intro_done = True
            m = re.match(r"^##\s+\[?([^\]\s]+)\]?\s*(?:—|-)?\s*(.*)$", line)
            ver = m.group(1) if m else line[3:]
            date = (m.group(2) or "").strip() if m else ""
            tag = "unreleased" if ver.lower() == "unreleased" else "released"
            body.append(
                f'<h2 class="rel {tag}"><span class="v">{inline(ver)}</span>'
                + (f'<span class="d">{inline(date)}</span>' if date else "")
                + "</h2>")
        elif line.startswith("### "):
            flush_para(); flush_quote(); close_list()
            body.append(f"<h3>{inline(line[4:])}</h3>")
        elif line.startswith("- "):
            flush_para(); flush_quote()
            if not in_list:
                body.append("<ul>"); in_list = True
            body.append(f"<li>{inline(line[2:])}</li>")
        elif line.strip() == "":
            flush_para(); flush_quote(); close_list()
        else:
            flush_quote(); close_list()
            para.append(line.strip())
    flush_para(); flush_quote(); close_list()
    return "\n".join(body)


TEMPLATE = """<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Silt changelog</title>
<meta name="description" content="Notable changes to Silt, release by release.">
<link rel="canonical" href="https://silthq.com/changelog.html">
<link rel="preconnect" href="https://fonts.googleapis.com">
<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
<link href="https://fonts.googleapis.com/css2?family=Fraunces:ital,opsz,wght@0,9..144,400;0,9..144,500;1,9..144,400&family=IBM+Plex+Mono:wght@400;500&family=IBM+Plex+Sans:wght@400;500;600&display=swap" rel="stylesheet">
<link rel="stylesheet" href="style.css">
<style>
  .doc h2.rel {{ display:flex; align-items:baseline; gap:1rem; flex-wrap:wrap; }}
  .doc h2.rel .v {{ font-family:var(--display); }}
  .doc h2.rel .d {{ font-family:var(--mono); font-size:0.8rem; color:var(--drab); letter-spacing:0.04em; }}
  .doc h2.rel.unreleased .v::after {{ content:" ·"; color:var(--ochre); }}
  .doc h2.rel.unreleased {{ color:var(--ochre); }}
  .doc blockquote {{ margin:1.4rem 0; padding:0.1rem 0 0.1rem 1.1rem; border-left:2px solid var(--ochre); color:var(--drab); }}
  .doc blockquote b {{ color:var(--bone); }}
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
  <p class="eyebrow">Changelog</p>
  <h1>What's changed</h1>
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
