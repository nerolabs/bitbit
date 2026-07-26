# Silt Roadmap — from swarm to product

Where this is going (per Andrew, 2026-07-25), structured into
milestones. M1–M8 are done; this file governs what comes next.

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
- NAT traversal (daemons on home networks) — likely relay-assisted,
  post-M12.

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

The launch track is deliberately **community-feedback-first** (Andrew,
2026-07-25), a re-sequencing of the earlier "form an entity + pay for an
audit before launch" plan. For an early, unproven infrastructure project,
spending the scarcest resources (money, legal exposure) on formalities
ahead of validation is backwards. Instead: ship something real and
honestly labeled, let the community be the first review, and let what we
learn decide whether the formal commitments are worth making.

**Phase A — Ship & solicit (the feedback release).**
8. Cut **v0.1**, labeled *experimental, unaudited, for evaluation and
   feedback — not for data you can't afford to lose.* Not signed/notarized
   (a wider-push concern); checksums + build-from-source suffice for a
   technical audience. Andrew does a personal review + hardening pass
   before any outreach.
9. Publish the **threat model** (`docs/threat-model.md`) for community
   review — honest disclosure *and* the invitation to break it. At this
   stage the community is the security review.
10. Market for exactly one thing: **feedback.** Narrow, technical
    audiences ("help us break this," not "use this"); the launch plan is
    re-pointed at that single goal.

**Phase B — Learn & decide (feedback-driven).**
11. Triage what comes back: bugs, design critique, security findings, and
    whether anyone cares.
12. **Then** decide the legal posture — entity, jurisdiction, or stay a
    pure code-publishing project — informed by what the early engagement
    reveals, not made blind. No entity is formed on spec.

**Phase C — Harden & formalize (only if B justifies it).**
13. **Cross-network reachability — bootstrap rendezvous + NAT traversal**
    (issue #27). The thing between an impressive demo and person-to-person
    use: two home nodes on separate networks can neither find nor connect
    to each other today. Needs a rendezvous (community DNS seeds / relays,
    never SiltHQ-run) plus NAT traversal (relay of ciphertext — content-
    blind by design — with hole-punching later). Arguably the highest-value
    engineering item once feedback validates the direction. Design in
    `docs/design/cross-network.md`: splits rendezvous from NAT traversal,
    keeps the relay *capability* in the binary while the public
    *deployment* stays throwaway dev scaffolding (never project-run), and
    sets the build order (mDNS → reachability check → relay → dev node →
    prove two Macs → hole-punching).
14. Security hardening — Sybil/eclipse resistance, ungameable reputation,
    real (non-toy) proof-of-retrieval.
15. Multi-process e2e integration tests over real TCP/daemons.
16. Denylist distribution/subscription (completes the abuse-handling
    story); pull-cache tier for demand dispersion.
17. Formal commitments *if warranted by traction and feedback*: legal
    entity + DMCA agent, an independent paid audit, signed/notarized
    binaries, and a wider launch.

The gate that used to be "entity + paid audit" is now **"publish the
threat model for community review."** Everything expensive or irreversible
waits for evidence. See `docs/launch-plan.md` and `docs/risk-register.md`.

## Release engineering (v0.1)
- **Cut the v0.1 release + publish signed binaries** — Phase 3. Not done
  yet (deliberately). The site's downloads point at build-from-source
  until then. When ready: move CHANGELOG "Unreleased" into a dated
  version, `git tag v0.1.0 && git push origin v0.1.0`; the release
  workflow builds the binaries and publishes a GitHub Release. Add
  code-signing/notarization (macOS) and a checksums file first.
- Website publishing + DNS: see DEPLOYMENT.md (Netlify, apex at Namecheap).

## Storage-layer hardening (Phase 1 — before launch)
Details in `docs/design/column-placement.md` and `BACKLOG.md`. Moved
ahead of launch (was "post-launch") because the placement wire format
must not ossify on a live network: column-based placement, then
failure-domain-aware placement, then the dispersion audit.
