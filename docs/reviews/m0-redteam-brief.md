# M0 red-team brief — break the trilemma

> **You are an independent adversary.** You have no prior context on this
> project and you are not here to be fair to it. Your job is to **break a
> tenet** — to find a concrete way the system fails a promise it makes about
> itself. A finding is a path, with steps, to an outcome the project says is
> impossible. Vibes are not findings.

## What you get

Only the **public surface**, exactly what a real attacker would have:

- the GitHub repository (clone it, read it, build it, run it, test it);
- the website and any published artifacts.

You get **nothing** from the builders — no design rationale beyond what is in
the repo, no "here's why it's safe." Treat every reassuring comment in the code
as a **claim to falsify**, not a fact.

## The one promise to break (M0)

silt's whole reason to exist is to hold three properties *at once* that every
prior system traded against each other. Read `docs/TENETS.md` (Part 0 + the six
corners + Part VII anti-personas) and `docs/design/gate4-m0-mechanism.md` §1 and
§6 for the exact, falsifiable statements. In short:

1. **Privacy** — a published root cannot be linked to the durable identity that
   authorized it, at any layer an observer can watch (ledger, network, issuance).
2. **Accountability** — content can be taken down, but only per-hash and
   pluralistically; there is **no** identity-level or global switch, and curators
   are themselves accountable.
3. **Sybil-resistance** — standing (consensus weight, denylist weight) cannot be
   cheaply forged, washed, or bought; N identities cost N independent
   space-time bonds, with no coin, stake, or capital lockup.

**A break is a concrete attack that violates one of these.** The sharpest
targets are the three "attacker outcome that must fail" rows in §6.

## Adversary personas — pick ONE per session, go deep

Run each as its own fresh session. Do not dilute — one mindset, exhaustively.

- **The Sybil farm.** Reach consensus or denylist influence for less than N
  independent bonds. Can you amortize one plot across many identities? Share
  disk? Recompute instead of storing? Fake space-time cheaply?
- **The colluding validator minority.** You control a minority of validators.
  Can you deanonymize a publisher by correlating issuance + timing + IP? Capture
  the quorum on a young network before the anchors shed? Censor a specific root?
- **The equivocating / off-head proposer.** Can you get two competing histories
  to both stand? Double-sign without losing standing? Stall liveness?
- **The censor.** Can you make a specific root unfetchable, or force a global /
  identity-level takedown that the design says cannot exist?
- **The network observer.** Given ledger + issuer logs + packet traces, can you
  link a target root to its standing key better than chance within the epoch
  anonymity set?
- **The liar prover.** Claim storage you do not hold and pass an audit; or make
  an honest host fail one.

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
  §6 for "honestly labelled", "recorded hardening", "residual"). Re-deriving one
  of those independently is a *useful confirmation* — report it, marked as such.
  But your prize is a break the builders did **not** already own.
- **Attack the composition, not just the primitives.** The primitives are adopted
  from the literature; the novel, riskiest part is how they are bound together.
- **State your assumptions.** If a break needs a capability (a global observer,
  factoring the VDF modulus, breaking Ed25519), say so — an attack that needs a
  broken primitive is not a break of *this* design.

## What to hand back

A single markdown file. Lead with a **verdict table** (one row per corner:
`DENIED` / `BROKEN` / `UNCERTAIN`), then one section per finding:

```
### <short title>
- corner:        privacy | accountability | sybil
- adversary:     which persona
- severity:      critical | high | medium | low
- breaks denial: which §6 outcome-that-must-fail this violates (or "hardening")
- confidence:    high | medium | low
- attack:        the concrete steps / state / script
- outcome:       what the attacker achieves that the tenets forbid
- suggested fix: optional
- already-known: yes (cite the CHANGELOG/§6 note) | no
```

If you cannot break a corner after a real effort, say so plainly and give the
argument for **why** it held — that is a denial, and it is exactly as valuable
as a break. M0 ships proven or it does not ship.
