# Primitive-availability gaps (what silt would adopt if a trustworthy impl existed)

> **Framing corrected 2026-08-08** per the research team's response
> (`silt-reviews/.../pure-go-crypto-gaps-RESEARCH-RESPONSE.md`). The earlier version filed
> **four different binding constraints under one "pure-Go" label**, which wrongly invited
> "maybe drop pure-Go" as a remedy — when dropping pure-Go would unblock almost none of these.
> The true constraint is **primitive maturity + construction design**, not the language.
> *(Renamed from `pure-go-crypto-gaps.md` — the old name undersold the content: only one or
> two of these are actually pure-Go library gaps.)*

silt follows **B8 — adopt, don't invent**: it never hand-rolls a novel cryptographic
primitive. It also ships as a single static `CGO_ENABLED=0` binary that cross-compiles
everywhere (`build.sh`) — load-bearing for *run-a-node-anywhere* and for a no-FFI-trust-surface
security story. Together these turn "there is no mature, audited implementation we can adopt
without cgo" into a **hard design gate** for a handful of *ideal* M0 constructions. This file is
the consolidated index of those gaps; each is also recorded inline in the decision it constrains
([`../decisions.md`](../decisions.md)). None is a *silently* assumed-closed seam — every one ships
an honest floor and a labelled residual.

**The key correction — name the TRUE binding constraint per gap:**

| # | Wanted primitive | True binding constraint | Blocks | M0 ships instead |
|---|---|---|---|---|
| 1 | Char-2 / binary-field poly commitment (FRI-Binius / lattice-SIS) | **Immature *everywhere*** (research-grade Rust, archived, unaudited) — B8-blocked in any language | bandwidth-blind proof-of-repair | Merkle-recompute floor (`core/repairproof`) |
| 2 | Threshold **decryption** (t-of-n) | **Library gap — but only the *decryption* slice.** DKG + threshold *signing* are mature pure-Go | fair-exchange dispute resolution; accountable disclosure | abort-safety floor (`ExchangeCommitment`) |
| 3 | Verifiable encryption (Camenisch–Shoup) | **Niche everywhere — and maybe not even required** (mainstream TSS uses Paillier range proofs) | verifiable TTP affidavit | — (rides on §2) |
| 4 | ZK threshold predicate | **Trusted-setup aversion + circuit-soundness design**, NOT library absence (gnark is mature pure-Go) | survivor-Nakamoto takedown metric | CT transparency log (`core/translog`) |
| 5 | Continuous identity-chained VDF | **Deferred on merit** (marginal value) + no impl in any language + theoretically contested | a real T-acquisition axis | T = retention only (decay/TTL) |

So of five "gaps": **one is immature-everywhere** (1), **one is deferred-on-merit** (5), **two were
overstated** because a mature pure-Go option already exists (2 partial, 4), and only threshold
*decryption* (2) plus verifiable encryption (3) are genuinely "the impl doesn't exist and we wish it
did" — and (3) may not be a real requirement. **Do not reopen the `CGO_ENABLED=0` decision on account
of this list: it would unblock almost none of it and cost the deployment + attack-surface properties
that are genuinely valuable.**

---

## 1. Characteristic-2-native polynomial commitment (blind proof-of-repair) — *wait-and-floor (correct)*

- **What silt wants.** A *plaintext-blind, bandwidth-blind* proof-of-correct-repair: verify a rebuilt
  erasure shard is the correct codeword coordinate **without seeing the bytes and without shipping k
  survivors**, via a public linear relation over *committed* values (a linearly-homomorphic polynomial
  commitment).
- **True binding constraint: immature everywhere, B8-blocked in any language.** silt's storage is
  **GF(2⁸)** (characteristic 2); there is **no ring homomorphism GF(2⁸)→F_r** (a ring hom must preserve
  characteristic), so a prime-field KZG/Pedersen commitment cannot carry the RS relation, and embedding
  bits into a prime field pays the blow-up Binius exists to remove. The char-2-native answer
  (FRI-Binius) is **research-grade Rust: the reference `binius` was archived 2025-09-09 with a README
  discouraging security-critical use; `binius64` is early, unaudited in any language.** Adopting it would
  violate B8 in Rust too — so this is **not a pure-Go problem.**
- **What ships instead (M0).** The `core/repairproof` **Merkle-recompute floor** — reconstruct the
  target from k survivors, check byte-identity to the manifest-committed shard id. Sound, pure-Go,
  content-blind, but **not bandwidth-blind** (fetches k survivors). An explicit M0 non-goal.
- **Revisit when.** A char-2-native commitment matures (any language), **or** an F_p storage re-encode
  (Escape hatch C) — but note that even the natural pure-Go alternative, a Merkle/FRI repair proof over
  the existing GF(2⁸) code, needs a binary *extension* field for FRI soundness — which is the Binius
  line, i.e. loops back to immature. **That loop is exactly why the Merkle-recompute floor is the right
  M0 call.** Fast-follow, not M0.
- **Recorded in:** [`decisions.md` D-S7](../decisions.md); deep-dive
  [`h7-proof-of-repair.md`](h7-proof-of-repair.md) §3, §7, §13.

## 2. Threshold DECRYPTION (t-of-n) — *narrow the claim; the gap is decryption only*

- **What silt wants.** A validator **quorum as a threshold-distributed TTP**: a value decryptable only
  if ≥ t validators cooperate (P2 fair-exchange dispute resolution; accountable disclosure if in scope).
- **True binding constraint: a library gap, but only the *decryption* slice.** **DKG and threshold
  *signing* are mature, adoptable pure-Go today** — drand/kyber (`share/dkg/pedersen`, `sign/tbls`),
  production-proven in the League of Entropy, drand daemon audited (Sigma Prime, 2020). What is missing
  is generic threshold **decryption**: kyber's `encrypt/` is ECIES + IBE only; the one pure-Go
  threshold-Paillier option (`niclabs/tcpaillier`) is unmaintained since 2020, unaudited, trusted-dealer.
  *(Earlier text implied DKG/threshold-signing were unavailable — they are not.)*
- **What ships instead (M0).** The P2 **abort-safety floor** (`core/demand/fairexchange.go`): an
  `ExchangeCommitment` pre-release promise + two locked invariants (an aborted exchange never consumes
  the token; a commitment can't redeem as demand). The dispute-*resolution* half is gated. Demand is a
  **neutral** observable, so an unresolved abort only undercounts a neutral quantity — **low stakes for
  M0; the urgency here is doc-accuracy, not mechanism.**
- **Revisit when.** The resolver becomes a priority — then evaluate **wazero-wrapping** a vetted
  threshold-decryption impl (Escape hatch A) rather than waiting indefinitely.
- **Recorded in:** [`decisions.md` D-DEMAND](../decisions.md), [`decisions.md` D-DISCLOSURE](../decisions.md).

## 3. Verifiable encryption (Camenisch–Shoup) — *wait-and-floor; but question the requirement first*

- **What silt wants.** Encrypt the exchange commitment to the quorum **and prove, without decrypting,
  that it contains the correct value.**
- **True binding constraint: niche everywhere — and possibly not required.** The one pure-Go CS impl
  (`coinbase/kryptology verenc/camshoup`) is in a repo **archived 2022-09-08** ("should not be used"),
  outside its audited set; the only other Go option self-declares "never use in production." CS is
  research-grade in *every* language. **Higher-value point: mainstream threshold-ECDSA (GG18/GG20, e.g.
  `bnb-chain/tss-lib`) does not use CS at all — it uses Paillier *range proofs*.** So before treating CS
  as a dependency to wait on, **re-derive whether silt's dispute path needs it**, or whether a
  range-proof construction covers the same job. This may be a gap to *design out*, not wait out.
- **What ships instead (M0).** Nothing for this leg — it rides on §2's gated path.
- **Requirement under review.** CS may be replaceable by a Paillier-range-proof construction with more
  mature tooling. Revisit together with §2.
- **Recorded in:** [`decisions.md` D-DEMAND](../decisions.md).

## 4. ZK threshold predicate (survivor-count metric) — *not a library gap; a design gap*

- **What silt wants.** Prove a **negative about distributed state without revealing who** — "≥ t
  distinct-domain, PoR-fresh replicas of this root are gone" (a *survivor Nakamoto coefficient*), as a
  ZK threshold predicate.
- **True binding constraint: trusted-setup aversion + circuit-soundness design, NOT library absence.**
  `gnark` (Consensys) is **pure Go** (no cgo in the default build; C++/CUDA backend is opt-in), a general
  R1CS/PLONKish stack with **~9 audits (2022–2024)** powering the Linea zkEVM, and it **can express** such
  a predicate. The real blockers are (a) both Groth16 and PLONK import a **trusted-setup ceremony**, which
  silt (B8/no-trusted-setup) resists — and for a censorship-resistance metric "who ran the ceremony" is
  itself a capture vector; **no audited *transparent* (STARK/FRI) pure-Go backend exists**, so *that*
  specific slice is a real remaining library gap; (b) the genuine difficulty of designing a *sound*
  circuit over distributed, freshness-attested replica state (what is the witness? who attests liveness?
  how is it kept non-gameable?).
- **What ships instead (M0).** The **CT-style append-only transparency log** (`core/translog`, H9):
  provable *recording* of every revoke/unrevoke (inclusion + consistency proofs). Provable non-globality
  by transparency — the weaker, shipped form of D-TAKEDOWN.
- **Revisit when.** If the stronger metric is ever wanted, it is a **design effort** (specify the
  predicate + witness + trust model, and a transparent-setup scheme) *using gnark* — not a "wait for a
  library" effort.
- **Recorded in:** [`decisions.md` D-TAKEDOWN](../decisions.md).

## 5. Continuous identity-chained VDF — *deferred on merit; pure-Go is irrelevant*

- **What silt wants.** If the **T (time) axis** ever prices standing *acquisition*, the only sound form
  is a **continuous VDF chained to the bond identity** — an unbroken chain of sequential work that could
  not have started before the bond existed (non-pre-farmable, unlike a `firstSeenTick` age counter, the
  coin-age anti-pattern).
- **True binding constraint: deferred on merit, and it has no implementation in any language.** A
  *continuous* VDF (Ephraim–Freitag–Komargodski–Pass, EUROCRYPT 2020) has **no impl anywhere** and is
  theoretically contested (Justin Drake: "non-practical"); "chained to a bond identity" is a bespoke
  composition not named in the literature. **Independently, it is out of scope on its own merits** —
  marginal Sybil-resistance over an already non-substitutable D axis, plus an always-on per-bond
  sequential process and a "fastest-squarer measures time" residual. **Pure-Go has nothing to do with it.**
- **What ships instead (M0).** **T = retention only** — `DecayStale` + `BondMaxAge` force continuous
  re-proof; acquisition is priced by **D alone** (the F-2 relabel).
- **Revisit when.** M1+, only if a real acquisition-time factor is ever wanted.
- **Recorded in:** research memo-F2 §3; the T-axis relabel in `m0.md` §3/§4, `TENETS.md`, `core/credit/credit.go`.

---

## Escape hatches (for the genuinely-needed, mature-somewhere library gaps)

When a primitive is genuinely needed, **mature in *some* language, merely missing in pure-Go, *and*
non-consensus** (which all five are — none is consensus-load-bearing, so a value computed heterogeneously
across nodes cannot fork the chain), there is a middle path between "floor" and "drop pure-Go." Verdict up
front: **wait-and-floor (D) remains default-correct for silt's current frontier gaps because they are
immature everywhere; the hatches earn their complexity only in a narrow band.**

**A. `wazero` / WASM-embedded primitive — the least-bad hatch; worth a small spike.**
Compile a mature Rust/C impl to WebAssembly and run it inside the Go binary via **wazero** (a pure-Go,
zero-dependency, cgo-free WASM runtime). **Buys:** adopt a mature impl (B8-safe) while genuinely keeping
`CGO_ENABLED=0` + cross-compilation, with a *better* trust boundary than cgo (guest runs in isolated
linear memory, cannot corrupt the Go heap). **Costs (do not hand-wave):** ~**4.7× native slowdown** on a
crypto benchmark; the fast compiler is **amd64/arm64-only** (interpreter ~10× slower elsewhere — correct
everywhere, fast only on two arches); Rust→WASM is **not reproducible by default** (needs pinned-Docker +
`--locked`); and the trust surface is *reshaped, not removed*. **Precedent + gap:** wazero is
production-grade (Dapr, Trivy, Redpanda); Arcjet migrated Rust-via-cgo → Rust-via-WASM/wazero *specifically
to regain `CGO_ENABLED=0`* — but **there is zero public example of heavy crypto (arkworks/Binius/threshold)
in wazero**, so benchmark the actual target in compiler mode on amd64 before committing. **Best fit:** §2
threshold decryption, §3 verifiable encryption — off-hot-path, non-consensus. **Not** a storage-hot-path
primitive.

**B. Optional cgo behind a build tag — a footgun for anything consensus-touching.**
Pure-Go static by default; opt-in build links a vetted C lib. **Killer caveat:** for a
**consensus-load-bearing** primitive this is dangerous — two nodes computing a value two ways can diverge
on pairing/BLS edge cases and split the chain. The ecosystem does not treat this as an operator knob:
`blst` requires cgo with no pure-Go path; go-ethereum's KZG-4844 dual-impl is selected by a *runtime flag*
kept as an escape *away* from a broken impl, not an operator choice; Celestia/CometBFT ship a **panic-stub**
fallback. **Best fit:** *only* a genuinely non-consensus primitive (local signing, transport auth). Never
for proof-of-storage verification — there, pick ONE impl network-wide.

**C. Re-encode storage GF(2⁸) → F_p (specific to §1) — sound but a hot-path tax.**
Move the RS erasure code *inside* the commitment field so a mature pure-Go prime-field KZG
(**gnark-crypto**, `CGO_ENABLED=0`, audited incl. a 2024 EF-funded Sigma Prime KZG audit) carries the
repair relation via additive homomorphism (Semi-AVID-PR precondition). **Buys:** bandwidth-blind PoR with
*existing* pure-Go crypto — no Binius, no cgo. **Costs:** a deep storage-core change with a **1–2
order-of-magnitude throughput tax** (GF(2⁸) SIMD RS ~12–15 GB/s/core vs F_r multi-limb Montgomery mul at
hundreds of MB/s). Industry tracks one line: KZG-DA forces prime-field RS (Ethereum 4844/PeerDAS), while
Merkle/FRI-DA keeps GF(2⁸) (Celestia). **Verdict:** a real-but-expensive **M1+** lever, not a quick unlock.

**D. Wait-and-floor (status quo) — the default; correct when the primitive is immature *everywhere*.**
Checklist, in order (a "wait" short-circuits the rest):
1. **Mature/audited in its native C/Rust?** If NO → wait-and-floor (a hatch only *imports* the immaturity).
   **The dominant gate** — it fails for §1 (Binius archived), §3 (CS), §5 (no impl anywhere).
2. **Consensus-load-bearing?** If YES → dual-impl/cgo is dangerous; favor a single pure-Go floor.
3. Floor sound (weaker guarantee, not a hole)? 4. Residual bounded + documented? 5. Would a later switch
   force a hard-fork / re-encode? (the one case that justifies paying hatch cost *now*, e.g. F_p.)
6. Credible timeline for the mature lib? If no far shore, design the floor as first-class.

Reserve A/B/C for the narrow band: mature-somewhere, missing-in-pure-Go, *and* consensus-safe. For silt's
current frontier gaps that band is nearly empty — so wait-and-floor is **not a defeat, it is the right
answer**, and the hatches are a per-gap menu for later as libraries mature.

---

## The through-line

**Keep the static binary; the gaps are about primitive maturity and construction design, not the
language.** Each floor is sound and honestly labelled (the M0 deliverable). Where a gap is *real and
worth it*, reach for **wazero, not cgo-everywhere** — and only then. Not one of these five is
load-bearing for the M0 safety claims (C1/C2 and the demand firewall); they bound *reach* (blind
auditing, dispute resolution, ZK metrics, a time axis), not the core denials. The two that actually
*move* when a feature becomes a priority are **wazero for §2/§3** and the **F_p re-encode for §1** — the
rest is wait-and-floor, correctly.
