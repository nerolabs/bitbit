#!/usr/bin/env python3
"""Render CHANGELOG.md into website/changelog.html, styled to match the site.

CHANGELOG.md is the single source of truth; CI runs this before every deploy so
the published page can never drift from the log. Dependency-free (stdlib only).

The page is organised as a CHRONOLOGICAL SPINE, not a wall of category headers:
the Unreleased section's entries are regrouped by their per-entry date (newest
first), each rendered as a card carrying a category chip. Released versions keep
their version+date header. This is why every Unreleased entry needs an inline
`(YYYY-MM-DD…)` date — the grouping key.
"""
import html
import re
import sys
from datetime import datetime
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
SRC = ROOT / "CHANGELOG.md"
OUT = ROOT / "website" / "changelog.html"
GH_BLOB = "https://github.com/nerolabs/silt/blob/main/"

CAT_CLASS = {"Added": "added", "Changed": "changed", "Fixed": "fixed",
             "Security": "security", "Docs": "docs", "Removed": "removed",
             "Deprecated": "deprecated"}


def _href(url: str) -> str:
    if url.startswith(("http://", "https://", "//", "#", "mailto:", "/")):
        return url
    return GH_BLOB + url


def inline(s: str) -> str:
    """Minimal inline markdown → HTML: escape, then code/bold/italics/links."""
    s = html.escape(s)
    s = s.replace("\\`", "`")  # unescape stray \`…\` in the source so it renders as code
    s = re.sub(r"`([^`]+)`", r'<span class="mono">\1</span>', s)
    s = re.sub(r"\*\*(.+?)\*\*", r"<b>\1</b>", s)
    s = re.sub(r"\*([^*]+)\*", r"<em>\1</em>", s)
    s = re.sub(r"\[([^\]]+)\]\(([^)]+)\)",
               lambda m: f'<a href="{_href(m.group(2))}">{m.group(1)}</a>', s)
    return s


# ── Parsing: CHANGELOG.md → [Version{name, date, entries[]}] ────────────────────
class Entry:
    __slots__ = ("category", "date", "lead", "subs")

    def __init__(self, category, date, lead, subs):
        self.category, self.date, self.lead, self.subs = category, date, lead, subs


class Version:
    __slots__ = ("name", "date", "entries")

    def __init__(self, name, date):
        self.name, self.date, self.entries = name, date, []


DATE_RE = re.compile(r"\(?(20\d\d-\d\d-\d\d)")


def _entry_from_block(block_lines, category):
    """A block is one top-level `- **…**` bullet plus its continuation lines
    (soft-wraps fold into the lead; nested `- ` bullets become sub-items)."""
    segs = []
    for ln in block_lines:
        s = ln.strip()
        if not s:
            continue
        is_bullet = s.startswith("- ")
        if is_bullet and segs:
            segs.append(s[2:])
        elif not segs:
            segs.append(s[2:] if is_bullet else s)
        else:
            segs[-1] += " " + s
    if not segs:
        return None
    lead, subs = segs[0], segs[1:]
    m = DATE_RE.search(lead)
    date = m.group(1) if m else None
    # strip the date token from the display text (it becomes the group header),
    # keeping any issue link that shared the parenthetical.
    lead = re.sub(r"\(20\d\d-\d\d-\d\d,\s*", "(", lead)   # (date, #47) -> (#47)
    lead = re.sub(r"\s*\(20\d\d-\d\d-\d\d\)", "", lead)   # standalone (date) -> gone
    lead = re.sub(r"\(\s*\)", "", lead).strip()
    return Entry(category, date, lead, subs)


def parse(md: str):
    versions = []
    cur = None
    category = None
    block = None

    def flush_block():
        nonlocal block
        if block and cur is not None:
            e = _entry_from_block(block, category)
            if e:
                cur.entries.append(e)
        block = None

    for raw in md.splitlines():
        line = raw.rstrip()
        if re.match(r"^\[[^\]]+\]:\s+https?://", line) or line.startswith("# "):
            continue
        if line.startswith("## "):
            flush_block()
            m = re.match(r"^##\s+\[?([^\]\s]+)\]?\s*(?:—|-)?\s*(.*)$", line)
            name = m.group(1) if m else line[3:]
            date = (m.group(2) or "").strip() if m else ""
            cur = Version(name, date)
            versions.append(cur)
            category = None
        elif line.startswith("### "):
            flush_block()
            category = line[4:].strip()
        elif re.match(r"^- ", line):          # a new top-level entry
            flush_block()
            block = [line]
        elif block is not None and (raw[:1] in (" ", "\t")) and line.strip():
            block.append(line)                # continuation / sub-bullet
        elif line.strip() == "":
            # blank: end an entry block, but keep collecting a version's prose intro
            flush_block()
    flush_block()
    return versions


# ── Rendering ───────────────────────────────────────────────────────────────
def fmt_date(iso: str) -> str:
    try:
        return datetime.strptime(iso, "%Y-%m-%d").strftime("%B ") + \
            str(int(iso[8:10])) + ", " + iso[:4]
    except ValueError:
        return iso


def render_entry(e: Entry) -> str:
    cls = CAT_CLASS.get(e.category, "other")
    chip = f'<span class="chip {cls}">{html.escape(e.category or "Note")}</span>'
    body = f'<div class="body"><p>{inline(e.lead)}</p>'
    if e.subs:
        body += "<ul>" + "".join(f"<li>{inline(s)}</li>" for s in e.subs) + "</ul>"
    body += "</div>"
    return f'<article class="entry {cls}">{chip}{body}</article>'


def render_unreleased(v: Version) -> str:
    out = ['<section class="unreleased">',
           '<div class="banner"><span class="dot"></span><div>'
           '<b>Unreleased</b> — merged to <span class="mono">main</span>, not yet in a '
           'tagged release. Newest first.</div></div>']
    # group entries by date, newest first; undated (shouldn't happen) sink to the end
    dates = sorted({e.date for e in v.entries if e.date}, reverse=True)
    undated = [e for e in v.entries if not e.date]
    for d in dates:
        out.append(f'<h2 class="day"><time datetime="{d}">{fmt_date(d)}</time></h2>')
        out.append('<div class="entries">')
        out += [render_entry(e) for e in v.entries if e.date == d]
        out.append('</div>')
    if undated:
        out.append('<h2 class="day">Undated</h2><div class="entries">')
        out += [render_entry(e) for e in undated]
        out.append('</div>')
    out.append('</section>')
    return "\n".join(out)


def render_release(v: Version) -> str:
    head = (f'<h2 class="rel"><span class="v">{inline(v.name)}</span>'
            + (f'<span class="d">{inline(v.date)}</span>' if v.date else "") + '</h2>')
    return (f'<section class="release">{head}<div class="entries">'
            + "".join(render_entry(e) for e in v.entries) + '</div></section>')


def render(md: str) -> str:
    parts = []
    for v in parse(md):
        if v.name.lower() == "unreleased":
            parts.append(render_unreleased(v))
        else:
            parts.append(render_release(v))
    return "\n".join(parts)


TEMPLATE = """<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Silt changelog</title>
<meta name="description" content="Notable changes to Silt, newest first.">
<link rel="canonical" href="https://silthq.com/changelog.html">
<link rel="preconnect" href="https://fonts.googleapis.com">
<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
<link href="https://fonts.googleapis.com/css2?family=Fraunces:ital,opsz,wght@0,9..144,400;0,9..144,500;1,9..144,400&family=IBM+Plex+Mono:wght@400;500&family=IBM+Plex+Sans:wght@400;500;600&display=swap" rel="stylesheet">
<link rel="stylesheet" href="style.css">
<style>
  /* the changelog is a scannable list, not prose — give it 2× the doc width */
  .doc {{ max-width:1680px; }}
  .doc > p, .doc .entry p {{ max-width:none; }}
  .doc .day {{ font-family:var(--mono); font-size:0.82rem; letter-spacing:0.06em;
    text-transform:uppercase; color:var(--drab); margin:2.4rem 0 0.9rem;
    padding-bottom:0.4rem; border-bottom:1px solid rgba(255,255,255,0.08); }}
  .doc .banner {{ display:flex; gap:0.7rem; align-items:flex-start; margin:0.5rem 0 1rem;
    padding:0.8rem 1rem; border:1px solid var(--ochre); border-radius:10px;
    background:rgba(200,140,60,0.06); color:var(--bone); font-size:0.92rem; }}
  .doc .banner .dot {{ width:0.55rem; height:0.55rem; border-radius:50%;
    background:var(--ochre); margin-top:0.42rem; flex:none; box-shadow:0 0 0 4px rgba(200,140,60,0.15); }}
  .doc .entries {{ display:flex; flex-direction:column; gap:0.7rem; }}
  .doc .entry {{ display:grid; grid-template-columns:6.2rem 1fr; gap:0.9rem;
    padding:0.85rem 1rem; border:1px solid rgba(255,255,255,0.07); border-radius:10px;
    background:rgba(255,255,255,0.015); }}
  .doc .entry:hover {{ border-color:rgba(255,255,255,0.14); }}
  .doc .entry .body p {{ margin:0; }}
  .doc .entry .body ul {{ margin:0.5rem 0 0; padding-left:1.1rem; }}
  .doc .entry .body li {{ margin:0.2rem 0; }}
  .doc .chip {{ align-self:start; font-family:var(--mono); font-size:0.68rem;
    letter-spacing:0.05em; text-transform:uppercase; padding:0.22rem 0.5rem;
    border-radius:999px; border:1px solid currentColor; white-space:nowrap; text-align:center; }}
  .doc .chip.added {{ color:#7fb98a; }} .doc .chip.fixed {{ color:#d0a44c; }}
  .doc .chip.changed {{ color:#6fa8c7; }} .doc .chip.security {{ color:#c77b6b; }}
  .doc .chip.docs, .doc .chip.other {{ color:var(--drab); }}
  .doc .entry.security {{ background:rgba(199,123,107,0.05); }}
  .doc h2.rel {{ display:flex; align-items:baseline; gap:0.9rem; flex-wrap:wrap;
    margin:2.6rem 0 1rem; padding-top:1.4rem; border-top:1px solid rgba(255,255,255,0.08); }}
  .doc h2.rel .v {{ font-family:var(--display); font-size:1.5rem; }}
  .doc h2.rel .d {{ font-family:var(--mono); font-size:0.8rem; color:var(--drab); letter-spacing:0.04em; }}
  @media (max-width:560px) {{ .doc .entry {{ grid-template-columns:1fr; gap:0.5rem; }} }}
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
  <p class="lead">Every notable change to Silt, newest first. The source of truth is
  <a href="{gh}CHANGELOG.md">CHANGELOG.md</a>; this page is generated from it.</p>
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
    OUT.write_text(TEMPLATE.format(body=render(SRC.read_text()), gh=GH_BLOB))
    print(f"wrote {OUT.relative_to(ROOT)}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
