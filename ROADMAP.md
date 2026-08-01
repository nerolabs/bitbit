# Silt Roadmap — from swarm to product

Where this is going (per Andrew, 2026-07-25), structured into
milestones. M1–M8 are done; this file governs what comes next.

## Tenets are the destination; this roadmap is the path

**V1 is defined by the tenets, satisfied and field-proven — not by a
feature list.** [`docs/TENETS.md`](docs/TENETS.md) is the destination:
what "good" is, stated as principles, outcomes, and explicit stances on
the hard tradeoffs. This roadmap is the *path* to that destination, and
the open [issues](https://github.com/nerolabs/silt/issues) are the gaps
and deviations we find while walking it. The relationship is one-way and
strict:

- **Tenets guide the roadmap.** Every milestone and phase below exists to
  advance one or more tenets; if a step serves no tenet, it does not
  belong here. Every open issue should trace to the tenet it advances or
  the bright line (Part VI) it defends — that traceability is how we know
  the backlog is the path to V1 and not drift.
- **A tenet gates V1 as a *principle*, never as a *mechanism*.** "Integrity
  is non-negotiable" (S1), "no silent-loss shapes" (S3), "cheap to
  participate" (S6), "reward tracks value" (Don't #7) are gates. *Which*
  algorithm, economic mechanism, or role satisfies them — and *when* it
  ships — is a roadmap decision, explicitly held in the tenets' **Evolving**
  bucket (Part IX). This is what keeps "V1 must adhere to the tenets" from
  silently expanding V1's scope: e.g. "reward tracks value" is canon, but
  whether V1 ships the real proof-of-retrieval economy or a clearly-labeled
  non-authoritative placeholder is a sequencing call made here. **Decided
  2026-07-31: the trust plane IS a V1 pillar** (resolving tenets Open
  Question #3), so V1 ships a *real, field-proven* proof-of-retrieval
  economy and chain — not a labeled placeholder. This is the deliberate,
  higher-cost path, chosen because the launch must be credible from day one
  (see the launch track below).
- **Release is gated by proof (tenet R1).** A tenet is only "met" for V1
  when it is field-proven, not sim-proven-only. The current gate is the
  multi-machine scale re-test of the #43/#46 fixes (integrity S1 held; the
  open question is availability — S2/S4/S5).

## The product stance

- **Daemons are infrastructure, not content.** A daemon stores and
  serves anonymous, encrypted chunks. It cannot know what it hosts:
  chunks are ciphertext, manifests are ciphertext, and the top-level
  identifier is an opaque hash carrying zero metadata. Resolving
  identifiers into human meaning (names, descriptions) is deliberately
  NOT this system — a separate layer, like DNS atop IP, built by
  whoever wants to build it.
- **Capacity is pledged, not assumed.** `silt daemon -capacity 2G`
  contributes exactly that much. The network continuously knows its own
  total size. No daemon needs any whole file — just chunks.
- **Writes are earned.** Publishing to the chain requires coordination
  among daemons with established reputation (M7's audit history is the
  reputation seed). No single node's say-so writes a block.

## Milestones

### M9 — Capacity (NOW)
Bounded stores (`-capacity 2G`), refusal when full, spill-over
placement (try the next-closest node when one refuses), stripe
anti-affinity (never two shards of one stripe on one node when
avoidable — one node death costs at most one shard per stripe), and
network capacity accounting: every message piggybacks the sender's
used/total, and each node estimates network size from XOR-space
density (math note 06) to compute total network capacity.

### M10 — Identity, TLS, discovery (DONE)
- NodeID = SHA-256(Ed25519 public key); all swarm connections are
  mutual TLS 1.3 pinned to the key (adapters/identity, tcpnet);
  registry over pinned HTTPS. No CA, no accounts — the identity IS the
  keypair, and reputation can't be shed without it. Frames claiming a
  sender other than the handshake's key are dropped.
- Discovery in layers (adapters/discovery): -bootstrap peer strings →
  -dns-seed TXT records → peer exchange, with the learned address book
  persisted to peers.json for flagless warm restarts.

### M11 — Encrypted manifests ("encrypted at all levels") (DONE)
Manifests are sealed blobs: layout encrypted under a layout key, with
the decryption material boxed under a content key inside. Both keys
derive one-way (HKDF) from the link key — the share handle is
`silt:v1:root:key`, and its degraded form `siltcare:v1:root:layoutkey`
grants repair/audit WITHOUT decryption (see docs/math/07-key-hierarchy.md).
Link keys are content-derived, so convergent dedup extends to the links
themselves. Infrastructure relays ciphertext end to end; caretakers do
their whole job inside the layout ring.

### M12 — The chain (DONE)
The registry is an append-only block chain maintained by the daemons
(core/chain, node validator role). Blocks hold entries only (manifests
stay sealed off-chain); commits require a quorum of Ed25519
attestations from validators whose reputation (credit.Reputation:
audits + serving) clears the threshold — each validator judging by its
OWN ledger. Replicas re-validate everything, latecomers sync and
re-check history, chainstore persists across restarts, and chainhost
fronts the chain as ports.Registry so swarm add/get work unchanged.
Not PoW, and honest about it: see docs/math/08-quorum-chains.md.

### M13 — Web frontends (DONE)
`daemon -ui ADDR` serves an embedded localhost web UI (cmd/silt/ui,
go:embed, JSON API on the same port, zero extra runtime):
- **Daemon dashboard**: pledge used/total, chunks, served bytes,
  self-estimated network size/storage, chain height, and the opaque
  top-level roots it holds shards of. Auto-refreshes.
- **Publish page**: drag a file → scatter via an in-process ephemeral
  client (staging never touches the pledge) → returns the silt link
  AND the care link.
- **Fetch page**: paste a link → fetch, verify, decrypt, download.
- **Observatory**: aggregates any list of daemon UIs — observed
  capacity, serving bandwidth (served-bytes delta), per-daemon roster,
  and every registered file with its shard spread (health = daemons
  hosting). No privileged view; it's knowledge any participant can
  assemble.

### M14 — Desktop client (DONE)
`silt client`: one binary that consumes AND serves (pledges disk by
default — every client a node), keeps a link-book library (the files
you hold keys for; the network's other identifiers stay opaque to you,
the Aslan boundary made visible), bootstraps via discovery, and opens
the web UI in your browser. build.sh cross-compiles Mac (Intel + Apple
Silicon), Windows, and Linux (amd64 + arm64) from one source, CGO off,
5 self-contained 8-10 MB binaries. Tray/Tauri wrapping is optional
polish that consumes the binary unchanged. See docs/desktop-client.md.

## The resolver layer ("Aslan" — separate product)
Meaning lives above the infrastructure, in a separate codebase with
its own distributed record chain: name/description/tags → (root,
manifest key). See docs/aslan-boundary.md for the full boundary
design. Silt ships zero Aslan code, ever.

## Reputation inputs (feeding M12)
- Storage honesty: M7 audit pass rate.
- Serving bandwidth: bytes-served rate (a storage-only hoarder that
  serves at a trickle is a freeloader too). The M9 capacity-gossip
  pattern extends to bandwidth totals.
- Endgame: every client is also a serving node — the M14 client
  pledges disk + uplink by default.

## Open questions being explicitly deferred
- Economic settlement across the chain (M5 credits become chain state
  eventually).
- ~~NAT traversal (daemons on home networks) — likely relay-assisted,
  post-M12.~~ **Done** (#27): mDNS → reachability (now also STUN-style
  observed-endpoint) → content-blind relay → cross-network field-proof →
  hole-punching. The relay is built and field-proven; hole-punching (relay
  demoted to rendezvous) has its punch **proven through cone NAT** (falls
  back to relay on symmetric) in the automated Docker NAT harness, gated in
  CI. See the launch track below and `docs/design/cross-network.md`.

## Prioritized sequence (agreed 2026-07-25)

Ordered by value = risk retired + capability unlocked, weighted by
cost-of-delay, over effort. The governing decision: **the placement
backbone lands *before* any public launch** — it changes placement/
routing wire semantics, and migrating a live network of independent
operators through that is far more expensive than doing it while the
network is empty.

**Phase 0 — cheap wins that de-risk everything after (days).**
1. Repair preserves stripe anti-affinity (closes a verified gap).
2. `-race` build + a coverage floor in CI.
3. Dispersion *measurement* — surface distinct-hosts-per-stripe in the observatory.
4. Docs-ship-with-code CI check + auto-rendered `website/roadmap.html`.

**Phase 1 — the durability backbone, BEFORE launch (main engineering bet).**
5. Column-based placement (`docs/design/column-placement.md`) — **done**: placement, retrieval, repair, and audits operate on columns; a column is found in one lookup and within-column anti-affinity is structural.
6. Failure-domain-aware placement — **done**: nodes gossip a domain label; publish and repair both spread columns across distinct domains (best-effort), roughly halving worst-domain co-residence. Bounds the cross-column co-residence #5 left open.
7. Dispersion audit — **done**: caretakers tally the domains that actually hold each stripe's columns and re-spread any stripe a single domain failure could break, until no domain loss drops it below k. Also surfaces the per-stripe domain spread the content-blind observatory can't compute.
8. Demand-responsive dispersion — **done** (push half): a node whose chunk runs hot pushes leased cache copies to more hosts (across domains); reads refresh them, cooldown expires them, so hot files fan out and contract elastically. Pull-cache tier (fetchers cache near readers) is the noted follow-up.

**Phase 1 is complete** — the durability + scale backbone is in.

The launch track is **harden-first** (Andrew, 2026-07-31 — a deliberate
re-sequencing of the earlier "community-feedback-first" plan). The reasoning:
a half-baked experimental drop on a project this ambitious — content-addressed
storage + a reputation-quorum chain, in a space crowded with "AI/web3 storage"
noise — reads as a *poser build* and burns the one first impression we get
with the exact technical audience we need. So the first public appearance must
be **credible and impressive from day one**: the tenet *floors* AND the harder
*ceilings* (Sybil training-wheels, real proof-of-retrieval, full DoS
resistance, cross-network hole-punching) are done, and the chain is
**field-proven**, BEFORE any launch. Feedback is still sought — but on
something that already stands up, not as a substitute for hardening. (This
also re-aligns with `launch-plan.md`'s pre-announcement gate.)

The pre-public waves, ordered (each item traces to a tenet or catalog risk;
the open issues are the live task list):

**Wave 0 — silent-loss floor.** #64 data-shard placement verify-≥k
(the #60 disease on DATA shards), field-proven on the rig; close #46. S3/B7.

**Wave 1 — panic/parse/local-API floor (cheap).** Panic-recover + fuzz the
decoders (A5); bound declared manifest chunk count/size (A6); lock the local
UI/JSON API with Origin/Host allow-listing + a per-daemon token (I1). Frame
bounds and relay caps already exist.

**Wave 2 — full resource-accounting + durability-under-load.** The per-peer
resource-accounting framework (catalog §A, A1–A14), repair-storm protection
(A15: probe-before-repair, backoff, concurrent-repair cap), and the post-cap
relay throughput / fetch-retry / register-after-distribute work (#65).

**Wave 3 — cross-network hole-punching (#27).** mDNS → reachability → relay →
two-Macs is done; hole-punching is the remaining structural piece, demoting
the relay from every-byte to rendezvous. Wire-sensitive, so it lands before
the network grows. Design in `docs/design/cross-network.md` (rendezvous split
from NAT traversal; relay *capability* in the binary, public *deployment*
stays throwaway per R3).

**Wave 4 — the trust plane, as a V1 pillar (the long pole).** Ratify the
tenets (#54); field-test the chain/consensus multi-machine (#52 — today it is
sim-proven only, and R1 demands field proof); build **real (non-toy)
proof-of-retrieval** to replace the self-admittedly-gameable credit ledger
(Don't #7); and stand up the **Sybil training-wheels + launch-window controls**
(risk 15: time-boxed seeded anchors, gated reputation ramp, maturity-scaled
quorum thresholds, shed on *measured* decentralization). With reputation-gated
writes now in V1, Sybil→quorum-capture (D3) is a direct V1 risk; PoW/stake
stays deferred, so the training-wheels carry that weight. Resolve publisher
privacy here (risk 14 / catalog F1) — direction: **blind-signed publish
tokens** (a Chaumian-style token unlinks a publish from the durable
reputation key while preserving the fee/anti-spam economics).

**Wave 5 — cut V1 (complete + signed) & solicit.** A signed/notarized,
checksummed release; publish the **threat model** (`docs/threat-model.md`) as
honest disclosure *and* an invitation to break it; multi-process e2e tests
over real daemons; then narrow, technical outreach ("help us break this").
Decide the legal posture (entity, jurisdiction) informed by the engagement —
still not on spec, but now *after* a credible artifact exists, not instead of
one.

**Wave 6 — economics & registry cheapness (#47/#48)**, co-designed with the
Wave 4 economy; denylist distribution/subscription; pull-cache tier.

The pre-launch gate is **"the tenets, field-proven"** (R1) — floors and
pillars both. Everything expensive or irreversible still waits for proof, but
"proof" now means *we* proved it stands up, not that we shipped it half-built
and asked the crowd. See `docs/launch-plan.md` and `docs/risk-register.md`.

## Release engineering (V1)
- **The target is V1** (harden-first). Any earlier `0.x` tags were learning
  misfires from before the harden-first decision — not real launches; ignore
  them and aim straight at V1, updating everything at that point.
- **Cut the V1 release + publish signed binaries** — not done yet
  (deliberately: gated on the tenets being field-proven, floors and pillars).
  The site's downloads point at build-from-source until then. When ready: move
  CHANGELOG "Unreleased" into a dated version, `git tag v1.0.0 && git push
  origin v1.0.0`; the release workflow builds the binaries and publishes a
  GitHub Release. Add code-signing/notarization (macOS) and a checksums file
  first. See `docs/release-checklist.md`.
- Website publishing + DNS: see DEPLOYMENT.md (Netlify, apex at Namecheap).

## Storage-layer hardening (Phase 1 — before launch)
Details in `docs/design/column-placement.md` and `BACKLOG.md`. Moved
ahead of launch (was "post-launch") because the placement wire format
must not ossify on a live network: column-based placement, then
failure-domain-aware placement, then the dispersion audit.
