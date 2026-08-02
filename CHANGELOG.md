# Changelog

All notable changes to Silt are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/), and the project aims to
follow [Semantic Versioning](https://semver.org/).

This log is published at [silthq.com/changelog](https://silthq.com/changelog.html).

## [Unreleased]

### Docs
- **Build-immutable: a bug fixed once stays fixed, caught locally** (2026-08-02)
  — added tenet **V5** and a new **build-immutable** category to `docs/TENETS.md`.
  Product-immutables define *what silt is*; build-immutables define *how we
  build* and are held at the same amendment bar. V5: every discovered defect
  ships in the same change as a failing-first regression test at its tier(s)
  (unit / integration-sim / e2e), runnable on a contributor's own machine, so a
  re-break surfaces locally in seconds — CI is the backstop, never the first line
  of defense. The three-tier Definition of Done (V1/V2) is elevated alongside it.
  Prompted by catching the integrated hole-punch gap (#27 Phase 3) locally via
  the Docker NAT harness rather than at CI.
- **Intention review actioned: M0 sharpened, S7 added, the V1 gate spine put
  on the board** (2026-08-02) — a docs/canon + tracker pass, no code or
  behavior change, acting on an intent-level fresh-eyes review. **M0** is
  requalified from "*resolve*" the trilemma to "***hold*** it — refuse to
  trade any corner away," and bound to a falsifiable test (held iff an
  *external* red-team suite denies all three failure modes); privacy and
  accountability hold from day one while **Sybil-resistance is the corner that
  bootstraps**. "No center" becomes **"no *permanent* center"** (immutable #3
  and T1), reconciling the invariant with the time-boxed launch-window anchors.
  A new tenet **S7 — "durability must pay for itself"** names the repair-loop
  economics that killed Freenet/GNUnet. **B8 and V3** now require the adversary
  that certifies a novel composition to be *external*, not self-graded. On the
  tracker, the **V1 gate spine is materialized** as GitHub labels + issues
  (gates 0→6, critical path 1→4→6, pinned epic #94): the previously
  prose-only Gate 1 floors (#87/#88/#89) and Gate 4 "the car" (#90–#93, the
  real M0 mechanism) and Gate 5 durability economics (#95) are now filed and
  traced to their tenet. The site's roadmap/changelog generators gain relative-
  link and blockquote rendering so the volatile pages stay generated, never
  hand-edited.
- **Canon reconciled: mission, mechanisms, and a single roadmap spine**
  (2026-08-02) — a docs/canon pass, no code or behavior change.
  `TENETS.md` is restructured into three tiers: a new mission-immutable
  **M0** (silt exists to *hold* the privacy × accountability × Sybil
  trilemma — unlinkable publishing, content-level accountability, and
  Sybil-resistance held together without trading any corner away), six
  mechanism-immutables, and the build tenets,
  which gain **B8** (use best-in-class, proven components; be novel only
  in how they are composed). `ROADMAP.md` is slimmed to a single GitHub
  `V1`-milestone spine: the retired M/Wave/Tier markers are dropped in
  favor of a "learning phase" framing, the 0.1.x/0.2.x line is relabeled
  experimental/learning, and the cadence is stated as 0.9.0 then 1.0.0.
  The issue tracker is reconciled (#78 and #79 closed as shipped, the
  `V1` milestone created), the website roadmap is regenerated from
  source, and a sensitive term was removed from the public docs. The
  math notes on proof-of-retrieval (05) and quorum chains (08) are
  reconciled to match: the current PoR is labeled a challenge-time toy
  with a real published-scheme PoR as the V1 target, and consensus
  standing is described as bond-gated challenged storage on a labeled
  placeholder seal being hardened for V1.

### Security
- **Publisher privacy: quorum-issued blind publish tokens** (#14 / F1): the
  chain recorded a Publisher NodeID per root, letting an observer map a durable
  reputation key to every root it published (silt protects who-READS far better
  than who-WRITES). A publish is now authorized by a **publish token** — a
  random serial blind-signed by a QUORUM of distinct validators (a k-of-n
  Chaumian blind multisignature: no single issuer, no trusted-dealer/DKG). The
  publisher pays the fee with its durable identity to acquire the token, but the
  issuers never see the serial, so the committed entry carries the token and
  **NO Publisher identity**, and each serial spends exactly once (chain-wide
  double-spend rejection). Daemon: `-require-tokens N` makes the chain accept
  only token-carrying entries and validators issue; `swarm add -token-quorum N`
  acquires one over the wire. Proven at three tiers: unit (blind sig, quorum
  bundle, chain enforcement), sim (acquire-then-publish through the node loop),
  e2e (three validators, a 2-of-3 token over real TCP). Honest residuals
  (labeled): each signature is unlinkable (Chaum), but a colluding validator set
  narrows the anonymity *set* to same-epoch requesters of the same subset (use a
  canonical validator set); the RSA issuer key is in-RAM (cross-restart
  persistence is a follow-up).
- **Launch-window training wheels** (#79, risk 15): a young network is the
  easiest to capture — a Sybil quorum is cheap before the network has
  decentralized. A validator set may now declare **anchors** (`-anchors`,
  `-anchor-quorum`): while the network is immature, a commit ALSO requires
  anchor sign-off, so a Sybil quorum cannot write to a young registry. The
  requirement **sheds mechanically** once `-mature-validators` distinct
  non-anchor validators have attested a committed block — measured
  decentralization, never a flag day. Because attesting requires earned bond
  standing (#78), the maturity metric can't be cheaply inflated by Sybils.
  Anchors are plural (a threshold; no single anchor is load-bearing, cf. R4)
  and their power is transparent, on-chain, and time-limited — they can never
  gate a *mature* network. Off by default (empty anchors). Proven for the
  OUTCOME at unit (`TestTrainingWheelsGateYoungNetworkThenShed`) and sim
  (`TestTrainingWheelsShedThroughTheNodeLoop` — the shed through the real
  propose/attest/commit loop); e2e deliberately skipped and recorded (the shed
  is deterministic chain logic covered at unit+sim, and the `-anchors` wiring
  is confirmed by a daemon smoke check — a bespoke multi-daemon shed e2e is
  high-cost/low-value).
- **Identity costs storage: bond-gated consensus standing** (#78): reputation —
  the number the chain gates writes on — is no longer dominated by
  self-reported serving (which two colluding nodes could wash-mint for free,
  threat-catalog D1/D3). Standing now costs **real, challenged, held storage**:
  a validator seals an identity-bound storage bond (`core/bond`, `-bond`), and
  validators challenge each other's bonds over the wire (`MsgBondChallenge`/
  `MsgBondReply`), verifying against only the committed Merkle root — no
  ground-truth fetch. Standing must be *sustained* (it decays if a bond stops
  being re-proven), so N Sybil identities cost N distinct bonds on N disks.
  Proven for the OUTCOME at three tiers: unit (`core/bond`), sim
  (`TestBondAuditEarnsStandingOverTheNetwork` — a no-bond node is refused,
  decay retires unsustained standing), and e2e
  (`TestBondEarnedStandingCommitsOverTCP` — two bonded validators earn standing
  over real TCP and commit on `-min-rep 100`). Honest limit: the bond is held
  in RAM and the seal is not yet memory-hard (proof-of-*space*-lite, labeled);
  disk-persistence + a memory-hard seal are tracked follow-ups. Design:
  `docs/design/bond-audit.md`.
- **Safe consensus defaults** (#79): `silt daemon -validator` now defaults to
  `-quorum 3 -min-rep 100` (was `-quorum 1 -min-rep 0`), so a lone or fresh
  node can no longer rubber-stamp the registry — writing requires earned
  standing and a real quorum. A trusted one-box swarm opts into self-commit
  explicitly (`-quorum 0 -min-rep 0`), which now prints a loud
  trusted-deployment warning rather than being the silent default. Outcome
  proven end-to-end: e2e `TestDefaultsRefuseRubberStampCommit` asserts the
  default refuses a lone commit, with `TestPublishCommitFetchOverTCP` (explicit
  `-quorum 0`) as the positive control.

### Added
- **Deterministic NAT/relay/hole-punch in the sim** (#27): the in-process
  network (`simnet`) now models a home router — a NATed node dials out freely
  (each outbound opening the conntrack reverse mapping so replies get back in)
  but is un-dialable cold from off its LAN. Two NATed nodes on different LANs
  therefore meet through a designated relay (counted in `Stats.Relayed`), or
  `HolePunch` opens a direct path for cone NATs and correctly falls back to the
  relay for symmetric ones. A relayed delivery pointedly does *not* open a
  direct mapping, so a later direct dial still needs a punch. This is the
  tier-1, seed-reproducible mirror of the `integration/nat` Docker harness; it
  is zero-overhead and byte-identical for every existing scenario (no NAT
  configured → the fast path short-circuits and draws no extra randomness).
- **Hole-punching: relay paths upgrade to direct connections** (#27): when two
  NATed daemons talk through a relay, the relay now *coordinates* a
  hole-punch — it tells each the other's observed endpoint, and both dial it
  from their relay-registration port at once (`SO_REUSEPORT`, TCP
  simultaneous-open). Through a cone NAT the crossing SYNs establish a direct
  link, which the transport adopts so the bulk traffic leaves the relay; on
  symmetric NAT it simply fails and the relay path stays. The relay forwards no
  bytes for the direct path — it only swaps addresses. The punch **primitive is
  proven end-to-end against real kernel NAT** by the `integration/nat` harness,
  CI-gated (cone → direct connection, symmetric → relay); the relay
  coordination is unit-tested. This demotes the relay from every-byte carrier
  to rendezvous, the big cost win for cheap public infrastructure (S6). (The
  live two-daemon upgrade has a harness scenario in progress — the caretaker
  traffic-trigger needs the minimal-network provider resolution sorted.)
- **NATed nodes learn their public endpoint, STUN-style** (#27, the groundwork
  for hole-punching): when a node registers with a relay, the relay reports the
  `host:port` it observed the registration coming from — the node's NAT mapping.
  A node behind NAT cannot otherwise know its own public address, and
  hole-punching needs it (it's the endpoint a peer aims a simultaneous-open at).
  Surfaced as `relay.Client.Observed()` / `node.ObservedAddr()` and logged by
  the daemon. This is phase 1 of #27; the relay-coordinated punch, port-reuse
  dial, and relay→direct upgrade follow. The `integration/nat` harness asserts
  a NATed node learns its *mapped* public IP (the gateway's), not its LAN
  address.
- **Automated cross-NAT integration harness** (`integration/nat/`, and a
  `nat-integration` CI job): stands up two genuinely-NATed daemons plus a
  public relay in real container networks (real kernel NAT via iptables
  MASQUERADE, real TLS over real sockets), publishes from behind one NAT and
  fetches from behind another, and asserts the bytes come back bit-perfect
  having crossed the relay (verified by counting relay splices). This is the
  automatable replacement for the manual two-machine (Mac A ↔ Mac B) rig — the
  NAT/relay path that the in-process sim and flat-localhost e2e can't reach —
  and the seed harness for hole-punching (#27) and restart/re-provide (#69)
  scenarios. Runs on one host (CI, a dev box, or Docker Desktop); no second
  machine.

### Fixed
- **The daemon no longer silently drops config fields** (#71): `cmd/silt` built
  `node.Config` field-by-field, so any field added to `DefaultConfig` defaulted
  to its zero value in the real binary — how the #65 fetch-retry shipped inert
  and demand-responsive dispersion was off in the daemon while the roadmap
  listed it as done. The daemon and the ephemeral swarm add/get client now
  start from `node.DefaultConfig()` and override only what genuinely differs
  (the daemon's 2s `RequestTimeout`), so new fields are inherited by default.
- **A restarted daemon's content stays discoverable** (#69, found in the #65
  field test): provider records live only in peers' memory and die with the
  process, so a daemon re-announces everything on its disk at startup
  (`AnnounceHeld`) — but a coded shard must be announced under its *column
  key* `hash(root‖column)`, where readers look, and that key is derived from
  the shard's storage proof. Proofs were kept only in memory, so after a
  restart the re-announce fell back to the bare chunk id and a disk full of
  intact content was invisible until it happened to be re-hosted. Storage
  proofs are now **persisted alongside the chunks** (`adapters/diskproofs`) and
  reloaded on startup, so the re-announce lands on the right key again — and
  the node can still answer storage-audit challenges after a restart. The
  `integration/nat` harness gained a `RESTART=1` scenario that restarts the
  whole swarm and re-fetches to prove it.
- **Fetches survive a saturated relay** (#65): once the public rendezvous
  node hits its capacity cap, every byte to a NATed provider funnels through
  the relay, whose per-peer splice slots saturate under concurrent fan-out
  and return "relay at capacity" — and the fetch path had **no retry**, so a
  transiently-refused chunk was reported unreachable (the tail-of-sweep
  fetch failures seen from a second network). A chunk fetch now **re-sweeps
  its providers with a backoff** when every provider failed *transiently* (a
  timeout or relay refusal, not a clean "don't have it") — the freed slots
  make the retry succeed — the fetch-side analogue of the #63 placement
  retry (`FetchAttempts`/`FetchBackoff`, default 3× / 200 ms). A clean miss
  (nobody has the chunk) still returns after a single pass. The relay's
  concurrency defaults are also raised from **64/8 to 128/16**
  (global/per-peer): splices are short-lived, so this is realistic headroom
  for a rendezvous node while staying a bounded, operator-tunable cost (each
  splice is still byte-capped). Remaining, tracked in #65: register-after-
  distribute (a loud placement failure still leaves a dangling registry
  entry), and hole-punching (the structural fix that moves bulk bytes off
  the relay entirely).
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
