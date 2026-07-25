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
7. Dispersion audit as an enforced invariant (built on the Phase 0 measurement).
8. Demand-responsive dispersion — elastic replication so hot files fan out across more hosts under load and contract on cooldown (matters at worldwide scale).

**Phase 2 — production trustworthiness (feeds the external security audit).**
8. Security hardening — Sybil/eclipse resistance, hardened reputation, real (non-toy) proof-of-retrieval.
9. Multi-process e2e integration tests over real TCP/daemons.
10. Denylist distribution/subscription (completes the abuse-handling story).

**Phase 3 — launch enablement (when the above are solid).**
11. Cut v0.1 + signed/notarized binaries + checksums, then execute the launch plan.

**Parallel throughout — non-coding, highest *overall* value, gates launch:**
form the legal entity + DMCA agent + legal read (long lead time), and
scope the independent security audit so Phase 2 feeds it. No wide launch
precedes these (see `docs/risk-register.md`).

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
