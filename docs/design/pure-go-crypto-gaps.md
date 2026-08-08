# Pure-Go cryptography gaps (what silt would build if the library existed)

silt follows **B8 — adopt, don't invent**: it never hand-rolls a novel cryptographic
primitive. That rule turns "there is no mature, audited, pure-Go implementation of X"
into a **hard design gate**, not a mere inconvenience — several places where the *ideal*
M0 construction is well-understood in the literature but has **no adoptable pure-Go
implementation in 2026**, so silt ships a sound-but-weaker floor and documents the
blind/full upgrade as a fast-follow.

This file is the **consolidated index** of those gaps. Each is also recorded inline in
the decision it constrains ([`../decisions.md`](../decisions.md)) — this page just puts
them in one place so the cryptographic dependency surface is legible. None of these is a
*silently* assumed-closed seam: every one ships an honest floor and a labelled residual.

> **Why "pure-Go" specifically.** silt is a single static `CGO_ENABLED=0` binary that
> cross-compiles to every target (`build.sh`) — no native toolchain, no C dependency, no
> FFI trust surface. A primitive that only exists as a C library, a `libsodium` binding,
> or an unaudited cgo wrapper is not adoptable under that constraint. "No pure-Go lib"
> below means: nothing that is *both* pure-Go *and* mature/audited enough to trust with
> consensus- or durability-critical code.

---

## 1. Characteristic-2-native polynomial commitment (blind proof-of-repair)

- **What silt wants.** A *plaintext-blind, bandwidth-blind* proof-of-correct-repair: verify
  that a rebuilt erasure-coded shard is the correct codeword coordinate **without seeing the
  bytes and without shipping k survivors**, by checking a public linear relation over
  *committed* values. RS repair is a public linear combination of surviving symbols and
  silt's commitments are linearly homomorphic, so this is exactly the job of a
  linearly-homomorphic polynomial commitment (KZG opening / BFKW subspace signature).
- **Why pure-Go / 2026 can't.** silt's storage math lives in **GF(2⁸)** (characteristic 2).
  There is **no ring homomorphism GF(2⁸) → F_r** (char 2 vs a prime `r`), so a prime-field
  Pedersen/KZG commitment **cannot carry silt's GF(2⁸) RS relation** with a linear
  homomorphic check. Semi-AVID-PR only works because its code lives *inside* the commitment
  field — adopting it faithfully would mean **changing silt's storage format to F_p**. The
  char-2-native answer is a **transparent binary-field polynomial commitment (FRI-Binius)**
  or a lattice-SIS commitment — and **no mature, standalone, pure-Go implementation of either
  existed to adopt** (B8 forbids hand-rolling one).
- **What ships instead (M0).** The `core/repairproof` **Merkle-recompute floor**:
  reconstruct the target shard from k survivors and check it is byte-identical to the
  manifest-committed shard id. Sound, pure-Go, publicly checkable, content-blind — but **not
  bandwidth-blind** (the verifier fetches k survivors). An explicit M0 non-goal, not a broken
  claim.
- **What it blocks.** Bandwidth-free durability auditing at scale.
- **Revisit when.** A pure-Go char-2-native commitment library matures, **or** silt chooses
  an F_p storage re-encode. Fast-follow, not M0.
- **Recorded in:** [`decisions.md` D-S7](../decisions.md) (the B8/no-trusted-setup bullet);
  deep-dive [`h7-proof-of-repair.md`](h7-proof-of-repair.md) §3, §7, §13.

## 2. Threshold decryption / distributed key generation (t-of-n)

- **What silt wants.** A validator **quorum as a threshold-distributed trusted third party**:
  a value encrypted to the quorum's shared key that can be decrypted **only** if ≥ t
  validators cooperate — no single party, no central escrow. Two consumers want it: the P2
  optimistic fair-exchange **dispute-resolution** half (a TTP affidavit), and **accountable
  disclosure** (D-DISCLOSURE) if it is ever in scope.
- **Why pure-Go / 2026 can't.** Threshold decryption needs a **DKG** (distributed key
  generation) plus threshold ElGamal/Paillier decryption. There is **no adoptable, audited,
  pure-Go implementation** — and adopting one would import a **large new cryptographic trust
  surface** (DKG is notoriously subtle) into consensus-critical code, which B8 + the
  unaudited-crypto risk both counsel against for M0.
- **What ships instead (M0).** The P2 **abort-safety floor** only
  (`core/demand/fairexchange.go`): an `ExchangeCommitment` pre-release promise with two locked
  invariants (an aborted exchange never consumes the token; a commitment can't redeem as
  demand). The *dispute-resolution* half is gated. Demand is a **neutral** observable, so an
  unresolved abort only undercounts a neutral quantity — never moves standing.
- **What it blocks.** Cryptographic fair-exchange dispute resolution; accountable disclosure.
- **Revisit when.** An audited pure-Go threshold-decryption + DKG stack exists.
  `ExchangeCommitment` is the exact seam the future resolver plugs into.
- **Recorded in:** [`decisions.md` D-DEMAND](../decisions.md) (P2 dispute-resolution) and
  [`decisions.md` D-DISCLOSURE](../decisions.md).

## 3. Verifiable encryption (Camenisch–Shoup)

- **What silt wants.** Encrypt the exchange commitment to the quorum **and prove, without
  decrypting, that the ciphertext contains the correct value** — so a fair-exchange TTP can
  adjudicate a dispute over a commitment it can verify but not open unilaterally.
- **Why pure-Go / 2026 can't.** Camenisch–Shoup verifiable encryption has **no mature pure-Go
  implementation**, and it composes with the threshold-decryption gap above (they ship
  together or not at all).
- **What ships instead (M0).** Nothing for this leg — it rides on the same gated
  dispute-resolution path as §2 (low stakes under demand-neutrality).
- **Revisit when.** Together with §2's threshold stack.
- **Recorded in:** [`decisions.md` D-DEMAND](../decisions.md).

## 4. Zero-knowledge threshold predicate (provable non-global takedown metric)

- **What silt wants.** For the takedown-transparency story, prove a **negative about
  distributed state without revealing who** — e.g. "**≥ t distinct-domain, PoR-fresh replicas
  of this root are gone**" (a *survivor Nakamoto coefficient* falling below the RS recovery
  threshold), as a ZK **threshold predicate** over the replica set.
- **Why pure-Go / 2026 can't.** A general ZK predicate over a distributed, freshness-attested
  replica set needs a ZK proving stack silt has **no adoptable pure-Go implementation** for
  (and B8 forbids rolling one).
- **What ships instead (M0).** The **CT-style append-only transparency log** (`core/translog`,
  H9): every honored revoke/unrevoke is committed with inclusion + consistency proofs, so a
  takedown is *provably recorded* and history can't be silently rewritten. This delivers
  **provable non-globality by transparency**, not by ZK predicate — the weaker, shipped form
  of D-TAKEDOWN.
- **What it blocks.** A ZK, privacy-preserving survivor-count predicate (the stronger metric).
- **Revisit when.** A trustworthy pure-Go ZK stack is adoptable. Post-M0.
- **Recorded in:** [`decisions.md` D-TAKEDOWN](../decisions.md).

## 5. Continuous VDF chained to the bond identity (a real time-acquisition axis)

- **What silt wants.** If the **T (time) axis** of `C_honest ∝ D×A×T×B` is ever to price
  standing *acquisition* by elapsed time, the only *sound* form is a **continuous VDF chained
  to the bond identity** (seed = f(bondRoot, nodeKey), run continuously since the bond was
  created) so that exhibiting "T units of standing" requires an unbroken chain of sequential
  work that **could not have been started before the bond existed** — non-pre-farmable, unlike
  a `firstSeenTick` age counter (the coin-age anti-pattern).
- **Why pure-Go / 2026 can't (and why it's deferred regardless).** silt already ships a
  **per-challenge Wesolowski VDF** (`core/vdf`), which proves ~milliseconds of sequential work
  on *one* input — it does **not** measure identity-longevity. A *continuous, identity-chained*
  VDF is a different construction: an always-on per-bond sequential process with real liveness
  and hardware-heterogeneity costs, and a residual "fastest-squarer measures time" caveat. It
  is **out of scope for M0** on its own merits (marginal Sybil-resistance over an already
  non-substitutable D axis), independent of library availability.
- **What ships instead (M0).** **T = retention only** — `DecayStale` + `BondMaxAge` force
  standing to be *continuously re-proven*; acquisition is priced by **D alone**. Stated
  honestly (no acquisition-time accrual) after the red-team F-2 relabel.
- **Revisit when.** M1+, only if a real acquisition-time factor is ever wanted.
- **Recorded in:** research memo-F2 §3; the T-axis relabel in `m0.md` §3/§4, `TENETS.md`,
  and `core/credit/credit.go`.

---

## Summary

| # | Wanted primitive | Blocks | M0 ships instead | Horizon |
|---|---|---|---|---|
| 1 | Char-2-native polynomial commitment (FRI-Binius / lattice-SIS) | bandwidth-blind proof-of-repair | Merkle-recompute floor (`core/repairproof`) | fast-follow |
| 2 | Threshold decryption + DKG (t-of-n) | fair-exchange dispute resolution; accountable disclosure | abort-safety floor only (`ExchangeCommitment`) | post-M0 |
| 3 | Verifiable encryption (Camenisch–Shoup) | verifiable TTP affidavit | — (rides on §2) | post-M0 |
| 4 | ZK threshold predicate | survivor-Nakamoto takedown metric | CT-style transparency log (`core/translog`) | post-M0 |
| 5 | Continuous identity-chained VDF | a real T-acquisition axis | T = retention only (decay/TTL) | M1+ (also deferred on merit) |

**The through-line:** each is a place where B8 ("adopt, don't invent") + the pure-Go / no-cgo
constraint + the 2026 library landscape force a floor. silt ships the floor, labels the
residual, and names the upgrade — which is the project's honesty rule applied to its own
cryptographic dependencies. Not one of these is load-bearing for the M0 safety claims (C1/C2
and the demand firewall); they bound *reach* (blind auditing, dispute resolution, ZK metrics,
a time axis), not the core denials.
