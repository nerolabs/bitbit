# BitBit Roadmap — from swarm to product

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
- **Capacity is pledged, not assumed.** `bitbit daemon -capacity 2G`
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
`bitbit:v1:root:key`, and its degraded form `bitbitcare:v1:root:layoutkey`
grants repair/audit WITHOUT decryption (see docs/math/07-key-hierarchy.md).
Link keys are content-derived, so convergent dedup extends to the links
themselves. Infrastructure relays ciphertext end to end; caretakers do
their whole job inside the layout ring.

### M12 — The chain
Replace the hosted registry with an append-only block chain of
registry entries, maintained by the daemons themselves:
- Blocks hold top-level identifiers + manifest chunk pointers only
  (manifests stay chunked off-chain — the chain stays small).
- Reputation-weighted quorum: a proposer with sufficient reputation
  (audit passes, uptime, served bytes) proposes; a block commits only
  with attestations from a quorum of high-reputation daemons. New
  nodes earn write access slowly. Deliberately not proof-of-work;
  cheap, and honest about its trust model.
- Every daemon replicates the chain; ports.Registry is the seam, so
  core logic doesn't change.

### M13 — Web frontends (embedded in the daemon)
Go binary serves a localhost web UI (go:embed, zero extra runtime):
- **Daemon dashboard**: capacity used/total, chunks held and served,
  bandwidth, top-level IDs it holds shards of, audit record.
- **Publish page**: drag a file → chunk, encrypt, scatter, chain
  write → returns the bitbit link.
- **Fetch page**: paste a link → download, verify, decrypt.
- **Network observatory**: connects to many daemons; total capacity,
  every hosted top-level ID, per-stripe distribution health,
  aggregate serving bandwidth.

### M14 — Desktop client (Mac/Windows/Linux)
Recommendation: one Go binary with the embedded web UI (menu-bar /
tray wrapper per OS), not Electron — single codebase, ~10 MB, all
three platforms from `go build`. It bootstraps via M10 discovery,
shows reachable top-level IDs, one click to fetch. If a richer native
feel is wanted later, wrap the same Go core in Tauri.

## The resolver layer ("Aslan" — separate product)
Meaning lives above the infrastructure, in a separate codebase with
its own distributed record chain: name/description/tags → (root,
manifest key). See docs/aslan-boundary.md for the full boundary
design. BitBit ships zero Aslan code, ever.

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
