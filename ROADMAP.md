# Silt Roadmap

> **One spine.** The single source of truth for *what is done and what is next*
> is the GitHub **`V1` milestone** and its issues — each issue traces to the
> tenet or immutable it serves, and each carries a `gate-N` label. The pinned
> epic **[#94 "V1 — the gate spine"](https://github.com/nerolabs/silt/issues/94)**
> is the sequenced, forward-looking checklist (Gate 0→6, critical path 1→4→6)
> that indexes every gate's issues. This file is the *narrative*: why the work is
> ordered the way it is. It is not a tracker and does not duplicate issue state.
>
> Earlier planning used interim markers during the project's **learning
> phase**; they are retired in favor of this single milestone-and-issues spine
> (that detail lives in `docs/buildlog/`, not here). The `v0.1.x` / `0.2.x` tags
> are **experimental / learning releases**, not steps on the march to V1.

## Tenets are the destination; this roadmap is the path

**V1 is defined by the tenets, satisfied and field-proven — not by a feature
list.** [`docs/TENETS.md`](docs/TENETS.md) is the destination. This roadmap is
the *path*; the open [issues](https://github.com/nerolabs/silt/issues) under the
`V1` milestone are the gaps we find walking it. The relationship is one-way:

- **Tenets guide the roadmap.** Every gate below advances one or more tenets; if
  a step serves no tenet, it doesn't belong here.
- **A tenet gates V1 as a *principle*, never a *mechanism* — with one deliberate
  exception: M0.** "Reward tracks value" is canon, but *which* mechanism
  satisfies it is a sequencing call. The lone exception is the mission itself:
  **M0 — *holding* the trilemma (token-less, work-backed, unlinkable reputation)
  without trading a corner away — is not a feature that satisfies a principle, it
  *is* the reason silt exists**, so its *real* mechanism is in V1 by definition
  (see `docs/TENETS.md` Part 0/IX). M0 is *held* only when an **external**
  red-team suite (V3) denies all three failure modes — self-graded does not
  count.
- **Release is gated by proof (R1).** A tenet is "met" only when field-proven
  multi-machine, not sim- or single-host-only.

## The launch stance — harden-first

The first public appearance must be **credible and spectacular from day one**. A
half-baked drop on a project this ambitious — content-addressed storage + a
reputation-quorum trust plane, in a space crowded with "AI/web3 storage" noise —
reads as a poser build and burns the one first impression we get with the exact
technical audience we need. So the tenet **floors** (integrity, no-silent-loss,
don't-crash, honest observability) *and* the **mission** (M0, field-proven) are
done before any launch. Feedback is sought — on something that already stands up,
not as a substitute for hardening.

**The build principle (B8):** best-in-class *components*, a novel *composition*.
We do not reinvent primitives (crypto, transport, codec); we adopt the strongest
proven ones and reserve novelty for the composition and incentives — where M0
lives — proven by spec + red-team suite, never hand-waved.

## What is already built (context, not a tracker)

The storage plane is field-proven at scale: cross-network publish/fetch,
erasure-coded durability with failure-domain-aware placement and a dispersion
audit, capacity pledging/spill, mutual-TLS pinned identity, encrypted manifests
+ care-links, a quorum chain, web UI/observatory, and a desktop client. The
silent-loss floors (#46/#60/#64) are fixed and field-proven; the reprovide and
config-drift gaps (#69/#71) are closed. Cross-network hole-punching is proven
through cone NAT in an automated Docker harness. **The trust plane's real M0
mechanism (Gate 4) is now built and internally adversarially tested** — a
verify-without-fetch proof-of-retrieval, a proof-of-**space-time** bond (a
space-hard identity-bound plot × a Wesolowski VDF, persisted, bound so N Sybils
cost N real disks), standing as the time-integral of bond + audit, fork-choice
reconciliation so partitions heal to the heavier-standing chain, and provable
equivocation that slashes double-signers — replacing the earlier
honestly-labeled placeholders (the space-lite in-RAM bond seal; the
fetch-to-grade PoR). It is proven at the unit, in-process simulation, and
real-daemon end-to-end tiers (including a two-validator consensus commit over
TCP). What remains before it can be called *proven*: **independent adversarial
review** (an acceptance pass then a red-team pass — see [`docs/reviews/`](docs/reviews/))
and the multi-machine field test; some hardening items are honestly recorded in
the CHANGELOG and design §6. M0 ships proven or it does not ship.

## The V1 gates (ordered by dependency and exposure)

The critical path is **Gate 1 → Gate 4 → Gate 6**; Gates 2 and 3 parallelize.
Gate 4 is the long pole and is where the schedule risk deliberately sits — it is
the mission.

**Gate 0 — Reconcile the spine.** Close shipped issues (#78/#79), create the
`V1` milestone, re-file every open issue against the tenet/immutable it serves.
Retire the M/Wave/Tier prose (history → buildlog). *Done-is-done matches
reality.*

**Gate 1 — Floors (cheap; blocks everything).** Panic-recover + fuzz the
decoders (A5, #87); bound declared manifest chunk count/size (A6, #88); lock the
local UI/JSON API with Origin/Host allow-listing + a per-daemon token (I1, #89 —
CORS is `*` today). You cannot field-test a chain on a daemon that crashes on a
malformed frame, and a crash surface kills the "impressive" claim on contact.

**Gate 2 — Durability under load.** Post-cap relay throughput, fetch-side retry,
register-after-distribute (#65). The silent-loss half is already done.

**Gate 3 — Cross-network hole-punching (#27).** Cone-NAT punch is proven;
wire-sensitive, so it lands before the network grows, demoting the public relay
from every-byte to rendezvous. Design in `docs/design/cross-network.md`.

**Gate 4 — The car: the mission mechanism, real and multi-machine (the long
pole).** Replace the labeled placeholders with the genuine M0 **composition**,
built from best-in-class components (B8), and prove it. M0's Sybil-resistance is
the systemic claim **C1 (no discount) + C2 (no quiet capture)**, held in tension —
not a single Sybil-proof primitive (Douceur). **Status: the parts that make each
axis of `C_honest` (disk × address-diversity × time × served-demand) real are
BUILT and internally hardened (PRs #117–#126 + the H1–H6 hardening pass); the
internal pass is complete. The remaining bar is external re-verification against
the C1/C2 claim + the multi-machine field test (4e/#52) — which together render
M0's yes/no verdict.** The sub-items:
- **4a (#90) — Real proof-of-retrieval / proof-of-storage.** Adopt a published,
  peer-reviewed scheme (the compact-PoR / PDP / proof-of-space family); the
  novelty is not the primitive but the binding.
- **4b (#91) — Real work-backed bond (the hardest piece).** Replace the
  space-lite iterated-SHA seal with a genuine, memory-hard / proof-of-space
  construction: cheap for a challenger to verify, expensive to fake,
  identity-bound.
- **4c (#92) — Identity-bound, time-integrated, *unlinkable* standing.** Bind 4a/4b to
  identity and time so standing is the integral of sustained proof over the
  non-substitutable axes (disk × diversity × time × demand), coin- and stake-free,
  while the blind publish token keeps publishing cryptographically unlinkable from
  that standing. *This is the core of M0's C1 (no discount). It holds only if the
  **composition** satisfies C1 + C2 under an **external** red-team (V3/B8) — a
  primitive passing in isolation is not enough, and a primitive failing in
  isolation is expected (Douceur), not an M0 failure.*
- **4d (#93) — Persistence + issuer distribution.** Bond and RSA issuer key
  persist across restart; on-chain issuer registration.
- **4f (#100) — Consensus equivocation detection + slashing + fork handling.**
  Today a validator can double-sign at a height with no penalty, and a fork is
  unrecoverable; slashing covers only storage-audit failures. Anti-persona #14
  ("equivocate in consensus") is an outcome we *deny*, so the defense is Gate-4
  work, not a footnote (surfaced by the build-vs-intention audit, 2026-08-02).
- **4e — Multi-machine field test of the whole trust plane (#52).** The R1 gate:
  bonds, tokens, and consensus across real machines and real NAT. Sim + one host
  is not "done."

**Chain-permanence prerequisites — CLOSED.** The chain is append-only with no
reorg, so a wrong record shape written first is *unrecoverable*. The
build-vs-intention audit (archived at
`/archive/reviews/build-vs-intention-2026-08-02.md`) found three, all now fixed and
merged: private publish is the default so the chain writes no permanent
`Publisher→root` identity map (#97, reinforced by H6's private-by-default publish);
`Block` carries a hash-committed version era so a record-shape change can't
silently hard-fork (#98); and the `Gated` registry no longer hard-requires a
Publisher (#99). The current M0 design constraints live in
[`docs/design/m0.md`](docs/design/m0.md) (superseded pre-code notes are in
`/archive/design-history/gate4-m0-mechanism.md`).

> **The binding is the hard part, not the primitives.** "Adopt best-in-class,
> don't invent" (B8) does *not* make Gate 4 easy — it *relocates* the research
> to the **binding**: identity-bound, time-integrated, cheaply-verifiable
> standing with unlinkable publishing on top. No drop-in library gives that
> composition; Chia/Filecoin spent years on the "cheap-to-verify,
> expensive-to-fake, identity-bound" corner alone, and we want that *and*
> hobbyist-cheap (S6) *and* unlinkable publishing. Gate 4 therefore owns a design
> doc + threat model *before* code — it is the mission, not a sprint.

**Gate 5 — Economics & registry cheapness (#47/#48), incl. durability that pays
for itself (S7).** *Designed alongside Gate 4, not after it* — Gate 4's
reputation buys *presence*, but S7 (the repair loop funded in equilibrium under
churn) is what decides whether files still exist in three years. This is the
wound Freenet/GNUnet died of — reputation without funded durability, one genre
before us; our closest ancestors are **Tahoe-LAFS** (convergent encryption +
erasure, no incentive layer) and **Freenet/GNUnet** (reputation, no funded
repair), and Gate 5 is where we refuse to repeat their hole. Scope:
registry-only mode, costless public rendezvous, denylist distribution/
subscription, the pull-cache tier (#47/#48), and the durability economics (S7,
#95) that make repair a service a publisher buys and a caretaker is paid to sell
— funded in equilibrium, not charity.

**Gate 6 — Cut V1.** Full docs-drift reconcile as the release gate (R2);
signed/notarized, checksummed binaries; publish `docs/threat-model.md` as honest
disclosure *and* an invitation to break it; decide legal posture; then narrow,
technical outreach ("help us break this"). **The independent security review
here is not a formality — it *is* the external adversary that certifies M0 and
B8** (top of `docs/risk-register.md`): the party that writes the attacks against
the Gate 4 composition must be someone other than its author (audit / bounty /
separate red-team). Until that suite runs and holds, M0 is claimed, not proven.

## The resolver layer ("Aslan" — separate product)

Meaning lives above the infrastructure, in a separate codebase: name/
description/tags → (root, manifest key). Silt ships zero Aslan code, ever. See
`docs/aslan-boundary.md`.

## Release engineering — the march to V1

The `0.1.x` / `0.2.x` tags were **experimental / learning releases**, not steps
toward V1 — treat them as archaeology. The real cadence has three stages:

- **Learning phase (past).** Everything through the experimental 0.x tags:
  proving the architecture. Detail lives in `docs/buildlog/`.
- **Feature-complete → `0.9.0`.** When every V1 gate's *mechanism* is built
  (the floors plus the M0 car), we cut `0.9.0` as the release-candidate line and
  harden it in the field.
- **`1.0.0` = V1.** Cut only once the tenets are field-proven multi-machine
  (R1) — a *true* release candidate, signed/notarized/checksummed. This is the
  first release we stand behind publicly.

Mechanics when ready: move CHANGELOG "Unreleased" into the version, tag, and the
release workflow builds + publishes binaries; add code-signing/notarization
(macOS) + a checksums file first. See `docs/release-checklist.md`; website/DNS in
`DEPLOYMENT.md`.
