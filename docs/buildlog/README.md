# Build log

The story and *reasoning* of building Silt — the design forks, the dead
ends, the decisions and why they went the way they did. This is
deliberately distinct from its two neighbors:

- **[CHANGELOG](../../CHANGELOG.md)** records *what shipped*, release by
  release.
- **[ROADMAP](../../ROADMAP.md)** records *what's next*.
- The build log records *how it was built and why* — the narrative a
  future reader (or employer, or contributor) can inspect to understand
  the thinking, not just the result.

It is strictly about building the **infrastructure**. The resolver layer
("Aslan") is a separate product with its own story; nothing about it
belongs here (see [docs/aslan-boundary.md](../aslan-boundary.md)).

## Format

One Markdown file per entry, named `YYYY-MM-DD-slug.md`. The first line
is an `# H1` title; everything after it is the entry body (standard
Markdown — `##`/`###` subheads, `-` lists, `**bold**`, `` `code` ``,
and `[links](https://…)`).

`scripts/gen_buildlog.py` renders every dated entry, newest first, into
`website/buildlog.html`, styled to match the site — the same
source-of-truth pipeline as the changelog and roadmap. CI regenerates it
and fails if the published page has drifted, so it can never fall out of
sync with these files.

To add an entry: drop a new dated file in this directory, run
`python3 scripts/gen_buildlog.py`, and commit both.
