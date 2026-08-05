# /archive — frozen history, not for planning

**Read this if you opened a file in here and wondered whether it is current. It is
not.** Everything under `/archive/` is preserved for provenance — it is the record
of how silt's thinking evolved — but it reflects **superseded, finding-by-finding
viewpoints** that competed and were replaced. A fresh reader (researcher, builder,
red team, or user) should ignore it for planning and read the current canon:

- **[`docs/TENETS.md`](../docs/TENETS.md)** — what silt is (the canon; M0 as the
  systemic C1 + C2 claim).
- **[`docs/design/m0.md`](../docs/design/m0.md)** — the single M0 spec: the
  composition thesis, the interlock, the as-built surface map, and the
  composition-level seams a red team should attack.

## Why this archive exists (the reset of 2026-08-05)

For months the project ran a loop — *design a Sybil-proof primitive → red team
breaks it → try a variation* — and accreted one design doc and one review report
per turn. The research capstone (`09-m0-as-composition.md`) showed that loop was
**unwinnable by theorem**: no single primitive can be Sybil-proof under free
minting + no permanent center (Douceur). The escape was a different *claim* — M0's
Sybil corner is held by the **composition** of many parts in tension (C1 no
discount + C2 no quiet capture), not by any one primitive.

That reframe made the per-turn docs **vestigial**: each states a per-primitive
goal that is no longer how "done" is defined. They are moved here so the live tree
carries exactly one current viewpoint. Nothing is deleted; the git history and the
CHANGELOG breadcrumbs still point here.

## What's here, and what replaced it

### `design-history/` — the finding-by-finding M0 design notes
| Archived doc | Superseded by |
|---|---|
| `gate4-m0-mechanism.md` | `docs/design/m0.md` (the M0 spec spine) |
| `m0-sybil-bond.md` (F1/F2/F3 structural pass) | `docs/design/m0-sybil-rebind.md` (bond as-built) + `m0.md` §6 S1 |
| `m0-consensus.md` (F6/F7) | `docs/design/m0.md` §6 S4 |
| `m0-privacy-issuance.md` (F4) | `docs/design/m0.md` §6 S7 + open decision D-PRIV |
| `m0-hardening-strategy.md` (the H1–H6 backlog + surface map) | `docs/design/m0.md` (surface map §6, seams §7, decisions §9) |

### `reviews/` — red-team, acceptance, and audit **reports** (point-in-time)
The *reports* are snapshots of a specific commit and carry outdated verdicts.
`M0-REDTEAM-REPORT.md`, `M0-REDTEAM-VERIFICATION.md`,
`build-vs-intention-2026-08-02.md`, `fresh-eyes-2026-08-02-intention.md`,
`gates-1-3-completeness-2026-08-02.md`.
**The living review *briefs* stayed in `docs/reviews/`** and are rewritten to the
current C1/C2 + seams understanding — a brief is a standing target, a report is a
dated result.

### `process/` — stranded handoff docs
`genesis-handoff.md` (the original inception brief; several code comments still
cite "the HANDOFF"; its own banner flags the fragments that now contradict canon).

## The one live source outside the repo (read-only)

The research memos and red-team field reports that drove this reset live **outside
this repo**, in `silt-reviews/` — treated as a read-only evidence archive. The
builder's adopted perspective from them is written into `/silt/` (here), never
back into `silt-reviews/`.
