# Gate 4 — the M0 mechanism: design + threat model (pre-code)

> **Status: design doc, ahead of code.** ROADMAP requires Gate 4 to own a
> design doc + threat model + an **external** adversary plan *before*
> implementation — the binding, not the primitives, is the research (B8). This
> is that document. It is a spec a skeptic can read, not the line-level
> implementation; it fixes the "get this wrong and you re-architect" decisions
> and hands the external red-team (V3) a falsifiable target.
>
> Gate-4 issues: #90 (4a PoR), #91 (4b bond), #92 (4c standing — *this issue is
> M0*), #93 (4d persistence), #100 (4f consensus equivocation/fork), #52 (4e
> multi-machine field test). Permanence prerequisites #97/#98/#99 landed
> (2026-08-02); `Block.Version` (#98) is the hard-fork guard every record change
> below rides on.

> ## ⚠️ LIVE RED-TEAM VERDICT (2026-08-04) — supersedes the "Resolved" language below
>
> The external M0 red-team ran against shipped code (`c1397e0`) and **broke all
> three corners in the composition** (the adopted primitives — the Wesolowski VDF
> and the Shacham–Waters PoR — held). Where §3/§4 below say a constraint is
> "Resolved by D1/D2/D3," read it as **design intent, not shipped reality** unless
> this banner says otherwise. Full report: `docs/reviews/M0-REDTEAM-REPORT.md`.
>
> | Corner | Design says | **Live verdict** |
> |---|---|---|
> | Privacy (D3 issuance-mixing) | Resolved | 🟡 **fee-link FIXED (2026-08-04), network-link pending** — prepaid publish credits (online Chaumian e-cash) sever the per-publish fee debit: the fee is charged in bulk at mint, a publish spends a credit with no durable-identity debit (`core/blindtoken`+`core/node`). Residual: relay/ephemeral/epoch network-layer link still open, so IP+timing correlation remains (F4). See `m0-privacy-issuance.md`. |
> | Accountability | Resolved | 🟢 **FIXED (2026-08-04, #136)** — on-chain revocation was a global, ownership-unchecked, irreversible switch (F5); now existence-checked, per-operator opt-in, and reversible. |
> | Sybil (D1 bond) | Resolved | 🟢 **FIXED (2026-08-04) — structural** — the plot now binds the full block *bytes* over a proven depth-robust graph (DRSample), closing the 1/128 leaves-only gap, and the VDF is seeded from a plot block **read before it runs**, so releasing the space forfeits the answer (F1/F2). Residual: the quantitative min-bond-size/delay floor is a tuning follow-up. See `m0-sybil-bond.md`. |
> | Consensus D2 (fork-choice) | Resolved | 🟢 **F6 FIXED + wired (`-objective`, e2e), F7 RESOLVED (2026-08-04)** — objective on-chain PoST-bond fork-choice (`Block.BondRegs`) makes divergent replicas agree and forks heal, with an anchor-bootstrapped cold-start and a daemon flag. F7 (cross-height double-backing) is resolved by F6 + sound same-height slashing: the pattern is indistinguishable from an honest reorg-follow (so slashing it would hit honest validators) and is neutralized by F6 anyway (`core/chain/redteam_f7_test.go`). Residual: flip the shipped default to objective (a launch-config decision). See `m0-consensus.md`. |
>
> **Status line: primitives real, composition unproven, M0 not yet held.** The
> Sybil (F1/F2), privacy (F4), and consensus (F6/F7) fixes are the mechanism
> **design turn** that follows; accountability (F5) is done. The design turn is
> written up per corner:
> - **Sybil bond (F1/F2/F3)** — [`m0-sybil-bond.md`](m0-sybil-bond.md) *(keystone; F6 depends on it)*
> - **Privacy issuance / D3 (F4)** — [`m0-privacy-issuance.md`](m0-privacy-issuance.md) *(independent)*
> - **Consensus fork-choice + slashing (F6/F7)** — [`m0-consensus.md`](m0-consensus.md) *(depends on the bond)*

## 0. The three decisions this design is built on

Surfaced by the [build-vs-intention audit](../reviews/build-vs-intention-2026-08-02.md)
and decided 2026-08-02. Two of the three took the more ambitious option, which
expands V1's M0 beyond a mechanism swap — stated plainly so the cost is owned,
not discovered mid-build.

| # | Decision | Consequence |
|---|---|---|
| **D1 — Bond primitive** | **Proof-of-Space-Time (PoST)**, Spacemesh-family. | Standing literally *becomes* dedicated space × time. Needs off-loop plotting + a persisted plot blob (resolves Constraint 1 / #4d). Not a hybrid; the storage pledge stays a *separate* resource from the Sybil bond. |
| **D2 — Consensus** | **Full fork-choice / reconciliation** (not "document the honest-majority bound"). | The append-only chain must gain a reorg/undo path (`apply` is one-way today). In return, fork-choice weight can be **objective on-chain PoST bond**, which *resolves* the subjective-reputation partition tension (Constraint 3) instead of merely documenting it. The larger commitment. |
| **D3 — Unlinkability depth** | **Also mix/relay the token-issuance step** (not "widen the anon-set and document the IP boundary"). | A new content-blind mixing path for blind-token acquisition (reuses the Gate-3 relay), on top of the already-ephemeral publish identity. Severs publish→standing at ledger **and** issuance **and** network layers. Adds a subsystem + publish latency. |

## 1. The M0 claim, made concrete

M0 (TENETS Part 0) is *held* iff an external red-team **denies all three failure
modes**. Gate 4 is the mechanism that makes the Sybil corner real; the other two
corners are already architectural. The falsifiable claim:

> **Standing = the time-integral of sustained, identity-bound, cheaply-verifiable
> proof-of-space-time (bond) and proof-of-retrieval (useful work) — cheap for one
> honest node, N× ruinous for a Sybil farm, with no coin, stake, or capital
> lockup — and a publish is cryptographically unlinkable from the standing that
> authorized it, at every layer an observer can watch.**

The three denials the V3 suite must produce (§6):

1. **Privacy** — no adversary ties a published root to the durable standing key
   that paid for the token (ledger + issuance + network layers).
2. **Accountability** — takedown acts only on a *hash*, pluralistically; no
   identity-level or global switch exists to find.
3. **Sybil** — no adversary buys consensus weight, washes reputation, or floods
   a denylist for less than N independent space-time bonds.

## 2. Architecture: the composition

```
                 off the core loop (adapter)                 on the core loop
   ┌───────────────────────────────────────┐   ┌─────────────────────────────────┐
   │  PoST plotter  ─plot(NodeID,size)→ Root│   │  cheap verify: Verify(root,     │
   │  persisted plot blob (survives restart)│   │    nonce, proof) — O(log n)     │
   └───────────────────────────────────────┘   │                                 │
                    │ commitment (Root,size)     │  standing = ∫ over time of      │
                    ▼                            │    { PoST space proven,         │
   bond commitment ── identity-bound ──────────▶│      PoR audits passed }        │
                                                 │    per NodeID, decays if unfed  │
   PoR authenticators (Shacham–Waters/PDP)      │                                 │
   computed at Stage() time, committed in the   │  standing gates: propose/attest │
   manifest/entry (rides Block.Version)         │    (consensus), denylist weight │
                                                 └─────────────────────────────────┘
                                                                │
   blind publish token (Chaumian RSA) ──acquired via mix/relay──┘  (unlinkable)
        k-of-N distinct qualified validators sign a blinded serial;
        the tokened, Publisher-less entry is the default chain record (#97).

   consensus: fork-choice over chains weighted by on-chain-verifiable PoST bond;
        equivocation evidence slashes; reorg replays to the heavier fork.
```

The load-bearing structural fact (from the audit): **the audit crypto reaches
consensus only as a number.** `credit.Reputation` (`core/credit/credit.go:179`)
→ `chain.rep` gates proposing/attesting; the chain record encodes neither the
bond scheme nor the PoR scheme. So 4a/4b/4c swap the *inputs* to that number
without touching the block schema — except where we deliberately add committed
data (PoR authenticators, equivocation evidence), which is why `Block.Version`
had to land first.

## 3. Mechanism spec

### 3a. Real proof-of-retrieval (#90 / 4a)

**Replace.** `core/node/por.go`: today possession is a tag
`SHA-256(nonce ‖ chunk_data)` (`por.go:38`) and the auditor *fetches the ground
truth* to grade (`gradeAnswers`, `por.go:191`). That defeats the purpose — a
real PoR lets the verifier check possession **without** holding or fetching the
bytes.

**Adopt (B8, do not invent).** A compact Proof-of-Retrievability
(Shacham–Waters) or Provable Data Possession (Ateniese) scheme: homomorphic
authenticators computed once at encoding time; a challenge samples a few block
indices; the prover returns one aggregated response; the verifier checks it
against a small public verification tag in O(1) communication, no fetch.

**Integration + the one schema touch.** Authenticators are computed in
`pipeline.Stage` (`core/pipeline/pipeline.go`) over the *ciphertext* chunks
(content stays blind) and their commitment is carried in the sealed
manifest/entry. This is a **record change** — it rides `Block.Version` (#98).
`gradeAnswers` loses its ground-truth fetch entirely and becomes a pure verify.
The care-link privilege split still holds: a caretaker verifies retrievability
without decrypting (it checks authenticators over ciphertext).

**Denial (V3):** a prover that does not hold the bytes cannot produce a passing
aggregated response except with negligible probability. Adversarial test: a
`liar` node (the hook already exists, `por.go` `n.liar`) fails every challenge.

### 3b. The bond = Proof-of-Space-Time (#91 / 4b) — D1

**Replace.** `core/bond/bond.go:170 sealBlock` — iterated SHA-256 expanding
`(NodeID ‖ index)` to 4 KiB blocks, an honestly-labeled space-*lite* placeholder,
resealed cheaply on the core loop (`bondaudit.go:28`). The verifier interface is
already the right shape and stays: `Verify(root, size, nonce, Answer) bool`
(`bond.go:126`) is stateless and O(log n) — Merkle inclusion proofs against a
published root. **We keep that seam and replace what fills the blocks.**

**Adopt (B8).** A published PoST construction (Spacemesh-style):

- **Plot (one-time, off-loop).** From a commitment derived from the node's
  identity, generate a large dataset of size `S` (the pledged bond space) and a
  Merkle root over it. Plotting is deliberately expensive and proportional to
  `S`; it is **identity-bound** — the dataset is a function of the NodeID, so it
  is non-transferable and a Sybil farm needs an independent plot per identity.
- **The time integral (ongoing, cheap).** Each epoch, a proof-of-*space-time*
  binds a fresh unpredictable challenge (from recent chain state) to a
  **non-parallelizable sequential-work** step (a VDF), then samples plot blocks
  and returns Merkle proofs. The sequential step is what makes it *space-time*:
  you cannot retroactively fake having held the space across the epoch, and you
  cannot parallelize your way out of the elapsed-time requirement.
- **Verify (on-loop, cheap).** Unchanged in shape: recompute challenge indices,
  check Merkle proofs + the VDF output. Stays O(log n), stays on the core loop.

**Constraint 1 resolved (B2 — single-loop core).** Plotting and per-epoch proof
generation run **off the core loop in an adapter**, handing only the finished
commitment (`Root`, `size`) and the epoch proof back to core. A new persisted
**plot-blob adapter** (analogous to `adapters/diskproofs`) means a restart does
**not** re-plot (#93 / 4d). Only cheap verification touches the loop.

**Sybil cost model (S6 — cheap for the honest).** One honest node commits a
modest amount of **commodity disk** (reusable, not staked or burned — no capital
lockup) and pays a one-time plot; thereafter proofs are cheap. A Sybil farm
wanting M identities pays M independent plots and must hold M×S space *across
time* — the cost is linear in identities and cannot be amortized (identity
binding + the per-epoch sequential work prevent one plot from covering many
identities or one time-slice from covering many epochs). The design doc's
**stated target**: honest-node cost ≤ a hobbyist SSD partition + negligible
ongoing CPU; Sybil-farm cost = N× that, with N the number of identities needed
to reach the consensus/denylist influence threshold. The red-team validates the
constant (§6).

### 3c. Identity-bound, time-integrated, unlinkable standing (#92 / 4c) — M0 itself

**The composition — the novel part (no library gives this).** Per identity,

```
standing(id, T) = ∫₀ᵀ [ w_b · PoST_space_proven(id, t) + w_a · PoR_audits_passed(id, t) ] dt
```

realized on the existing seam: `credit.Reputation` (`credit.go:179`) already
sums `bondedBytes/bondUnit + audit terms`, and already has the **time integral**
in skeletal form — `DecayStale` (`credit.go:150`) zeroes `bondedBytes` if the
bond is not re-proven within `maxAge` (`bondaudit.go:52`), and `firstSeenTick`
gives the age. Gate 4 makes this real:

- `RecordBondChallenge` (`credit.go:131`) is fed by **PoST epoch proofs** instead
  of the placeholder reseal; `bondedBytes` becomes *proven space this epoch*.
- Standing *accrues* with sustained proof and *decays* without it — so a farm
  cannot pay once and coast; it must hold the space-time continuously. This is
  what makes standing an integral, not a one-shot payment.
- The formula scalars (`w_b`, `w_a`, `bondUnit`, decay `maxAge`) are free
  variables (Evolving tenet); the **shape** — bond-and-audit only, serving bytes
  deliberately excluded (`credit.go:173`) — is the M0-load-bearing part.

**What standing gates.** Consensus influence (propose: `MinProposerRep`; attest:
`MinAttesterRep`, `chain.go:313/375`) and denylist/curation weight. Influence is
*earned*, never bought raw.

**Unlinkable publish — the privacy half.** A publish must not be tied to the
standing key that paid for it. Three layers, all severed (D3):

1. **Ledger layer (done, #97).** The default entry is tokened and
   **`Publisher`-less**; the chain *refuses* a `Publisher`-bearing entry unless
   `AllowPublisher` (`chain.go` `ErrPublisherEntry`). The blind token
   (`core/blindtoken`, Chaumian RSA-FDH) proves "a quorum of qualified
   validators authorized *a* publish" without revealing *which* requester.
2. **Network layer (mostly done).** A publish already announces from a **fresh
   ephemeral identity** (`joinSwarm` → `identity.Generate`, `daemon.go:719`), not
   the bonded daemon NodeID. Residual: the operator's IP.
3. **Issuance layer (D3 — the new work).** The residual linkage is that the
   **bonded** identity requests blind signatures from validators
   (`AcquireToken`, `node/tokenrole.go:88`), an event a colluding validator set
   can correlate with the publish by IP + timing (`publishtoken.go:9-14`).
   **Fix:** route token acquisition over the **content-blind Gate-3 relay / a
   mix**, and batch/delay issuance into epochs so the anonymity set is "every
   identity that requested a token this epoch," not "the one who asked at
   14:03:07 from this IP." Bound the colluding-validator narrowing with a
   **canonical validator set** (every publisher requests from the same set, so
   the subset-choice leaks nothing).

**Denial (V3):** given the full ledger, the validators' issuance logs, and
network traces, the red-team cannot link a target root to its standing key better
than guessing within the epoch anonymity set.

### 3d. Persistence + issuer distribution (#93 / 4d)

Everything that *is* standing must survive a restart, or standing means nothing:

- **Plot blob** persists (new adapter, §3b) — a restart re-verifies, never
  re-plots (B7: persisted state is re-verified on load, not trusted).
- **Blind-signature issuer RSA key** persists (today in-RAM, `blindtoken/issuer.go`);
  **on-chain issuer registration** so the issuer set is distributed and
  verifiable, not a single-host secret. Ties to immutable #3 (no *permanent*
  center): launch-window issuers are explicit, time-boxed anchors that shed.
- **Ledger + chain state** rebuild by re-syncing blocks; the double-spend
  `spent` set and reputation are reconstructable from the chain. The reorg path
  (§3e) makes "rebuild to the heavier fork" a first-class operation.

**Done when:** bond + issuer key survive restart with standing intact; issuer
registration is on-chain; a restart **regression test** proves it (V5, local).

### 3e. Consensus: fork-choice, equivocation, slashing (#100 / 4f) — D2

Today (honestly labeled, `chain.go:22-25`): `Attest` is stateless
(`chain.go:125`), nothing records what a validator signed; slashing exists only
for storage audits (`credit.go:109`), not consensus misbehavior; `apply` is
append-only with no undo (`chain.go:396`) and `SyncChain` silently `break`s on
divergence (`chainrole.go:207`) — a forked node stays forked forever. D2 builds
the defense:

- **Fork-choice weight = objective on-chain PoST bond.** A chain's weight is the
  sum, over its blocks, of the **verifiable PoST bond** of the attesters who
  committed them — a quantity *any* node can recompute from on-chain proofs.
  This deliberately moves fork-choice off **subjective** `reputation`
  (`credit.go:179`, a per-node local view) and onto an objective, globally
  agreed weight — which is how D2 **resolves Constraint 3** rather than only
  documenting it. Heaviest verifiable-bond chain wins; ties break by lower block
  hash (deterministic).
- **Reorg / reconciliation.** `apply` gains an inverse: entries, `spent` serials,
  revocations, and `validatorsSeen` become **reversible** (or the chain rebuilds
  from the last common ancestor via a checkpoint). On seeing a heavier valid
  fork, a node rolls back to the common ancestor and replays — `SyncChain` stops
  `break`ing and reconciles.
- **Equivocation evidence + slashing.** Per-validator signing history is tracked;
  two signatures for **different blocks at the same height** are a compact,
  self-verifying **equivocation proof** that any node can include in a block (new
  record, rides `Block.Version`). A proven equivocation **slashes reputation**
  (extending `credit` slashing beyond storage audits) and drops that validator's
  fork-choice weight — so double-signing is not free and cannot capture the
  quorum for free.

**Denial (V3):** an equivocating or off-head proposer cannot get two competing
histories to both stand; the partition heals to the heavier-bond fork; provable
double-signing costs the actor standing.

## 4. Constraints — status after the three decisions

The stub's four "sharp edge" constraints, now each resolved or explicitly owned:

1. **Off-loop, persisted proof work (B2).** **Resolved by D1** — PoST plotting +
   per-epoch proofs run in an adapter; only cheap verify touches the loop; a
   persisted plot blob avoids re-plot on restart (#4d).
2. **Cross-layer unlinkability.** **Design intent: D3 — 🔴 NOT shipped (red-team
   F4).** Layers 1 (Publisher-less ledger, #97) and 2 (ephemeral publish
   identity) hold, but the issuance layer's mix/relay + epoch batching + canonical
   validator set **were never built**: `AcquireToken` requests blind signatures
   directly from the bonded identity's own transport, so a colluding issuer
   minority ties the publish to the standing key by IP+timing (and by the fee
   debit). The anonymity set is a *singleton*, not "narrowed." Ships in the
   privacy design turn.
3. **Subjective reputation as the partition boundary.** **Design intent: D2 — 🔴
   NOT shipped (red-team F6).** Fork-choice weight is still the *subjective local
   reputation view* (`blockWeight` qualifies attesters by `c.rep(id)`), not
   objective on-chain PoST-bond weight — so two honest replicas with different
   audited sets compute different winners and diverge permanently. Reputation
   gates *eligibility* today; making bond weight decide *fork-choice* (and it must
   be a real bond — see Constraint on D1/F1) is the consensus design turn.
4. **Permanence — record right before real blocks.** **Landed** — #97 (tokened
   default), #98 (`Block.Version`), #99 (Gated registry fenced). Every record
   change above (PoR authenticators, equivocation evidence) rides the version
   guard.

## 5. What is a free variable vs. baked into the schema

From the code map — so the build knows what a swap costs:

- **Free (swap without a schema bump):** the PoST plot/verify functions behind
  `bond.Verify`'s existing interface; the PoR authenticator scheme behind a pure
  `Verify`; the standing scalars `w_b/w_a/bondUnit/maxAge`; reputation gates
  (`MinProposerRep`, `Quorum`); RSA key size; the fork-choice tie-break.
- **Baked in — needs `Block.Version` (landed):** PoR authenticator commitments in
  the manifest/entry; equivocation-evidence records; any change to what
  `Block.Hash` commits to. These are the deliberate additions Gate 4 makes; the
  version guard is what lets old and new eras validate under their own rules.

## 6. Threat model + external red-team plan (V3 / B8) — the deliverable

Per B8/V3 and #92's "Done when": the suite that *certifies* M0 is written by a
party **other than the author** (audit / bounty / independent implementer), runs
**multi-machine** (#52 / 4e), and its result **is** the M0 verdict. M0 ships
proven or does not ship.

**Adversaries to simulate** (anti-persona #14, TENETS Part VII): a Sybil farm; a
colluding validator minority; an equivocating / off-head proposer; a censoring
validator; a network observer correlating publishes; a `liar` prover claiming
storage it lacks.

**The three falsifiable denials** (each a test that must *fail for the attacker*):

| Corner | Attacker outcome that must fail | Concrete test |
|---|---|---|
| **Privacy** | Link a target root → its standing key | Given full ledger + issuer logs + network traces across machines, adversary's link accuracy ≤ chance within the epoch anonymity set. Probe each layer: tokened entry carries no Publisher; issuance is relay/mixed + epoch-batched; publish announce is ephemeral. |
| **Sybil** | Reach consensus/denylist influence for < N bonds | Measure real plot + space-time cost per identity; show influence threshold needs N independent identity-bound plots held across time; a farm splitting one plot fails identity-binding; coasting without re-proof decays out. State the cost constant. |
| **Accountability** | Find an identity-level or global takedown switch | Show takedown acts only on a hash, per-operator/pluralistic; no code path removes by identity or globally; curators are themselves accountable/auditable. |

**Consensus sub-suite (D2):** equivocating proposer cannot stand two histories;
partition heals to heavier-bond fork; provable double-sign slashes; low-bond
proposer rejected over the real wire; forged/tampered block rejected.

**Multi-machine field test (#52 / 4e):** N validators across real machines +
NAT; propose→attest→commit converges on every replica; kill a validator
mid-round, quorum still commits; restart a validator, standing survives (#4d).
The storage plane got this rigor; the trust plane must too.

**Cost-model honesty (open research risks — hand these to the red-team):**

- Can a Sybil farm amortize one PoST plot across N identities, or is binding
  strict (N plots for N identities)? *Design intent: strict; the red-team must
  break or confirm it.*
- Is "N× ruinous for a farm, hobbyist-cheap for one honest node" (S6) actually
  achievable at the target honest cost, given the plot is the equaliser? State
  and measure the constant.
- Does the blind-token anonymity set stay large under realistic validator
  collusion + IP/timing side channels, given the mix/relay + epoch batching?
- Can a validator capture the quorum on a small/young network *before* the
  anchor training wheels shed (#100, risk #15)? Bound it.

## 7. Build sequencing (design-locked; code is the next turn, not this one)

1. **4a PoR** (#90) — contained to `por.go` + the manifest/entry authenticator
   commitment (rides `Block.Version`). Adversarial: `liar` fails.
2. **4b PoST bond** (#91) — the plot/verify adapter + persisted plot blob behind
   the existing `bond.Verify` seam. The long pole. Off-loop (B2).
3. **4d persistence** (#93) — plot blob + issuer key persist; on-chain issuer
   registration; restart regression (V5).
4. **4c standing** (#92) — wire PoST epoch proofs into `RecordBondChallenge`;
   tune the integral; mix/relay + epoch-batch the token issuance (D3). *This is
   M0; it ships specified + externally proven.*
5. **4f consensus** (#100) — fork-choice by on-chain bond weight; reversible
   `apply` + reconciling `SyncChain`; equivocation evidence + slashing (D2).
6. **4e field test** (#52) + **V3 external red-team** — the certification. M0's
   yes/no verdict goes on the board.

Each piece ships with unit + integration/sim + e2e coverage and a failing-first
regression, catchable on the local suite (build-immutable V5); the external V3
suite is the final gate. Gate 5 (economics / S7 durability-that-pays) is designed
*alongside* this, not after — the `por.go` verify-then-settle pattern is its
anti-fraud template.
