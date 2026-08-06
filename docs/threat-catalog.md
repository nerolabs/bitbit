# Silt threat catalog

**Status: working catalog, draft for review (2026-07-31).** This is the
full enumeration behind [`threat-model.md`](threat-model.md) (which is the
narrative "attack us" document) and [`risk-register.md`](risk-register.md)
(which ranks the top risks with owners). This file is the *breadth* — every
malicious activity we've thought of, so none is silently forgotten.

State key: **✓** mitigated/built · **~** partial · **✗** open · **?**
suspected-but-unverified in code (verifying these is itself launch work).

A few structural through-lines, stated once:

- **Free identity (Sybil) is the taproot — and Sybil-resistance is a
  *composition*, not a primitive.** Node IDs are `SHA-256(pubkey)` and minting a
  keypair is free, so most of the worst items below reduce to "an attacker with
  many cheap identities." Minting stays free; what costs is consensus **standing**.
  Per the composition reframe ([`design/m0.md`](design/m0.md)), **no single
  primitive can be Sybil-proof** under free identity + no permanent center
  (Douceur) — so a primitive failing a standalone "is-it-Sybil-proof?" test is
  *expected*, not a failure. The guarantee is **systemic: C1 (no discount) + C2 (no
  quiet capture)**, held in tension, where forging N standings costs N× of *every*
  non-substitutable resource — `C_honest = disk × address-diversity × time ×
  served-demand`. The parts that make each axis real are built and internally
  hardened (Gate 4): an identity-bound proof-of-**space-time** bond over a proven
  depth-robust graph (axis D), a proof-of-retrieval that verifies possession
  **without fetching**, signed provider records + failure-domain diversity against
  key-surround (axis A), and equivocation slashing with forks reconciling to the
  heavier-standing chain — proven at unit + sim + real-daemon e2e. The internal
  hardening pass is complete; **external re-verification against the systemic C1/C2
  claim is the remaining bar.** A hard proof-of-work on *minting* stays deferred;
  we say so loudly.
- **Day one is a security parameter.** Nearly every attack is easiest on a
  tiny network — eclipse, quorum capture, version-floor evasion all peak at
  launch, the worst possible moment. The launch strategy is a control, not
  an afterthought (see "Launch-window design").
- **Self-reported trust is gameable.** Any metric a node asserts about
  itself (capacity, serving, liveness) is an attack surface — we were
  already bitten by the size-estimate pollution (#43).
- **The Aslan boundary is a liability firewall.** All "meaning" attacks
  live in the naming/resolver layer, which is out of core *by immutable
  design*. Core carries zero meaning, forever.
- **Transport is TLS-over-TCP, not UDP** — which removes classic
  spoofed-source reflection amplification for free (the handshake proves a
  requester's address before we do expensive work).

---

## A. Resource-exhaustion / asymmetric DoS ("cheap to send, expensive to serve")
The class a WAF/CDN can't help with, because Silt's core is a P2P TLS
protocol on raw sockets, not an HTTP origin. Treat these as facets of **one
per-peer resource-accounting framework** — a budget across connections,
compute, storage, and bandwidth; exceed it → throttle then disconnect; all
limits operator-configurable with sane defaults.

**Connection / handshake (CPU, FDs, memory)**
- A1 **Handshake flood** ✗ — cheap half-open mTLS handshakes burn asymmetric-crypto CPU → per-IP conn-rate limit + concurrent-handshake cap + timeout.
- A2 **Connection/FD exhaustion** ✗ — idle conns eat FDs/RAM → global + per-peer/per-IP concurrent-connection caps + idle timeout.
- A3 **Slow-loris** ✗ — hold streams open dribbling bytes → read/write deadlines + minimum-throughput enforcement (peer and relay).

**Parsing / memory (CPU, RAM)**
- A4 **Oversized frame** ? — one huge frame forces a huge alloc → max frame size, bounded/streaming decode.
- A5 **Malformed frame → crash** ? — fuzz every CBOR/manifest/link decoder + `recover` per request so one bad frame can't kill the daemon.
- A6 **Unbounded manifest fan-out** ? — a manifest claiming millions of chunks forces millions of lookups/fetches → bound declared chunk count / total size; reject implausible manifests.

**Compute amplification (CPU)**
- A7 **Erasure-decode amplification** ✗ — requests that force reconstruction → serve raw shards by default; reconstruct only for legitimate repair; rate-limit/charge it.
- A8 **Proof-of-retrieval challenge amplification** ? — cheap nonce → expensive `H(nonce‖bytes)` over big shards → bound challenge size/rate per challenger; sample a bounded byte-window.

**Storage (disk)**
- A9 **Publish/disk-fill spam** ~ — flood placements to consume pledged capacity → per-peer placement quotas + publish credit-cost (exists, but Sybil-farmable).
- A10 **Small-object / metadata bloat** ~ — many tiny chunks inflate index/registry/per-object overhead (seen in the 300-file run) → min-chunk-size or per-object accounting; per-publisher registry quota.
- A11 **Dangling-entry registry bloat** ~ — weaponized #60 (register-without-store). The silent-loss half is FIXED (#60 manifest + #64 data-shard: publish returns no link unless the content is provably reconstructable, else fails loud), so a failed publish no longer hands back a broken link — but a loud-failed publish still leaves a dangling *registry entry* (no link to the user), so register-after-distribute (#65) + registry write quotas remain open.
- A12 **Targeted capacity-exhaustion as censorship** ✗ — fill the nodes nearest a victim's key so its shards can't be placed/repaired → headroom reservation + detect targeted fills (overlaps eclipse; relates to #64).

**DHT / provider records (outbound bandwidth)**
- A13 **Lookup outbound-exhaustion** ✗ — one FindNode/GetProviders fans out to α×hops; can't be spoofed (TLS) but an authenticated malicious peer can still pump them → per-peer lookup rate limit.
- A14 **Provider-record flooding** ✗ — announce fake provider records to bloat tables / mislead fetchers → cap records per (chunk, announcer), expire them, trust only announcers that answer a serve-challenge.

**Repair (CPU + bandwidth + disk — the scariest amplifier)**
- A15 **Repair-storm** ✗ — cheap "shard lost" signals trigger expensive whole-stripe reconstruction → **probe-before-repair** (verify loss), debounce/rate-limit triggers, cap concurrent repairs, exponential backoff.

**Relay (the public node)**
- A16 **Relay slot exhaustion / free-CDN abuse** ~ — hold the per-peer splice slots (default raised 8→16, global 64→128 for #65 fan-out), or pump bulk NAT↔NAT bytes on someone else's dime → per-identity slot + byte quotas, throughput timeouts, credit-metering. **Structural relief now BUILT: hole-punching** (#27) upgrades a relay path to a direct connection through cone NAT (proven, CI-gated), so bulk bytes leave the relay entirely — the relay is demoted to rendezvous. Symmetric-NAT peers still relay; per-identity abuse quotas still open.

## B. Sybil / eclipse (the taproot — a composition, not a single primitive)
- B1 **Bulk identity minting** ~ — minting keypairs is still free; consensus STANDING costs the **composition**, not one primitive. Earning a fraction *q* of standing costs ≈ *q* of every non-substitutable resource — `C_honest = distinct sealed disk × address/AS diversity × elapsed time × served demand` ([`design/m0.md`](design/m0.md) §3). The storage bond (axis D) is a real proof-of-space-time — persisted, identity-bound, over a **proven depth-robust graph** (DRSample, G2) — so one disk cannot back many identities; the other axes are priced by their own parts. A standalone primitive failing "is-it-Sybil-proof?" is expected (Douceur), not a failure. PoW/stake on *minting* still deferred. Awaiting external re-verification of the composition (C1/C2).
- B2 **Eclipse a key's neighborhood** ~ — surround a chunk's key to suppress its provider records (censor discovery of one file). **Now addressed (H5):** provider records are self-certifying (signed, key-bound, expiring — cannot be forged) and announced/resolved over a failure-domain-diverse near set, so a single-domain key-surround can't suppress discovery. Residual: `Domain` is self-reported — bind to the transport-observed /24 for full strength.
- B3 **Size-estimate bias** ~ — skew XOR-density estimate via placed identities (the #43 family; partly fixed for ephemeral clients).
- B4 **Eclipse of new nodes** ✗ — feed a joining node a poisoned peer set (see discovery D-layer, F14).
- B5 **γ→1/N shared-content sealing boundary** ✗ — the one surviving economy of scale (#182). Fusing served-content into consensus standing without a per-identity **γ→1/N discount** would let one physical copy of a shared, erasure-coded shard answer for N pledges at 1/N marginal cost, collapsing C1 ("N standings cost N disks"). Closing it needs identity-keyed PoRep sealing that does not exist. **Not exposed today:** standing comes *only* from the dedicated identity-bound proof-of-space-time bond plot; served bytes fund the balance/durability economy, not standing. C1 holds while served-content and bond-standing stay separate; the "one ledger" fusion is deferred pending sealing.

## C. Poisoning / integrity
- C1 **Chunk substitution** ✓ — defeated by content addressing + verify-on-read; wrong bytes under a hash is a SHA-256 break.
- C2 **Manifest→root binding** ✓ — `Get` verifies the manifest hashes to the requested root.
- C3 **File poisoning at the resolver layer** ✗-out-of-core — content addressing guarantees *bytes-under-a-hash*, not that a **name** means what you think. Name→root poisoning is the real vector and lives in Aslan (immutable boundary; see H). Document: trusting a name = trusting that resolver.
- C4 **Availability-poisoning via fake providers** ✗ — bury real provider records under fakes to make a chunk undiscoverable (eclipse-adjacent; A14).
- C5 **Registry poisoning** ~ — spam dangling entries to bloat/mislead (A11).

## D. Economic / incentive
- D1 **Wash-serving / self-dealing** ~ — wash-serving still moves the BALANCE economy (observability), but it no longer buys STANDING: reputation dropped self-reported `servedBytes` and is built on challenged held storage + audits (T1/#82). So sybils can't wash-serve their way to a quorum. (Witnessed/challenged *serving* for the balance side is still future.) **Decision D-DEMAND:** standing is priced on **cost-to-wash**, never raw receipt count — demand authenticity is a Douceur limit (you cannot cheaply prove *genuine* demand), so the pricing charges for the cost of faking it rather than trusting the count.
- D2 **Credit-farming → spam funding** ✗ — farmed credits fund publish-spam/disk-fill (feeds A9/A10).
- D3 **Reputation collusion → quorum capture** ~ — accruing standing across sybils now costs N real storage bonds (T1/#82), not free wash-serving; and on a young network commits also need anchor sign-off that sheds on measured decentralization (T2/#83 training wheels). Not eliminated (a well-resourced adversary can buy disk), but no longer cheap — the economy→consensus bridge is priced.
- D4 **Audit gaming / "storage theater"** ~ — pass challenges from briefly-borrowed shards, or precompute if predictable → reputation without durable storage → unpredictable, unborrowable challenges.
- D5 **Withholding / trickle-serving** ✗ — pledge + accept placements, then serve at a trickle → reputation must weight bytes *actually served*, challenged.
- D6 **Selective serving / griefing** ✗ — serve everyone except a target → censorship below the revocation layer.
- D7 **Fee mispricing / governance** ~ — too cheap enables spam, too dear kills adoption; who tunes it is unspecified.
- D8 **Make honesty unprofitable** ✗ — if serving costs more than it earns, rational operators stop → availability collapses with zero protocol violation. Economic parameters are safety-critical.

## E. Consensus / chain (reputation quorum, not BFT)
- E1 **Bootstrap-window capture** ✗ — thin/declared reputation at genesis → founding quorum tiny, easiest to capture or coerce at launch.
- E2 **Long-range / history rewrite** ? — latecomers re-check history against what anchor beyond genesis? → signed checkpoints.
- E3 **Equivocation** ? — conflicting attestations; verify slashing exists and works across partitions.
- E4 **Quorum liveness griefing** ✗ — a subset refuses to attest → commits stall.
- E5 **Revocation-as-weapon** ~ — quorum revokes lawful content (auditable, but abuse remains; adoption-bound). **Takedown transparency (D-TAKEDOWN):** the non-globality guard is now a **constructed metric** — a survivor Nakamoto-coefficient over failure domains, published as a certified lower bound ≥ t via a ZK threshold predicate that reveals only the scalar t — so "no takedown is global" is measurable, not just asserted.

## F. Privacy / deanonymization (beyond the doc'd traffic-analysis)
- F1 **Publisher identity on-chain** ✓ — CLOSED (T3/#84). A publish is authorized by a quorum-issued **blind publish token** (a serial blind-signed by k distinct validators), so a committed entry carries the token and NO Publisher NodeID — authorship is unlinked from the durable key. Residuals (labeled): a colluding validator set narrows the anonymity *set* to same-epoch requesters of the same subset (use a canonical set). The RSA issuer key now **persists** across restarts (#126, `adapters/diskissuer` — tokens it signed stay verifiable and peers' cached keys don't go stale); on-chain issuer registration remains a follow-up.
- F2 **Access-pattern correlation** ✗ — a participating node sees which roots you fetch/hold over time.
- F3 **Timing/size fingerprinting** ✗ — chunk sizes + timing fingerprint files even encrypted (no padding).
- F4 **IP↔NodeID persistence** ✗ — reputation needs a long-lived identity; long-lived identity is persistent trackability. A genuine tension.
- F5 **Relay/seed as panopticon** ~ — running a popular relay or bootstrap seed is a metadata goldmine.
- F6 **Confirmation-of-file (convergent)** ✓ — documented; private mode defeats it at the cost of dedup.
- F7 **Convergent-dedup takedown-collateral** ~ **(accepted-low)** — because convergent encryption maps identical plaintext to one opaque root, a single opaque-hash takedown collaterally purges *every* independent publisher's copy of that convergent content, not just the reported one. **Accepted-low:** takedown is by opaque hash on content the quorum has already ruled on, private mode (the default, H6) does not converge and is unaffected, and convergent is opt-in with a printed warning. Recorded so the close is visible rather than a silent drop (F6 covers only confirmation-of-file).

## G. Data-durability, adversarially driven (beyond #60/#64)
**Durability posture (D-S7):** center-less **proof-of-repair now has a construction** — a composition of proven parts (transparent polynomial-commitment correctness + Shacham–Waters retrievability + DAS quorum; no coordinator repairs on anyone's behalf) — but it is **NOT YET BUILT** (→ H7 / #95). Durability is therefore *designed* **finite-but-renewable**, not "perpetual": redundancy decays without work and is renewed by funded, verified repair — and that renewal is solvent only while the measured credit-cost decline `g > 0`. The items below attack the *renewal* loop.
- G1 **Provider-record poisoning of repair** ✗ — fake providers burn the repair budget so real repair never happens (pairs A14/A15).
- G2 **Targeted churn below k** ✗ — join/leave near a stripe's columns to drop shards faster than repair (the #64 vector, weaponized).
- G3 **Manifest-provider targeting** ✗ — the manifest chunk is a small single point; attack its providers to make files unfetchable → higher replication + monitoring for manifest chunks.
- G4 **Decode-shard corruption** ? — confirm each shard is hash-checked before feeding erasure-decode.

## H. The Aslan boundary as a firewall (immutable by design)
- H1 **Boundary erosion** ✗-must-never — teaching core to resolve names makes it inherit all moderation + legal burden it exists to shed. Any change adding meaning to core is a top-severity regression → architectural guard + tenet.
- H2 **Meaning-leak audit** ~ — the firewall holds only if core leaks no meaning; F1 (publisher) is a partial leak; confirm nothing else (filenames, MIME, size-fingerprints) crosses.
- H3 **Resolver-trust delegation** ✗-doc — a malicious resolver is the poisoning vector; core can't stop it → document that a name's trust is the resolver's, like DNS.
- H4 **Reputational contagion** ✗ — an Aslan failure is reported as "Silt did X"; crisis-comms + public boundary explanation is the mitigation.

## I. Local API / integration surface (developer & app seat)
- I1 **DNS-rebinding / CSRF against the local daemon** ? — the UI/JSON API on `127.0.0.1:8081` is a classic rebinding target (drive publish, spend credits, read the link-book) → `Origin`/`Host` allow-listing + per-daemon API token.
- I2 **Malicious Aslan app abusing the daemon** ✗ — a rogue app publishes illegal content under the user's identity or drains credits → scoped capabilities, not full daemon access.
- I3 **SSRF via config inputs** ? — `-dns-seed`/`-bootstrap`/relay strings pointed at internal/metadata endpoints.

## J. Discovery layer
- J1 **DNS-seed poisoning** ✗ — DNS TXT bootstrap is spoofable/hijackable → poison newcomers' peer set → sign seed records, DNSSEC, plural independent seeds.
- J2 **mDNS/LAN spoofing** ✗ — LAN attacker injects peers; bounded by mTLS+verification (confirm).

## K. Self-reported-metric poisoning
- K1 **Gossiped capacity/serving lies** ~ — lie about capacity to attract/repel placement, inflate serving to farm reputation, skew the observatory → cross-check against challenged/witnessed evidence; treat the observatory as advisory, not authoritative.

## L. Protocol / implementation
- L1 **State-machine confusion** ? — out-of-order messages to unhandled states → strict per-connection state validation + fuzz.
- L2 **Slow-burn leaks** ? — memory/goroutine/FD leaks over time → soak-testing + leak detection in CI.
- L3 **Downgrade / replay** ~ — TLS1.3-pinned stops crypto downgrade; replay of signed attestations/advisories needs nonces/sequence (extend everywhere).
- L4 **Clock dependence** ✗ — TTLs/leases/deadlines on wall-clock are manipulable → prefer monotonic/observed time (ties to the update-advisory clock anchor).

## M. Social / governance / supply-chain
- M1 **Maintainer key compromise** ~ — steal a signing key → forge releases *and* advisories (incl. Critical) → threshold m-of-n signing is non-negotiable for the Critical lever.
- M2 **Malicious contributor / backdoor PR** ~ — review-gate built; reinforce with a small trusted reviewer set + reproducible builds (backdoor detectable).
- M3 **Dependency compromise** ✗-easy-win — vendored, minimal deps + `govulncheck`/Dependabot in CI + SBOM. Silt's tiny dependency surface makes this cheap.
- M4 **Fake-binary / typosquat distribution** ~ — signed releases + canonical distribution + provenance.
- M5 **Legal coercion of maintainers** ✗ — compelled malicious update/advisory → threshold + reproducible + operator-controlled + transparency-log (warrant-canary style).
- M6 **Governance capture** ✗ — the advisory channel is the one privileged broadcast; capturing it captures the network → threshold + transparency.

## N. Abuse / safety evasion (beyond the built takedown)
- N1 **Takedown evasion by re-encoding** ✗ (fundamental) — re-chunk/re-encrypt denied content → new hashes → past a hash-based denylist. A limit to state honestly; robust moderation needs the resolver layer (Aslan).
- N2 **Denylist-as-DoS** ✗ — a huge/adversarial denylist makes every op expensive → bound list size + check cost.
- N3 **Weaponized-liability seeding** ✗ — deliberately seed illegal content *at specific operators* to manufacture legal jeopardy → content-blindness + operator-chosen denylists + no-project-override must be airtight and documented for operators.

## O. Legal / jurisdictional / compelled
- O1 **Compelled metadata disclosure** ✗-design — subpoena a relay/registry/seed for logs (IPs, NodeIDs, timing) → **data-minimization**: don't retain what you don't need; audit what each role logs by default.
- O2 **Jurisdiction-shopping abuse** ✗ — publish where content is legal to burden operators where it isn't (pairs N3).
- O3 **Compelled node modification** ~ — content-blindness + encryption + reproducible builds + no-override limit the blast radius.

## P. Crypto horizon
- P1 **Harvest-now-decrypt-later** ✗ — ciphertext captured today is decryptable when SHA-256/Ed25519/AES fall. For durable storage this matters → **crypto-agility**: versioned primitives + migration path; name the confidentiality horizon honestly.

---

## Cross-cutting mitigation: detection
Most items are cheaper to *detect* than *prevent*. The observatory (Gini,
capacity, serving, provider maps) should grow an **anomaly-detection layer**
— identity influx (Sybil), reputation-farming graphs (wash-serving),
repair-storm spikes, provider-record floods. Detection + reputation-slashing
+ the version-floor recall is the pragmatic triad, now backstopped by the
work-backed standing bond (T1/#82) pricing the identities these attacks need
— while a hard PoW/proof-of-space prevention primitive on minting stays
deferred.

## Decisions taken (2026-07-31 conversation)
- **Hole-punching is V1.** Public-relay economics require it (NAT↔NAT
  today relays every byte through the public node — expensive; see
  [`network-protection.md`](network-protection.md)).
- **Rate limits: yes, operator-configurable from the start.**
- **Sybil answer: token-less, work-backed, identity-bound reputation.**
  Standing costs a challenged storage bond (T1/#82); a young network also
  gates commits behind maturity-scaled anchor sign-off (T2/#83 training
  wheels). The **real mechanism is now built** (Gate 4: a proof-of-space-time
  bond, a verify-without-fetch PoR, fork-choice reconciliation, and equivocation
  slashing), so Sybil (B) and its downstream (D1/D3) are priced by real
  space-time held over time; **independent review + the multi-machine field test
  remain before it is *proven***. A hard PoW / proof-of-space primitive on
  identity minting stays
  deferred.
- **Update enforcement: criticality-graded, signed, recallable** (see
  [`network-protection.md`](network-protection.md) and TENETS).
- **The Aslan boundary is immutable; core carries zero meaning** (TENETS).
- **Docs use the neutral term "file poisoning"** — no worked example.
