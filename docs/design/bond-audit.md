# Bond audit — the as-built wire (stub)

> **This is a short pointer, not the full note.** The original first-cut design note
> (2026-08-01) is archived at
> [`archive/design-history/bond-audit.md`](../../archive/design-history/bond-audit.md).
> For the current bond, read [`m0.md`](m0.md) §6 (S1 — shipped surface) and
> [`m0-sybil-rebind.md`](m0-sybil-rebind.md) (the v3 identity-bound plot as-built).
> The bond is **one axis (D — distinct sealed disk per identity) of M0's systemic
> composition** (C1 + C2), not "the Sybil corner."

`core/node/bondaudit.go` cites this file. The **wire protocol** it refers to — still
current — is:

- **Bond gossip (no new message):** every message carries the sender's `BondRoot`
  (32 B sealed-bond commitment) and `BondSize` alongside the existing capacity/domain
  gossip; peers accumulate `peerBonds[nodeID] = {root, size}`. Zero bond ⇒ never
  challenged.
- **`MsgBondChallenge`** (challenger → target): only a fresh `Nonce` travels; the
  target's `(root, size)` is already known from gossip.
- **`MsgBondReply`** (target → challenger): `Encode(bond.Answer)` — bounded (~80 KiB),
  not an amplification vector; an empty reply scores a fail.
- **Auditor loop:** a validator periodically challenges each peer's bond, verifies via
  `bond.Verify(root, size, nonce, answer)` against the committed root (no ground-truth
  fetch), records into its **own** ledger, and `DecayStale` retires bonds that stop
  answering — so standing is *sustained*, not one-shot.

Everything else in the archived note (the in-RAM "space-lite" first cut, the future-tense
"Gate 4" plan) is **superseded** by the shipped v3 rebind — read `m0-sybil-rebind.md`.
