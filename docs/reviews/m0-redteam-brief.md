# M0 red-team brief — break the systemic claim

> **You are an independent adversary.** You have no prior context on this
> project and you are not here to be fair to it. Your job is to **break the
> systemic M0 claim** — to find a concrete way the composition fails a promise
> it makes about itself. The unit of test is **not** "is this primitive
> Sybil-proof?" — a lone primitive failing that test is *expected* (Douceur),
> not an M0 failure. The unit of test is the systemic claim (C1 + C2) and the
> seams that hold it together. A finding is a path, with steps, to an outcome
> the project says is impossible. Vibes are not findings.

## What you get

Only the **public surface**, exactly what a real attacker would have — start
from these two, everything else is reachable from them:

- **the repository — <https://github.com/nerolabs/silt>** — clone it, read it,
  build it (`go build ./cmd/silt`), run it, run its tests;
- **the website — <https://silthq.com>** — and anything it links.

You get **nothing** from the builders — no design rationale beyond what is in
the repo, no "here's why it's safe." Treat every reassuring comment in the code
as a **claim to falsify**, not a fact.

**OFF-LIMITS — internal review artifacts (do NOT read; they are a graded answer
key, not the public product).** This is a blind pass: you find the breaks
yourself. Therefore:

- **Do not open anything under `docs/reviews/` except this brief.** That directory
  holds QA and audit artifacts from PRIOR internal reviews — earlier findings
  reports, fix-verification guides, acceptance briefs. Reading them would hand you
  a numbered list of what to look for and what was already patched, which defeats
  an independent pass.
- **Ignore the "red-team verdict / builder response" status banners** at the top
  of the design docs (e.g. the verdict table in `docs/design/m0.md` §6) and any
  CHANGELOG entry that references prior "F1–F7" findings. Read the design docs for
  the *mechanism and the falsifiable claims* (below), not the grade sheet.

Everything you need is in the **code** and the **genuine product docs** — the
README, `docs/` outside `reviews/`, and the website's user-facing pages. A real
attacker reads code comments; you may too — but they are claims to break, and you
do the finding.

## The one promise to break (M0)

silt does **not** claim any single primitive is Sybil-proof — that claim is false
by theorem (Douceur), and breaking it is not new information. What silt claims is
**systemic**: read `docs/TENETS.md` (Part 0) and `docs/design/m0.md` §3 (the
claim) and §7 (the seams) for the exact, falsifiable statements. In short:

> the systemic M0 claim — **C1 (no discount):** a strategy earning a fraction *q*
> of consensus standing must pay ≈ *q · C_honest* — **and C2 (no quiet capture):**
> the min colluding operator set to reach quorum capture stays above *k* — **held
> in tension.**

where `C_honest = disk × address-diversity × time × served-demand`, a **product
of orthogonal, non-substitutable factors** (you cannot trade surplus disk for
missing address diversity, or buy your way past elapsed time). C1 is the fold:
**forging standing ≡ honestly providing that much real, served, sustained,
address-diverse storage.** C2 is the residue C1 cannot touch (an honest whale
concentrating real standing).

> **Shipped subset — the B (served-demand) axis is NEUTRAL.** `D×A×T×B` is the
> *target* composition; in the shipped M0 subset served-demand confers **zero**
> consensus standing — it is a neutral observable, severed from standing by the
> γ→1/N firewall (`m0.md` §3/§7, `#181`/`#182`), so today standing ≈ `D×A×T`. The
> demand mechanism (`core/demand`) is built and blind/unlinkable, but a receipt —
> forged, self-dealt, or wash — buys **nothing** that matters to consensus. So your
> demand/wash attacks (personas below) are a test of that **firewall**: find any path
> by which served/witnessed demand moves consensus standing. "Demand doesn't move
> standing" is the design's claim — break it, or confirm it held.

**A break is one of three things:** (i) a strategy that earns
consensus-controlling standing for **less than *q · C_honest***; (ii) a strategy
that **concentrates past capture** (violates C2); or (iii) a strategy that
**breaks a §7 seam**. The sharpest targets are the seams in `m0.md` §7 — that is
where composed systems break.

## Adversary personas — pick ONE per session, go deep

Run each as its own fresh session. Do not dilute — one mindset, exhaustively.
**Stage the attack along the axes** (see "How to stage" below): each persona is
either a per-axis reuse attack (expected to be denied by its part), a cross-axis
**seam** attack (the real prize, §7), a concentration/shed attack against C2, or a
cold-start capture against the young regime.

- **The Sybil farm (per-axis reuse → C1).** Reach consensus or denylist influence
  for **less than *q · C_honest***. Can you amortize one plot across many
  identities (D axis)? Share disk? Recompute instead of storing? Mint many keys
  near one target key without address diversity (A axis)? Mint standing instantly
  without elapsed time (T axis)? Self-deal your own junk as served demand (B
  axis)? Each is denied by one part — your prize is a shortcut on one axis that
  is **not** caught by another axis's check.
- **The colluding validator minority (C2 + seams).** You control a minority of
  validators. Can you deanonymize a publisher by correlating issuance + timing +
  IP (the privacy↔attribution seam, §7.4)? Capture the quorum on a young network
  before the anchors shed (cold-start, §7.2)? Concentrate real standing past the
  capture threshold while evading operator-clustering (C2 / §7.5)? Censor a
  specific root?
- **The equivocating / off-head proposer (seam / liveness).** Can you get two
  competing histories to both stand? Double-sign without losing standing? Grief
  liveness under the fused rules — withhold demand receipts, refuse to attest
  (§7.7)?
- **The censor.** Can you make a specific root unfetchable, or force a global /
  identity-level takedown that the design says cannot exist?
- **The network observer (privacy↔attribution seam).** Given ledger + issuer logs
  + packet traces, can you link a target root to its standing key better than
  chance within the epoch anonymity set, or mint demand receipts without served
  bytes (§7.4)?
- **The wash-trader (C1 anti-wash, §7.3).** Manufacture fabricated demand for your
  own stored content to inflate served-demand standing. Can you make wash demand
  outweigh real demand at realistic parameters?
- **The liar prover.** Claim storage you do not hold and pass an audit; or make
  an honest host fail one.

## The attack surface

[`docs/user-seam.md`](../user-seam.md) is the complete set of operations silt
exposes, by role — your attack-surface map. [`docs/test-topologies.md`](../test-topologies.md)
is how to stand up the network shapes you'll need (a validator swarm, real
NATs, a partition). Subvert any operation in the seam to violate a promise
below.

**Don't burn your budget on setup.** The [`examples/`](../examples/README.md)
playbooks boot a validator swarm in one command, and two helpers instrument the
attack: `silt id` prints a peer's NodeID without launching it (fill
`-attesters`/`-bootstrap` up front), and `silt chain-status -store DIR` prints a
replica's head height + head hash — how you tell whether two histories both
stand. Several sims are **pre-built adversary models with the mechanism's denial
already wired in** — extend these rather than starting cold: `silt sim run
bondstanding` (a Sybil quorum is denied), `consensus` (a zero-reputation
proposer is refused), `audit` (a liar-prover is caught), `takedown` (a denied
root is blocked per-operator). `sim/*_test.go` and `go test ./sim/ -run <name>
-v` show how each is driven.

## Where the mechanism lives

- Sybil bond: `core/bond` (space-hard plot), `core/vdf` (the time), the plot's
  identity binding, `core/credit` (standing, the root-owner dedup, slashing).
- Retrieval audit: `core/por`, `core/node/por.go`.
- Consensus: `core/chain` (fork-choice `Reconcile`, `Equivocation`), the
  attest/commit flow in `core/node/chainrole.go`.
- Privacy: `core/blindtoken`, `core/publishtoken`, the token flow in
  `core/node/tokenrole.go`, the chain's Publisher-less default.

## Rules of engagement

- **Concrete or it didn't happen.** Give the steps, the state, and the outcome.
  Ideally a failing test, a script, or a precise sequence that a builder can
  reproduce.
- **The repo already documents some known limits** (search the CHANGELOG and
  `m0.md` §6/§7 for "honestly labelled", "held", "residual"). Re-deriving one
  of those independently is a *useful confirmation* — report it, marked as such.
  But your prize is a break the builders did **not** already own.
- **Attack the composition, not the isolated primitives.** The primitives are
  adopted from the literature and a lone one failing a standalone Sybil-proof test
  is *expected* (Douceur); the novel, riskiest part is how they are bound
  together. The break that matters is a cheap shortcut on one axis that is **not**
  caught by another axis's check, a capture that concentrates past C2, or a §7
  seam.
- **How to stage the attack (per `m0.md` §8):** (i) per-axis reuse attacks (one
  disk → many ids; one IP → many nodes; instant standing; self-dealt content) —
  each should be denied by its part; (ii) cross-axis **seam** attacks (§7) — the
  real prize; (iii) concentration/shed attacks against C2; (iv) cold-start capture
  against the young regime.
- **State your assumptions.** If a break needs a capability (a global observer,
  factoring the VDF modulus, breaking Ed25519), say so — an attack that needs a
  broken primitive is not a break of *this* design.
- **Scriptable → we'll keep it.** If your attack — or your denial — runs as a
  script or a `go test`, hand it over; strong ones become permanent adversarial
  regression tests (the acceptance pass's reproduction scripts became
  [`examples/`](../examples/README.md)). A denial carries most when it's backed by
  a *failing* attack — the mechanism visibly refusing, the way `bondstanding` /
  `consensus` / `audit` already encode their denials — not by prose alone.

## What to hand back

A single markdown file. Lead with a **verdict table** — one row per unit of test:
the two claim halves (**C1**, **C2**) and each of the **seven §7 seams** —
marked `HELD-IN-TENSION` / `BROKEN` / `SILENTLY-ASSUMED-CLOSED` / `UNCERTAIN`.
Then one section per finding:

```
### <short title>
- unit:          C1 | C2 | seam-1..7 (name it)
- adversary:     which persona
- severity:      critical | high | medium | low
- breaks:        C1 (cheaper than q·C_honest) | C2 (concentration past capture) |
                 which §7 seam | "hardening"
- confidence:    high | medium | low
- attack:        the concrete steps / state / script
- outcome:       what the attacker achieves that the claim forbids
- suggested fix: optional
- already-known: yes (cite the CHANGELOG / m0.md §6-§7 note) | no
```

**Expect "held," not "closed," on some seams — and the spec says so.** A seam
*held in tension* (bounded cost, documented residual — see §7: C2, cold-start,
and the real-vs-wash-demand ratio are named as most likely held-not-closed) is a
**PASS** under M0's own definition; the failure mode is a seam **silently assumed
closed**. If you cannot break a unit after a real effort, say so plainly and give
the argument for **why** it held (or why it holds only in tension) — that is a
denial, and it is exactly as valuable as a break. M0 ships held-at-stated-
parameters or it does not ship.
