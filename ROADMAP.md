# Silt Roadmap

> **Source of truth.** *What M0 asserts and why* lives in
> [`docs/design/m0.md`](docs/design/m0.md) (the composition spec). *What the owner
> has decided* lives in [`docs/decisions.md`](docs/decisions.md). *The destination*
> is [`docs/TENETS.md`](docs/TENETS.md). This file is the **narrative path**: where
> we are, what the forward tracks are, and why they're ordered the way they are. It
> is not a tracker — live state is the GitHub **`V1` milestone** and its issues.
>
> The earlier **Gate 0→6 spine** (with Gate 4 as "the M0 mechanism to build") is
> **retired**: that mechanism is built and the mission was reframed (below). The
> `v0.1.x`/`0.2.x` tags are **experimental / learning releases**, not steps on the
> march to V1; that history lives in `docs/buildlog/`.

## Tenets are the destination; this roadmap is the path

**V1 is defined by the tenets, satisfied and field-proven — not by a feature
list.** Every track below advances one or more tenets; if a step serves no tenet,
it doesn't belong here. The relationship is one-way: **tenets guide the roadmap.**
A tenet gates V1 as a *principle*, never a *mechanism* — with one deliberate
exception, **M0** (the mission itself), whose *real* mechanism is in V1 by
definition. **Release is gated by proof (R1):** a tenet is "met" only when
field-proven multi-machine, not sim- or single-host-only.

## The launch stance — harden-first

The first public appearance must be **credible and spectacular from day one**. A
half-baked drop on a project this ambitious burns the one first impression we get
with the exact technical audience we need. So the tenet **floors** (integrity,
no-silent-loss, don't-crash, honest observability) *and* the **mission** (M0,
field-proven) are done before any launch. Feedback is sought — on something that
already stands up, not as a substitute for hardening.

**The build principle (B8):** best-in-class *components*, a novel *composition*.
We do not reinvent primitives (crypto, transport, codec); we adopt the strongest
proven ones and reserve novelty for the composition and incentives — where M0
lives — proven by spec + an **external** red-team, never self-graded.

## Where we are now (the honest status)

- **Storage plane — field-proven at scale.** Cross-network publish/fetch,
  erasure-coded durability with failure-domain-aware placement + dispersion audit,
  capacity pledging/spill, mutual-TLS pinned identity, encrypted manifests +
  care-links, a quorum chain, web UI/observatory, desktop client. The silent-loss
  floors and the reprovide/config-drift gaps are fixed and field-proven;
  cross-network hole-punching is proven through cone NAT in an automated harness.
- **Trust plane — the M0 mechanism is BUILT and internally hardened.** The genuine
  composition shipped (PRs #117–#127): a verify-without-fetch proof-of-retrieval, a
  proof-of-**space-time** bond (an identity-bound sealed plot × a Wesolowski VDF,
  persisted, so N Sybils cost N real disks), standing as the time-integral of bond +
  audit, fork-choice reconciliation so partitions heal, and provable equivocation
  that slashes double-signers. The **H1–H6 systemic hardening pass is complete** —
  every standing/consensus/DHT/privacy surface has a shipped mechanism + inverted-PoC
  regression + the Invariant-A/B guardrails.
- **The mission was reframed — the composition reset.** M0's Sybil corner is a
  **systemic claim held in tension**, not a Sybil-proof primitive (impossible by
  Douceur). It is stated as **C1 (no discount)** — forging a fraction *q* of standing
  costs ≈ *q*·`C_honest`, where `C_honest = disk × address-diversity × time ×
  served-demand` (non-substitutable) — **plus C2 (no quiet capture)** — the
  concentration metric keeps the minimum colluding *operator* set above *k*.
  Durability (S7) is **fused into the same budget** (one ledger). Full spec:
  [`docs/design/m0.md`](docs/design/m0.md).
- **The research commission has been answered.** The two constructions we'd routed
  to research both **exist**: center-less **proof-of-repair** (a composition of
  proven parts — unblocks durability) and a **non-globality metric** (ZK threshold
  predicate). Durability is decided **finite-but-renewable**, not "perpetual." The
  genuinely-hard residue collapsed to **one** named open problem — the shared-content
  sealing boundary (see the research frontier below). Decisions:
  [`docs/decisions.md`](docs/decisions.md).

## The forward tracks (what replaces the gate spine)

Three kinds of work remain. **Build** makes the decided directions real; **verify**
is the gate to declaring M0 *held* (not merely built); **research frontier** is the
handful of items that need a new result, not a decision.

### Build tracks

- **H7 — durability, funded and verifiable (next).** The S7 spine: per-object
  credit escrow + auto-skim + rarest-shard bounty, and **center-less
  proof-of-correct-repair** built as the A1 composition (transparent binary-field
  polynomial commitment [FRI-Binius] for correctness + Shacham–Waters PoR for
  retrievability + a DAS quorum for center-less checking). Ships as an explicit
  **finite-but-renewable** contract; **instrument `g`** (credit-denominated cost
  decline) as the number that decides perpetual-vs-finite. *(#95.)*
- **H8 — metadata-layer privacy.** The D-PRIV build track: mixnet transport +
  **private DHT lookup** (server-held-DB PIR, Peer2PIR model — routing/provider
  records only; blobs ride the mixnet) + **D3 issuance-mixing** (route token
  issuance over the content-blind relay from an ephemeral identity, epoch-batched)
  to close the publisher IP+timing link. Bounded by the anonymity trilemma; a stated
  product tradeoff, not a blob-layer absolute.
- **H9 — pluralistic takedown, provably non-global.** Signed subscribable label
  layer + a **CT-style append-only transparency log** (every honored revocation
  committed, with inclusion/consistency proofs) + a narrow opt-in denylist + the
  **non-globality metric** (survivor Nakamoto-coefficient published as a certified
  lower bound `≥ t` via a ZK threshold predicate). Low urgency.
- **D-DEMAND — the blind demand receipt.** Standing tracks *witnessed* demand
  priced on **cost-to-wash, never receipt count**. Prototype-first (issue →
  PoR-bound delivery-ack → bank → redeem) with fee-burn + bonded-fetcher credential,
  then a self-dealing red-team. Demand *authenticity* is a Douceur limit (re-priced,
  not proven away); unlinkability depends on H8's D3.
- **C2 metric wiring.** Compute the concentration metric — Nakamoto-coefficient /
  cost-to-corrupt over **bond-distinct operators** — from the **committed on-chain
  bond ledger, not gossip** (kills the skew half). One measurement feeds three seams
  (consensus shed, private-lookup committee certification, C2).
- **Registry economics (Gate-5 lineage).** Registry-only mode (#47) and keeping
  public registries cheap to run (#48) — a costless public good, decoupled from
  full storage/serving.

### Verify tracks — the gate to "M0 held"

M0 is *held* only when an **external** party attacks the built composition and it
survives at declared parameters. Self-graded does not count.

- **Multi-machine field test (R1, #52).** Bonds, tokens, and consensus across real
  machines and real NAT — the trust plane earning the rigor the storage plane has.
- **External red-team vs C1/C2.** A fresh, no-memory adversary attacks the
  *systemic* claim and the seven composition **seams** ([`m0.md`](docs/design/m0.md)
  §7), not isolated primitives — a primitive failing a standalone "Sybil-proof" test
  is Douceur, expected, not an M0 failure. A seam *held in tension* (bounded cost,
  documented residual) is a pass; a seam silently assumed closed is the failure mode.

### Research frontier — needs a new result, not a decision

- **⭐ The shared-content sealing boundary — the one surviving economy of scale.**
  Plain PoR over *shared* erasure-coded shards lets one physical copy answer for N
  pledges (γ→1/N); closed only by **identity-keyed PoRep sealing of arbitrary useful
  shared data**, which is not yet publicly-verifiable + timing-free +
  trusted-setup-free. **silt is not exposed today** — standing comes from a dedicated
  identity-keyed bond plot, not the shared shards — but *fusing* served content into
  standing without leaking γ→1/N is the highest-leverage open question. An
  academic-collaborator task (`m0.md` §10).
- **MSR / regenerating-code proof-of-repair.** The A1 composition is airtight for
  plain-RS reconstruction; no published construction specializes it to MSR/Clay. Off
  today's critical path (silt ships plain-RS).
- **Byzantine size-estimation under adversarial NodeID placement.** The C2 sampling
  tolerance is proven for *random* Byzantine placement; a stake-splitter chooses its
  NodeIDs, degrading it by an amount the literature does not quantify.

## What "M0 held" means

M0's Sybil corner is not a primitive to be proven Sybil-proof — that is impossible.
It is **held** when, at the network's declared parameters, no strategy earns
consensus-controlling standing for less than `q · C_honest` (**C1**), and the
concentration metric keeps the minimum colluding operator set above *k* (**C2**),
with the §7 seams either closed or *held in tension with a documented, bounded
residual*. C1 is a **theorem *under* the B5 hypotheses H1–H3** (a direct-product
bound) — *conditional*, not unconditional: the per-identity Alwen–Blocki lift is
unproven, the shared-content H3 gap (γ→1/N, #182) is open, and in shipped code only
the bond (D) axis gates standing so far (§ "where we are"). C2 is a **measurement
bounded by an impossibility result** (Kwon) — held, not closed, by design. The
verdict is rendered by the external red-team + the field test, together.

## The resolver layer ("Aslan" — separate product)

Meaning lives above the infrastructure, in a separate codebase: name/description/
tags → (root, manifest key). Silt ships zero Aslan code, ever. See
`docs/aslan-boundary.md`.

## Release engineering — the march to V1

The `0.1.x` / `0.2.x` tags were **experimental / learning releases**, not steps
toward V1 — treat them as archaeology. The real cadence has three stages:

- **Learning phase (past).** Everything through the experimental 0.x tags:
  proving the architecture. Detail lives in `docs/buildlog/`.
- **Feature-complete → `0.9.0`.** When every V1 build track's *mechanism* is built
  (the floors plus the M0 composition, durability, privacy, takedown) we cut
  `0.9.0` as the release-candidate line and harden it in the field.
- **`1.0.0` = V1.** Cut only once the tenets are field-proven multi-machine (R1)
  **and** the external red-team verdict holds — a *true* release candidate,
  signed/notarized/checksummed. This is the first release we stand behind publicly.

Mechanics when ready: move CHANGELOG "Unreleased" into the version, tag, and the
release workflow builds + publishes binaries; add code-signing/notarization
(macOS) + a checksums file first. See `docs/release-checklist.md`; website/DNS in
`DEPLOYMENT.md`.
