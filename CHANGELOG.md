# Changelog

All notable changes to Silt are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/), and the project aims to
follow [Semantic Versioning](https://semver.org/).

This log is published at [silthq.com/changelog](https://silthq.com/changelog.html).

## [Unreleased]

### Fixed
- **Publish no longer returns a link for a file the swarm can't rebuild**
  (#64, the data-shard twin of #60): placement verified that *manifest*
  chunks landed durably, but **data and parity shards were placed
  optimistically** — a column that no node accepted was ignored, so under
  load a stripe could silently erode below its erasure threshold `k` and the
  publish still returned a valid-looking link (in the field, f123 came back
  `stripe 0: only 9 of 16 shards, need k=10, unrecoverable`). Distribute now
  tracks per-shard placement and, before returning a link, **verifies every
  stripe kept enough placed shards to reconstruct** (accounting for the
  known-zero padding of a short final stripe); a column that lands nowhere is
  **retried with a fresh lookup** (as manifest chunks already were), and if a
  stripe still can't be made recoverable the publisher **fails loudly**
  instead of handing back an unrebuildable link. The same check closes the
  identical silent-loss on **uncoded files** (which carry no parity, so every
  chunk is required). Extends tenet **B7 — trust but verify; no optimistic
  operations** from the manifest path to all of publish.
- **Publish no longer returns a link for content it never stored** (#60,
  found in the 300-file scaling re-test): under load, once the network
  passed its capacity cap, a manifest chunk could be placed on *no* node
  (all candidates full or unreachable) yet publish still registered the
  root and returned a valid-looking link — ~14% of files were stranded
  behind dangling links (fetch failed with "manifest chunks unreachable").
  A manifest chunk that lands nowhere is now **retried with a fresh lookup**
  (these misses are usually transient — a relay hiccup once the nearest
  nodes cap out and load shifts onto NATed hosts), so publishes that used to
  strand now succeed; if it still can't be placed after several tries the
  publisher **fails loudly** instead of handing back an unretrievable link.
  This makes publish honor the new tenet **B7 — trust but verify; no
  optimistic operations.**
- **Ghost routing entries no longer break discovery at scale** (found in
  the 300-file scaling test, #43): every `swarm add`/`swarm get` ran as a
  short-lived client with a fresh identity, and nodes both routed to those
  clients and persisted them to `peers.json` — so a busy node's routing
  table filled with dead entries (in the test: 327 entries, 2 live, ~75%
  query timeouts), which broke provider discovery and made most fetches
  fail. Fixed at both ends: nodes persist only peers they have actually
  reached, and a short-lived client stamps its messages so peers never
  route to it.
- **Re-publishing identical content is idempotent** (#46): a failed
  publish could leave a root registered but return no link, and a retry
  then hit "root already published with different entry" — because
  idempotency compared the whole entry, including the per-invocation
  publisher identity. It now dedups on content, so a retry (or a second
  person adding the same file) succeeds instead of colliding.
- **NATed peers can actually converse** (found in the first real
  cross-network test, #27): the transport dialed a fresh connection per
  message, so a reply required dialing *into* the requester — impossible
  behind NAT, and bootstrap came back with zero table entries. Replies
  (and all traffic) now ride the live connection the peer opened, and
  dialed connections are kept and reused. Two corollaries: a wildcard
  bind (`0.0.0.0`/`[::]`) is never stamped on outgoing messages (it used
  to poison peers' address books with an undialable address — a new
  `-advertise HOST:PORT` flag lets a public box say what to gossip), and
  a daemon that registers with a relay now **re-bootstraps** through it,
  since its first join attempt may have been unanswerable. The
  reachability dial-back deliberately never reuses a connection — its
  meaning is "a fresh inbound dial landed" — so AutoNAT stays honest.
- **Relay-form addresses survive `-bootstrap`, DNS seeds, and
  peers.json** — peer strings split on the first `@`, not the last, so
  `ID@relay:RID@host:port` parses instead of being silently dropped.

### Added
- **Opt-in in-RAM read cache for hot chunks** (`-cache SIZE`, default off;
  #42): a cache hit serves trusted bytes from memory, skipping both the
  disk read and the per-read hash re-verification. Read-through LRU,
  cache-on-read only, and Delete evicts so purged content is never served.
- **The daemon caretakes content published through its own UI** by default
  (`-care-published`, #44): without a caretaker a published file's
  redundancy only decays as nodes churn — now the publishing daemon
  repairs its own roots, and both the UI and CLI say whether a caretaker
  is running.
- **Paginated, shard-sorted roots list in the daemon UI** (#45): the
  "identifiers this daemon holds shards of" table now paginates and sorts
  by shards held, instead of rendering every row (unusable at hundreds).
- **A public build log** — a chronological "how it was built and why"
  narrative under `docs/buildlog/` (dated Markdown entries), rendered to
  `website/buildlog.html` by `scripts/gen_buildlog.py` on the same
  source-of-truth pipeline as the changelog and roadmap (CI fails if the
  page drifts). It's the *reasoning* behind the design — the forks, the
  dead ends, the decisions — distinct from the changelog (what shipped)
  and the roadmap (what's next), and strictly about building the
  infrastructure. Seeded with three entries: the one-process/ports-and-
  adapters prime directive, the placement spectrum, and cross-network
  reachability. Linked from the site's docs and footer.
- **`-log LEVEL` — narrate the normal path, not just failures** — both
  `silt daemon` and `silt client` take `-log error|warn|info|debug`,
  opening the `debug.log` sink at that threshold; `-debug` is now
  shorthand for `-log debug`. At `info` the happy path narrates —
  `file distributed` (chunks placed), `block committed` (quorum reached,
  by proposal or broadcast), `file retrieved`, alongside the existing
  `stripe repaired`, `dispersion re-spread`, and `reachability verdict`
  — so a real-world run can be checked against how the system is
  *supposed* to behave, not only when something breaks, and without the
  debug firehose. Free when off and off the hot path (per-chunk store
  events stay at debug); core still logs through the `ports.Logger` port
  and imports nothing new.
- **Multi-process end-to-end tests over real TCP** (CI hardening,
  BACKLOG Phase 2) — a new `e2e/` suite builds the `silt` binary and
  runs three daemons as separate OS processes, publishes a 1 MiB file
  through the chain-backed registry over pinned HTTPS (driving a real
  consensus round to a committed block), then fetches it back across the
  swarm and asserts it returns bit-perfect. This exercises the whole
  wire path the in-process sim deliberately bypasses — exactly where
  #36's "a reply can never reach a NATed peer" bug hid until real
  sockets carried it. It runs as its own CI job; the unit and race jobs
  pass `-short` to skip the process spawning.
- **Relay discovery by gossip** (#27 polish) — a daemon offering `-relay`
  now stamps the service's dialable `host:port` on every outgoing
  envelope (borrowing the `-advertise` host when the relay listener is
  bound to a wildcard). Peers record these first-hand — a node only ever
  announces its *own* relay, and dialing pins the relay's identity, so
  gossip can direct but never impersonate. A daemon whose reachability
  verdict is NATed and that has no `-relay-via` adopts the first
  discovered relay automatically (and keeps watching until one appears):
  the two-Macs runbook now works with nothing but `-bootstrap`.
- **Two-slot address book: direct preferred, relay fallback** (#27
  polish) — the transport now remembers up to two addresses per peer,
  one direct `host:port` and one `relay:R@host:port`, instead of one
  slot the two forms fought over (an mDNS-learned LAN address used to be
  clobbered by the peer's relay stamp, sending house-mates through a
  relay on another continent). Dials try direct first — no third hop —
  and fall back to the relay within the same delivery; a direct address
  is dropped only when the relay fallback *reaches* the peer, which
  proves the address stale rather than the peer down. Contact gossip
  passes on the relay form when one is known (a relay-advertising peer
  is NATed, so its direct address is LAN-scoped hearsay); `peers.json`
  persists both slots. The reachability dial-back ignores relay
  addresses outright: reachable-through-a-relay is exactly what "public"
  must not mean.

- **Relay** (#27, step 3 — the universal NAT fallback) — a NATed daemon can
  now be reached across networks through any reachable node running
  `-relay ADDR`. The shape is libp2p Circuit-Relay-v2's, without the
  dependency: the NATed node keeps one registered outbound connection to
  the relay (`-relay-via RELAYID@HOST:PORT`, taken up automatically when
  the reachability verdict says NATed) and advertises `relay:R@host:port`
  as its address; a sender dials the relay, the target dials back, and the
  relay splices the two streams. Crucially, the sender then runs its
  normal pinned **end-to-end TLS handshake with the target through the
  splice** — the relay moves opaque bytes it cannot read, alter, or forge,
  so "a frame's sender is whoever the handshake authenticated" holds
  unchanged across a relay. Relaying is a capability, not infrastructure:
  opt-in, capped (concurrent sessions, per-peer sessions, per-session
  bytes), no relay baked into the binary, and the relay-operator metadata
  exposure is documented in the threat model. CI proves the full path on
  localhost — including both-peers-NATed, every byte relayed — because
  "NATed" is modeled honestly as "accepts no inbound connections".
- **`-debug` flag → `debug.log`** on both `silt daemon` and `silt client` —
  a leveled logger behind a new `ports.Logger` interface (core stays pure;
  the file sink is `adapters/logfile`). One grep-able line per event:
  transport failures (dials, handshakes, forged frames), node events
  (request timeouts, repairs, dispersion re-spreads, the reachability
  verdict), and daemon milestones (discovery, bootstrap). Quiet by default
  and free when disabled; with `-debug`, a failure in the field leaves an
  artifact that can be attached to a bug report. Groundwork for testing
  cross-network reachability (#27) on real networks, where failures are
  one-shot and remote instead of deterministic and replayable.
- **Zero-config LAN discovery** (#27, first rung of cross-network
  reachability) — `silt daemon` now announces itself on the local network
  and folds any peer it hears into the routing table, so two nodes in the
  same house find each other with no `-bootstrap`, no DNS seed, and no
  infrastructure. It's link-local multicast (the same idea as mDNS, scoped
  to the LAN by TTL), and self-authenticating: an announcement carries a
  peer's `ID@host:port`, and the TLS handshake still must present a key
  hashing to that ID, so a rogue beacon can misdirect a dial but never
  impersonate a node. On by default; `-mdns=false` opts out, and a
  loopback-only `-listen` disables it with a note (nothing on the LAN could
  reach a loopback address anyway). See
  [docs/design/cross-network.md](docs/design/cross-network.md).
- **Reachability check** (#27, our AutoNAT) — after bootstrap, a daemon asks
  a couple of known peers to dial it back at its advertised address. A
  landed dial-back both proves and delivers the verdict "public"; silence
  within a timeout is read, conservatively, as "behind NAT" (which only ever
  costs a relay we might not have needed, never a false claim of being
  reachable). The daemon logs the result and the dashboard shows it; the
  relay step will key its advertise-direct-vs-via-relay decision off it. No
  new message plumbing beyond two wire kinds; the pure core stays
  NodeID-only — reachability is simply whether the transport can deliver.

## [0.1.1] — 2026-07-26

Still early, experimental, and unaudited (see the
[threat model](https://github.com/nerolabs/silt/blob/main/docs/threat-model.md)).
This release is the first round of first-production-user feedback from 0.1.0,
fixed:

### Changed
- **Swarm registry docs & error messages** (#17) — the registry is
  *key-pinned HTTPS*, and now everything says so. The README swarm recipe
  and `silt daemon -registry` help use the `<ID>@https://host:port` form the
  daemon prints; passing a bare `https://` or an `http://` URL to a pinned
  registry returns a message that names the fix instead of a raw TLS error.
- **`silt info` summarizes by default** (#18) — root, mode, size, chunk and
  stripe counts, erasure params; the full per-shard dump moved behind
  `-shards`. It was a wall of hashes on any real-sized file.
- **`silt add` leads with the share link** (#19), labelled, and prints the
  care link after with a "repair only, cannot decrypt" caveat. The bare
  link stays on stdout so `silt add file` remains pipeable.
- **`silt daemon` pledges 5G by default** (#21), matching `silt client`, so a
  fresh daemon contributes measurable, countable storage instead of an
  unlimited pledge that read as 0 B of network storage. `-capacity ""` still
  means unlimited.
- **Shorter, easier-to-copy links** (#20) — a link now encodes its two
  32-byte values in compact base64url (43 chars each) instead of 64-char
  hex, so a share link is ~30% shorter (137 → 95 chars). Old hex links still
  parse.
- **Observatory** (#22) explains it shows only the daemons you list that run
  `-ui` (no swarm auto-discovery), that "daemons observed" is not the peer
  count, and now displays the swarm's self-estimate ("~N peers") right beside
  it so the two numbers reconcile.

### Added
- [**Build your own Silt test network**](https://github.com/nerolabs/silt/blob/main/docs/local-test-network.md) —
  a public, end-to-end local walkthrough (sims → a real multi-node swarm that
  survives a node death), with all of the above fixes baked in.

## [0.1.0] — 2026-07-25

**The first release — early, experimental, and unaudited.** Silt 0.1.0 is
published to get technical feedback, not to be trusted with data you can't
afford to lose. Please read the
**[threat model](https://github.com/nerolabs/silt/blob/main/docs/threat-model.md)** —
it names the weak parts on purpose (a toy proof-of-retrieval, unhardened
Sybil/eclipse, a quorum-not-BFT chain, and more) — and help us break it.
Binaries are **not** code-signed; verify them against the attached
`SHA256SUMS`.

### Added
- **Content-addressed storage** — every fragment is named by the SHA-256
  of its bytes; verification is intrinsic, so hosts are never trusted.
- **Erasure coding** — Reed-Solomon stripes (default any 10 of 16 rebuild
  the file); a repair loop restores redundancy as machines fail, and — like
  the initial placement — keeps each stripe's shards spread across distinct
  hosts as it rebuilds, so one machine's death never costs a stripe more
  than a single shard.
- **Encryption at every level** — chunks and manifests are both
  ciphertext; a file's share handle is a *link* (`silt:v1:root:key`)
  whose one-way key hierarchy also yields *care links* that grant repair
  and audit without the ability to decrypt.
- **The swarm** — Kademlia routing, provider records, and multi-node
  fetch over a deterministic simulator or real mutual-TLS sockets;
  identity is a keypair and a node's ID is the hash of its public key.
- **Column placement** — an erasure-coded file is placed by *column* (one
  shard position across every stripe), keyed by `hash(root‖col)`, so a
  whole column lands together: one host holds one shard of each stripe,
  a reader finds a column in a single lookup, and losing a host costs a
  stripe exactly one shard (up to n−k columns can go and the file still
  rebuilds). Placement, retrieval, repair, and audits all speak columns.
- **Failure-domain-aware placement** — a node can declare a failure-domain
  label (AS / rack / geo / operator) and gossips it; placement and repair
  spread a file's columns across distinct domains, so an entire domain
  going dark costs a stripe as little as possible — not just distinct node
  IDs, but distinct *domains*.
- **Dispersion audit** — a caretaker doesn't just keep a stripe *alive*, it
  keeps it *spread*: each sweep it confirms which domains actually hold each
  column, and if any one domain holds enough of a stripe that losing it
  would drop below the recovery threshold, it seeds extra copies into other
  domains until no single domain failure could break the file.
- **Demand-responsive dispersion** — storage flexes with popularity. A node
  that finds itself serving a chunk hard pushes leased cache copies to more
  hosts (spread across domains) so readers divide across more sources; when
  the reads cool off, the copies expire and the file contracts back to its
  baseline. A flash-popular file fans out without permanently hoarding
  capacity.
- **Capacity** — nodes pledge a fixed budget (`-capacity 2G`); placement
  spills over as nodes fill, and every node estimates the whole network's
  size from local gossip alone.
- **Proof-of-retrieval audits** — hosts are challenged to prove
  possession with a fresh nonce; those that keep the proof but drop the
  data are slashed.
- **The registry chain** — an append-only chain kept by the operators;
  blocks commit only with a quorum of attestations from validators whose
  reputation (audits + serving) is earned, not bought.
- **Genesis** — every fresh network is born carrying a founding manifesto
  in block 0, declared identically on every node.
- **Takedown by revocation** — illegal or unwanted content is removed at
  the availability layer, not the ledger: an append-only revocation
  record, committed by the same reputation quorum, makes compliant nodes
  no-op on a denied opaque root (refusing to store, serve, prove,
  announce, or repair it) and purge what they hold — never decrypting
  anything. Operators may also load a local denylist they choose to honor
  (`silt daemon -denylist`). The project ships the mechanism and no list;
  it operates neither the network nor the policy.
- **Web UI** — an embedded dashboard, publish/fetch pages, and a network
  observatory, served by the daemon.
- **Desktop client** — one binary that consumes and serves at once, keeps
  a link-book library, and runs on macOS, Windows, and Linux.
- **Public website** (silthq.com) with brand, docs, operator guide, and
  build-from-source instructions.
- **Continuous delivery** — PR previews, a `staging` environment, and
  production deploys from `main`; a public changelog rendered from this
  file.
- **Governance & strategy docs** — the fresh-eyes council, risk register,
  launch plan, safety/takedown model, and `GOVERNANCE.md`.

[0.1.1]: https://github.com/nerolabs/silt/releases/tag/v0.1.1
[0.1.0]: https://github.com/nerolabs/silt/releases/tag/v0.1.0
