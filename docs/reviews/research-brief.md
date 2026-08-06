# Research brief — open questions for the research team

> **✅ ANSWERED (2026-08-06).** This brief was commissioned and the research team
> delivered — eight footnoted memos in
> `silt-reviews/research/research-outcome/commission/` (synthesis in
> `00-COMMISSION-SYNTHESIS.md`). Headline: the two named walls are **lower than
> feared** — **A1 center-less proof-of-repair EXISTS** (composable from proven parts,
> plain-RS; unblocks D-S7 → build H7) and **A2 non-globality metric is CONSTRUCTED**.
> The genuinely-open residue narrowed to **one** task — the **shared-content sealing
> boundary** (B5: plain PoR over shared shards leaks γ→1/N; needs identity-keyed PoRep
> sealing) — plus the held-in-tension bounds M0 already predicted (C2, wash-vs-real,
> cold-start). Decisions folded into [`../decisions.md`](../decisions.md) and
> [`../design/m0.md`](../design/m0.md) §10. This brief is kept as the **question set**
> the commission answered; the constructions are now build/research tracks, not open
> questions. The seam stress-tests (B1–B5) remain the **sharpened target for the
> external red-team** (test C1+C2 at declared parameters, not the primitives).

**Purpose.** The research package (memos 01–09) has been **accepted**, and its
recommendations are now decided directions (see [`../decisions.md`](../decisions.md)
and [`../design/m0.md`](../design/m0.md)). This brief asks the research team for the
things the memos **could not close** — the primitives a memo self-flagged as
non-existent, and the systemic seams that rest on economic/empirical arguments rather
than proofs. **We are not re-opening the directions; we are commissioning the
constructions and stress-testing the claim.**

**How to read this.** Everything here is scoped to silt's constraints: *proven crypto
only* (adopt, don't invent primitives — B8), *no permanent center*, *no speculative
external token* (an internal, non-speculative credit reserve is now permitted), and a
*paid substrate* (practical latency/bandwidth). The framing to attack is the systemic
M0 claim — **C1 (no discount) + C2 (no quiet capture)**, held in tension — not any
single primitive (a primitive failing a standalone "Sybil-proof" test is expected;
that's Douceur, not an M0 failure).

---

## A. Primary commissions — the two constructions the memos named as non-existent

### A1. Center-less proof-of-repair (D-S7 / backlog H7) — **highest priority**

Memo 07 concludes that the S7 funding model (internal escrowable credit reserve +
per-object escrow + auto-skim + rarest-shard bounty) is the only center-less,
non-charity path — **but** it rests on a primitive that *"does not exist in
deployment"*: a way to *verify* correct repair when the verifier cannot read the data.

**What we need a construction (or an impossibility result) for:**
> A succinct, cheaply-verifiable **proof that a specific coded (Reed–Solomon) shard was
> correctly regenerated** from surviving coded symbols — checkable by a **quorum that
> cannot read the plaintext**, verified against the erasure-code commitment / Merkle
> root the network already holds — with **no trusted center**. A bounty releases only on
> a valid proof; a false claim slashes the caretaker's storage bond.

**Specific questions:**
1. Can this be composed from proven parts — regenerating codes (Dimakis et al.) + a
   PDP/PoR-style challenge (Shacham–Waters, already in silt) + the Ethereum
   proof-of-custody / data-availability-sampling pattern — or is a new primitive
   required?
2. Proof size and verifier cost at silt's shard sizes? Is it succinct enough for a
   quorum to check per bounty without a bandwidth blowup?
3. Does it compose with the funding model (per-object credit escrow + auto-skim), or
   does verified-repair-under-payment introduce a new incentive gap?
4. **Equilibrium:** is there a Nash/competitive equilibrium in which center-less,
   token-less repair of *cold* coded data funds itself under churn? Memo 07 §5 says no
   paper proves one. A parameterized existence argument (with explicit, falsifiable
   cost-decline assumptions) is the deliverable; a proof of *non*-existence is equally
   valuable (it tells us S7 is unsolvable as scoped).

*This is the subject of the dedicated durability memo already anticipated. A1 is the
gate on whether files exist in three years.*

### A2. A formal non-globality metric (D-TAKEDOWN / backlog H9) — lower urgency

Memo 04 calls this *"silt's most distinctive contribution — no existing system can
currently demonstrate it never flipped a global switch."*

**What we need:**
> A formal metric/proof that a takedown was **not global** — how much of the content
> survived, on how many **independent hosts / failure domains** — demonstrable to a
> third party, Byzantine-robustly, **without the metric itself becoming a discovery
> oracle** that leads an adversary to the surviving replicas.

**Specific questions:** How to define non-globality measurably; can a CT-style
append-only revocation log plus survivor-sampling yield a provable *lower bound* on
surviving independent replicas; how to prevent the measurement from leaking
locations.

---

## B. Stress-test the systemic claim — the seams (m0.md §7)

These are where M0 is *held in tension*, not closed. Each rests on an argument the
memos flag as economic/empirical, not cryptographic. We want the research team (and,
separately, a red team) to attack them as the **unit of test**, replacing the retired
per-primitive attacks.

- **B1 — C2 under adversarial measurement (the wealth residue).** Does cost-to-corrupt
  over bond-distinct **operators**, Byzantine-robustly sampled, actually stay above the
  target *k* when the adversary skews measurement *and* splits stake into many equal
  bonds? Operator identification without a trusted authority is provably imperfect
  (Kwon et al.) — how tight can the clustering heuristic (infra/timing/locality) get
  before it costs the adversary meaningfully? **This is the corner most likely to be
  held-not-closed; we want the bound, honestly stated.**
- **B2 — The blind-token demand receipt (the load-bearing interlock).** Can the
  Chaumian primitive give an **unlinkable demand receipt** that is simultaneously
  (a) *unforgeable* without real served bytes and (b) *unlinkable* against a colluding
  validator minority? This single mechanism reconciles the Sybil corner's need to
  attribute demand with privacy's need to keep it unlinkable (memo 09 §4 row 4).
  **Prototype-first candidate** — it's where "held in tension" becomes a mechanism.
- **B3 — Real-demand > wash-demand.** C1's anti-wash is economic. Is there a parameter
  regime where honest demand provably dominates fabricated demand? We need the model
  and the regime, stated as the empirical condition it is.
- **B4 — Reachability of maturity (the whole bet).** Is there a proof or a safe
  parameterization that the anchor-scaffolded *young* regime reaches the mature (shed)
  regime **before** it can be captured? The entire thesis is conditional on this
  window being safe (immutable #3). No proof exists today.
- **B5 — Formalizing "no economy of scale survives."** m0.md §3–§4 is an argument, not
  a theorem. Turn "every cheap shortcut on one axis is caught by another axis's check"
  into a proof — or find the surviving shortcut. **This is the core theorem-level open
  task; the right thing for an academic collaborator.**

---

## C. Privacy metadata layer (D-PRIV decided; H8 build track questions)

D-PRIV is decided (metadata-layer tradeoff, not a blob-layer absolute). The build
track (H8) inherits two open problems memo 01 named:

- **C1 — Private lookup over a *dynamic* DHT.** Preprocessing PIR (Piano-style) assumes
  a static DB; silt's churn breaks the hints. Is there a churn-tolerant private
  provider-record / peer-routing scheme at silt's scale?
- **C2 — Non-colluding servers vs. "no permanent center."** Every practical
  metadata-private scheme needs ≥2 non-colluding parties. Can silt's quorum plane
  durably supply that **without becoming a center**? This is unresolved and interacts
  with the same shed-metric decentralization question as B1.

---

## D. Parameterization (cross-cutting)

`C_honest`'s non-substitutable weights (disk × address-diversity × time × served
demand), the concentration threshold *k*, the audit/decay windows, and the
demand-attestation ratio are all free variables the thesis assumes can be set safely
(the "evolving" tenet tier). **Is there a principled method to set them for a given
network size, or are they necessarily empirical/adaptive?** Setting them is where a
young network is most fragile.

---

## What we are NOT asking

- Do **not** re-derive the accepted directions (D-PRIV, D-S7, D-TAKEDOWN, D-DISCLOSURE)
  — those are decided; see `decisions.md`. If you believe a *direction* is wrong, say
  so explicitly as a challenge, but the default task is the constructions (A) and the
  seams (B), not re-litigating the choice.
- Do **not** attack primitives in isolation ("is this bond Sybil-proof?"). That is
  Douceur, and it is expected. Attack the composition (C1 + C2) and the seams.

**Pointers:** [`../design/m0.md`](../design/m0.md) (the systemic spec — §7 seams, §9
decisions, §10 open problems), [`../decisions.md`](../decisions.md) (the decision
ledger), [`../TENETS.md`](../TENETS.md) (canon). The accepted memos live in the
read-only research archive `silt-reviews/research/research-outcome/`.
