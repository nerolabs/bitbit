# Changelog

All notable changes to Silt are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/), and the project aims to
follow [Semantic Versioning](https://semver.org/).

This log is published at [silthq.com/changelog](https://silthq.com/changelog.html).

## [Unreleased]

### Security
- **F-3: the whole-registry `GET /all` dump is off the public mux** (2026-08-08) — Completes the
  red-team F-3 fix. `/all` serialized the entire registry O(N) with no pagination — an unbounded
  per-request cost. An interim change priced it by work, but that only bounds cost *per source*; a
  **distributed** dump (one request per source IP) no per-IP counter can touch. Since `/all` is used
  only by an operator's own CLI/UI — which reads the registry **in-process** (the daemon's local
  `chainhost`/`fileregistry`), never over this wire — it is now simply **not served on the public
  mux**: a remote client gets `404` / `ErrAllNotServed` and degrades to per-root `/lookup`; an
  operator listing its *own* registry is unaffected. This deletes both the amplification and the
  distributed variant. Also hardened the rate-limiter's per-IP **bucket map with a hard size cap +
  sampled-LRU eviction** so a source-IP-cycling flood can't grow it without bound (it would otherwise
  be its own cost vector). Regressions: `TestBucketMapIsBounded` + the round-trip test now asserts
  `/all` is not served. (The interim work-pricing `charge()` is removed as superseded.)
- **F-1: the maturity shed is now a genuine one-way ratchet (anchors never re-arm)** (2026-08-08) —
  A blind red-team pass (re-)found that the launch-anchor "training wheels" were gated on the **live**
  `Mature()`, which recomputes decentralization from the *current* bonded set — so an honest whale
  growing **real** bond past ⌊total/3⌋ could flip a matured chain back to immature and **re-arm the
  zero-bond anchors**, either halting the chain (if the anchors were gone) or handing them permanent,
  standing-free power (contradicting immutable #3, *no permanent center*). Fixed as a bundle:
  - **One-way `everMature` latch** — the anchor requirement (and anchors' bond-free eligibility) is now
    gated on whether the network has *ever* matured, a replay-derived **consensus fact** (latched in
    `apply`, re-derived on reload, carried across a reorg). Once matured, the anchors never re-arm.
  - **Real-bond super-quorum de-maturation fallback** — if a matured network later drops below the
    decentralization bar, a commit needs a **real-bond super-majority** (≥⅔ of live bonded weight, no
    anchor sign-off) instead of the retired anchors — center-less liveness that preserves accountable
    safety (`ErrDeMatureQuorum`).
  - **Weak-subjectivity checkpoint** — silt is now explicitly weakly subjective; a fresh/long-offline
    node pins a recent trusted block with `-ws-checkpoint HEIGHT:HASH` and **refuses any reorg at or
    before it** (`ErrPreCheckpointReorg`), the long-range-attack defense that makes the latch safe. The
    daemon prints `checkpoint: HEIGHT:HASH` for its committed head so operators can publish/cross-check it.
  - The two residuals are **owned, not hidden** (`docs/design/m0.md` §10): a bounded, socially-recoverable
    re-centralization risk (the honest whale — the same trade Ethereum/Cosmos/Bitcoin made) and the
    weak-subjectivity dependency itself. Regressions invert the red-team PoC (both halt and
    permanent-center horns killed; super-quorum enforced; long-range reorg refused).
- **C2 concentration: the address-diversity (A) axis + an out-of-band honest-whale alarm** (2026-08-08) —
  Two follow-ups to F-1, hardening the residual it deliberately leaves open (the honest whale — real bond
  concentrated by a real operator, unclosable on-chain by theorem, Kwon):
  - **A axis wired into the shed.** A validator's failure-domain (`-domain`) is now **committed in its
    bond** (`BondReg.Domain`, backward-compatibly signed) so the concentration metric counts
    **address-diverse** participants: bonds sharing a declared domain aggregate into one group
    (`NakamotoDomains`), and the maturity shed gates on `min(NakamotoOperators, NakamotoDomains)` — so a
    stake split across many keys in ONE domain cannot fake decentralization; retiring the launch anchors
    needs distinct domains, not just distinct keys. Turns the flat operator-margin `M` into a
    per-network-position cost. Honestly **weak** (a domain is declared, transport-cross-checked, not
    proven; /24s are rentable) — it *prices* concentration, it does not *close* it. With no `-domain` set,
    behavior is identical to before.
  - **Concentration alarm.** `C2Metric` now also reports **HHI**, the **Gini** coefficient, and the
    top bond's share; the daemon narrates them and raises a `⚠ CONCENTRATION ALARM` when one bond holds
    ≥ ⅓ of bonded weight — a social/operational veto, explicitly not on-chain enforcement.

  Regressions: `TestC2Metric_AddressDiversityGate` (same-domain splitting doesn't shed; distinct domains
  do; unset domains unchanged), `TestBondRegDomainSignatureBackwardCompatible`, `TestC2Metric_ConcentrationSignals`.

### Changed
- **Truth-in-labelling sweep + split-defense safe-default — remediating the M0 blind
  red-team + acceptance passes** (2026-08-08) — The reviews found the composition sound (no C1
  discount, no C2 capture, demand→standing firewall holds both directions) but flagged a cluster of
  *docs-ahead-of-code* overclaims and two documentation gaps. Corrected:
  - **The time (T) axis is relabelled as retention-only.** `Reputation()` has no acquisition-time
    term (`firstSeenTick` is recorded but read by no standing calc), so full standing is granted on
    the first passing bond challenge and acquisition is priced by **D alone**. The docs (`m0.md` §3
    & §4, `TENETS.md`, `core/credit/credit.go`) previously asserted T was a live acquisition factor
    ("cannot buy last month's uptime"); they now state T ships for *retention only* (decay/TTL) and
    that a time-acquisition ramp is deferred (a bare age gate is pre-farmable — the coin-age
    anti-pattern; the only sound form is a continuous bond-anchored VDF, M1+).
  - **`GET /all` registry read-cost is now priced by work, not per request** (F-3). A per-IP token
    bucket that charges one token regardless of endpoint metered the wrong quantity — `/all`
    serializes the whole registry O(N) for the same token as a 183-byte `/lookup` (~20,000×
    amplification at N=20k). `/all` now additionally charges ~one token per 64 entries served,
    draining the source's bucket into bounded debt, so a single caller can't repeatedly amplify one
    token into a full-registry dump. Regression: `TestChargePricesAllByWork`. (A *distributed* `/all`
    flood and full cursor pagination remain post-launch — the #48 entry now says so.)
  - **The C2 operator margin M is safe-by-default.** `-operator-margin` now auto-arms to a
    conservative `M>1` for an untrusted (objective) validator — exactly as `-min-bond-floor` and
    `-byzantine-quorum` already do — instead of shipping `M=1` (zero protection against one operator
    splitting real stake across NodeIDs to fake the decentralization that sheds the launch anchors).
    An explicit `-operator-margin 1` still opts out for a trusted/single-operator swarm. M stays an
    honest heuristic (unverifiable on-chain, #182). Regression:
    `TestOperatorMarginDefaultsAboveOneForUntrustedValidator`.
  - **The seam-4 demand-receipt one-liner (`m0.md`) no longer reads as closed.** Two residual leaks
    (a receipt is forgeable with zero object bytes; a bonded-mode receipt links fetch→standing key)
    are neutralized *today* by the firewall (demand has no consensus consumer) but must be closed
    before any demand→standing fusion — now stated as such.

### Fixed
- **Acceptance-pass documentation gaps** (2026-08-08) — From the fresh-operator acceptance pass (all
  nine flows worked; these were doc/test issues, not broken capabilities):
  - `docs/user-seam.md` Role 4 "become a validator" walkthrough errored as written — the default
    objective fork-choice path needs `-anchors` to bootstrap a young network's on-chain bonded weight,
    which the walkthrough omitted (`bonded 0, needs …`). It now passes `-objective=false` to match the
    cited test `TestBondEarnedStandingCommitsOverTCP`, with a note on the objective/anchor launch path.
  - `README.md` said the default add mode was "convergent"; the default is `-mode private` (H6). Fixed.
  - `examples/flow8-takedown.sh` published with the (now private) default, giving two *different* roots
    so the takedown test denied one root and confirmed an unrelated one still served. It now publishes
    `-mode convergent` so both operators hold the **same** root, actually demonstrating per-operator
    takedown of a shared root.

### Added
- **`-registry-only` — the leanest public-registry role** (2026-08-08,
  [#47](https://github.com/nerolabs/silt/issues/47)) — A daemon started with `-registry-only` serves a
  file-backed registry over HTTPS and constructs **no storage node at all** — no DHT, chunk store,
  chain, or caretaker. It sits below `-freeload` (which is still a full routing node that merely refuses
  to host content): a public-infrastructure operator can now run a rendezvous registry at minimal cost.
  The daemon returns to a tiny registry-serving path before any of the node machinery is built. Proven
  over real TLS (`e2e/registry_only_test.go`: a pinned client publishes + looks up an entry, and the
  daemon never announces a routing peer). With `-freeload` (PR #201) this completes #47.
- **Registry read-cost bounding — keep public registries cheap to run** (2026-08-08,
  [#48](https://github.com/nerolabs/silt/issues/48)) — A registry is a costless public good only if a
  single caller can't drive unbounded cost, so the registry HTTP server now enforces a **per-client-IP
  token-bucket rate limit** (generous defaults — 20 req/s, burst 40 — so normal clients never notice;
  sustained floods from one source get `429`) plus **server timeouts** (read-header / read / write /
  idle) against slowloris. Idle rate buckets are pruned on a timer so a caller cycling source IPs
  can't grow the bucket map without bound (the map would be its own cost vector). `GET /all` is
  additionally **priced by work** (see the Fixed entry below) — a flat token bucket alone meters
  request *count*, not the O(N) serialization `/all` does. Covers the read-cost-bounding lever of #48
  for a **single source**; a distributed `/all` flood, full cursor pagination, liveness-pruning of
  dead entries, and federation/sharding remain as post-launch work.

### Fixed
- **Prepaid publish credits were silently dropped over real TCP** (2026-08-08,
  [#179](https://github.com/nerolabs/silt/issues/179)) — `tcpnet`'s hand-rolled wire codec
  (`toWire`/`fromWire`) never mapped the `Credit` field of a `MsgTokenRequest`, so the F4/D3 **fee
  decoupling** (paying for a token with a prepaid blind credit instead of charging the durable
  identity) only ever worked in the in-process sim — over real sockets the credit vanished and the
  issuer fell back to charging the requester. Added `Credit` to the wire struct + `toWire`/`fromWire`,
  with a `TestCreditSurvivesWire` round-trip guard (the exact `#65`-class silent-drop bug
  `wire_por_test.go` warns about). This repairs the publish-token credit path (F4) as well as enabling
  D3 below.

### Added
- **D3 issuance-mixing, slices 1 & 2 — ephemeral-identity + relay-routed token withdrawal** (2026-08-08,
  [#179](https://github.com/nerolabs/silt/issues/179)) — Closes the remaining fetcher-unlinkability
  links in the demand receipt. The token issuer authenticates whoever dials it via the end-to-end TLS
  handshake, so a withdrawal made over a fetcher's durable identity tied that withdrawal to the fetcher
  (the blind signature hid only the token serial, not the network identity). Now `client.
  WithdrawDemandTokenPrivately` performs the withdrawal over a **fresh ephemeral identity** — a one-off
  keypair + transport, torn down on return — paying with a **prepaid blind credit** (`Node.
  AcquireDemandTokenWithCredit`) rather than a durable account, so the issuer authenticates only an
  unlinkable ephemeral key and charges nothing it can tie to the fetcher (**slice 1 — the identity
  link**). Given a **relay-form** issuer address (`relay:R@host:port`) the ephemeral transport dials the
  issuer THROUGH a content-blind relay, so the issuer's inbound connection is from the relay, not the
  fetcher — hiding the fetcher's IP as well (**slice 2 — the IP link**); the end-to-end TLS still
  authenticates the ephemeral key across the relay pipe. Proven over real TCP (`client/privissue_test.go`:
  a mock issuer records the authenticated identity — it is the ephemeral one, never the fetcher's, direct
  and relay-routed; a withdrawal with no valid credit is refused). *Fetcher-unlinkability is now
  cryptographic + identity + IP; timing-correlation (epoch-batching) is deferred to the post-M0 H8
  mixnet.* Whole suite green with \`-race\`; full e2e suite green over real TCP.
- **#184 verify — forged-block→reject and low-bond→reject over the REAL WIRE** (2026-08-08,
  [#184](https://github.com/nerolabs/silt/issues/184)) — The two `ValidateProposal` defences, proven
  against real daemons over TCP: an honest validator refuses to attest a proposal whose **proposer
  signature is forged** (corrupted after signing) or whose **proposer lacks a qualifying bond**. A
  single red-team primitive (`Node.ProposeBadBlock`, behind the daemon's `-forge-block` /
  `-lowbond-propose` flags) sends one crafted proposal to a peer and reports whether it was refused;
  the block is otherwise valid and built at the target's head, so the only reason for refusal is the
  fault under test (a bad signature → `ErrBadSignature`, or an under-bonded proposer →
  `ErrLowReputation`). `TestForgedBlockRejectedOverTCP` and `TestLowBondProposerRejectedOverTCP`.
  These complete #184's four consensus-safety cases over the real wire (with equivocation→slash and
  partition→heal). Whole suite green with \`-race\`; full e2e suite green over real TCP.
- **#184 verify — partition→heal proven over the REAL WIRE (partition control + reorg observability)** (2026-08-07,
  [#184](https://github.com/nerolabs/silt/issues/184)) — The M0 consensus denial "honest replicas cannot
  permanently diverge under a partition" now runs against real daemons over real TCP, not only the
  in-process sim (`sim/reorg_test.go`). A new **`-block-peers` daemon flag** (`Node.SetBlockedPeers`)
  simulates a severed link — the node drops all traffic to/from the listed peers — so a validator can be
  partitioned, each side can make its own progress, and then the link can HEAL (restart without the
  flag; the persisted chain reloads and reconciles). The e2e (`TestPartitionHealsToHeavierForkOverTCP`)
  splits two committing groups into divergent forks (a heavier two-block fork and a lighter one-block
  fork), then heals the lighter side, which **reorgs onto the heavier fork** — consensus reconverges on
  one history. Also adds a `Node.OnReorg` callback so the daemon surfaces a reorg on stdout
  (`chain: reorged onto a heavier fork (dropped N block(s), …)`) — a significant, operator-visible
  consensus event, and the precise signal the e2e asserts. `-block-peers` models a transport fault, not
  Byzantine behaviour (the node stays honest); a real deployment never sets it. Second of #184's four
  consensus-safety cases (after equivocation→slash); low-bond→reject and forged-block→reject follow.
  Whole suite green with \`-race\`; the e2e passes over real sockets.
- **#184 verify — equivocation→slash proven over the REAL WIRE (adversarial-daemon harness)** (2026-08-07,
  [#184](https://github.com/nerolabs/silt/issues/184)) — The accountability property "a proven
  double-sign costs standing" (D2) is now exercised against real daemons over real TCP, not only in the
  in-process sim. A new **`-equivocate` red-team daemon flag** (quarantined in `core/node/adversary.go`,
  loudly announced, reached by no honest path) makes a validator DELIBERATELY double-sign: it places
  block X at height 1 on one honest peer and a heavier conflicting fork (Y, Z) on another. The honest
  detector syncs the heavier fork, reconciles across the two histories, and `chain.FindEquivocations`
  catches the adversary signing two different blocks at the same height — slashing it. Because
  fork-choice is summed qualified-attester weight, the heavier fork is deterministic, so the e2e
  (`TestEquivocatorSlashedOverTCP`) is not a timing race. Also adds a `Node.OnSlash` callback so the
  daemon surfaces slashing on stdout (`chain: slashed equivocator …`) — a real operator-visible
  accountability event, not only a debug-log line. Keeping the adversary in the shipped binary (behind
  the flag) means it runs in CI *and* lets an external red-team drive the same attack against a
  deployment to confirm the defence holds. First of #184's four consensus-safety cases; partition→heal,
  low-bond→reject, and forged-block→reject follow. Whole suite green with \`-race\`; the e2e passes over
  real sockets.
- **D-DEMAND P2 — the optimistic fair-exchange abort-safety floor** (2026-08-07,
  [#181](https://github.com/nerolabs/silt/issues/181)) — The demand exchange is content C (server →
  fetcher) ⟷ a delivery receipt (fetcher → server). Fair exchange provably needs a TTP (Pagnia–
  Gärtner); silt's is the validator quorum as a threshold-distributed TTP (Asokan–Shoup–Waidner),
  invoked only on dispute. This ships the **optimistic phase + both abort-SAFETY properties**, which
  hold structurally today: **(1) fetcher-side** — an aborted exchange never *consumes* the token (a
  serial is spent only by a completed `Redeem`), so a server that takes the commitment and delivers
  nothing leaves the paid token reusable at another server (the fetcher can't be robbed of its token);
  **(2) server-side** — a fetcher's pre-release `ExchangeCommitment` (a signed promise made before
  content release, domain-separated from the receipt and carrying no PoR proof) can *never* redeem as
  demand, so a server can't turn "the fetcher engaged" into a fake completed delivery — the
  unforgeability bound `#receipts(C) ≤ #completed correct deliveries` survives the abort path.
  Regression-locked (token-reusable-after-abort, commitment-is-not-a-receipt, domain separation,
  optimistic path still credits). *Gated, deliberately not built: the dispute-RESOLUTION half — turning
  a server-held commitment into a TTP-affidavit on fetcher default requires the quorum to verify
  delivery completed without the fetcher — i.e. verifiable escrow of the content key (Camenisch–Shoup)
  + threshold decryption t-of-n across validators. The threshold-decryption/DKG half is available in Go
  (dedis/kyber, drand-grade); the wall is the verifiable-escrow primitive (no adoptable pure-Go impl)
  plus the large new crypto trust surface — disproportionate to a neutral observable. Same strategy as
  H7 (floor now, heavy crypto as a fast-follow), different primitive. Demand-neutrality keeps this
  low-stakes: an unresolved abort only undercounts a neutral observable, never standing.
  `ExchangeCommitment` is the seam the future resolver consumes.* Whole
  suite green with \`-race\`.
- **D-DEMAND P3b — the bonded-fetcher credential (second cost-to-wash lever)** (2026-08-07,
  [#181](https://github.com/nerolabs/silt/issues/181)) — Witnessed demand can now be gated on a
  **bond-distinct fetcher credential**: with `demand.Bank.RequireBondedFetcher` (wired on the daemon
  as `Node.RequireBondedFetchers`), a delivery receipt counts toward an object's demand only if the
  fetcher's signing key is a bond-distinct identity in the **committed on-chain bond ledger**
  (`chain.IsBonded` — the same Sybil-priced, deduped supply the C2 metric measures), and demand then
  counts **distinct bonded fetchers per object**. So a self-dealer running one bonded identity can
  still mint N perfectly valid receipts (a self-fetch *is* a real paid delivery — Douceur is
  unbeaten), but witnessed demand rises by **1, not N** — re-pricing wash to *one real storage bond
  per faked unit of demand*, the best achievable under no-center. This is the second lever alongside
  the already-shipped **P3a fee-burn** (each wash burns a real retrieval fee). Demand stays a
  **neutral observable** throughout — the gate changes what *counts* as demand, never whether demand
  touches consensus standing (it never does; the γ→1/N firewall is intact). Off by default (raw
  count, unchanged). Self-dealing red-team locked at both the pure layer (`core/demand`: unbonded →
  0, one bonded identity washing 6 → demand 1, 4 distinct bonded → demand 4) and the real node wire
  (`sim`: one bonded identity washes 5 → demand 1, unbonded delivery → 0, a distinct bonded identity
  → +1). *Residual: the credential shows the bonded key in the clear, so fetcher-unlinkability stays
  nominal until D3/H8 — the `demand.BondCheck` doc marks the exact seam for a blind bond-distinctness
  proof.* Whole suite green with \`-race\`.
- **Registry economics — `-freeload` role separation for the daemon** (2026-08-09,
  [#47](https://github.com/nerolabs/silt/issues/47)) — A daemon can now be started with `-freeload`
  to serve the **registry / relay / routing** role while **refusing to store or serve content** — so
  a public-infrastructure operator can run a rendezvous registry without being conscripted into
  hosting arbitrary content (the conflation that caps how many public registries the network can
  attract, which bootstrap/NAT-traversal depend on). The mechanism (`node.SetFreeload`, honored by
  the serve paths) already existed and was sim-only; this exposes it on the real daemon and announces
  the role. The node still carries DHT routing. *(The leaner `-registry-only` mode — no storage node
  constructed at all — is the follow-up.)* Covered by a real-TCP e2e; whole suite green with \`-race\`.
- **H9 takedown transparency — the CT-style append-only log** (2026-08-09,
  [#180](https://github.com/nerolabs/silt/issues/180)) — New `core/translog`: an RFC 6962
  (Certificate Transparency) append-only Merkle log — adopted, not invented (B8) — for honored
  revocations. It offers the two proofs that make a takedown **auditable and non-silent**:
  **inclusion** ("revocation R is entry i of the log at size N") so a specific takedown is provably
  recorded, and **consistency** ("the log at size M is a prefix of size N") so an operator can't
  quietly rewrite history — a dropped or back-dated revocation breaks the consistency proof.
  Exhaustively tested: prover-generated inclusion paths (every leaf × every size) and consistency
  proofs (every prefix pair) cross-check against the recomputed roots, and a tampered history fails.
  This is the M0-honest core of pluralistic takedown; the ZK non-globality predicate + PIR-routed
  probes on top are post-M0. **Wired into the chain:** every honored revocation/un-revocation is
  appended to the log in `Chain.apply` (a deterministic function of the committed blocks, rebuilt
  identically on replay), and the chain exposes `RevocationLogRoot` + inclusion/consistency proofs +
  the exported `RevocationLeaf` so an auditor can reconstruct a leaf from public block data — so silt
  can now *prove* a takedown was recorded and that its takedown history was never silently rewritten.
  Whole suite green with \`-race\`.
- **D-DEMAND — the delivery receipt goes live on the wire** (2026-08-09,
  [#181](https://github.com/nerolabs/silt/issues/181)) — The `core/demand` primitive is now a real
  node capability. A fetcher `AcquireDemandToken` blind-withdraws a retrieval token from an issuer over
  the existing token-request wire (no new issuance path — the blind signature is domain-agnostic), then
  `SubmitDeliveryReceipt` sends the server a `MsgDeliveryReceipt` (the token + a PoR-bound, signed ack
  over the received bytes). The server verifies it against the issuer key it trusts and banks it into a
  **neutral witnessed-demand observable** (`Node.WitnessedDemand`) — **never wired to consensus
  standing**, so a forged or self-dealt receipt buys zero standing (the γ→1/N firewall). Only receipts
  naming this server are banked; replays (double-spent serial) and forged/mis-issued tokens are rejected
  over the wire. Integration sim covers the honest flow + both rejections. Whole suite green with
  \`-race\`. *(Fee-burn cost-to-wash is P3; fetcher-unlinkability needs D3.)*
- **D-DEMAND P1 — blind-withdrawn retrieval token** (2026-08-08,
  [#181](https://github.com/nerolabs/silt/issues/181)) — The demand token is now **blind-withdrawn**:
  `core/blindtoken` gains a domain-separated demand variant (`BlindDemand`/`VerifyDemand` — a demand
  token can't be presented as a publish token or credit under the same key), and `core/demand` upgrades
  the token from a placeholder issuer-signed serial to `Withdraw → SignWithdrawal → Unblind`. The issuer
  blind-signs the token **without learning its serial**, so the token that later redeems is
  cryptographically unlinkable to its withdrawal. Fetcher-unlinkability stays **nominal until D3
  issuance-mixing** (H8) closes the IP/timing channel — the blind signature hides the serial, not the
  withdrawer's network identity. The P0 unforgeability red-team carries over (now over blind tokens),
  plus an unlinkability regression. Whole suite green with \`-race\`.
- **D-DEMAND P0 — the blind demand receipt primitive (witnessed delivery, unforgeable-at-the-token-level)** (2026-08-08,
  [#181](https://github.com/nerolabs/silt/issues/181)) — First phase of the B axis (served-demand) of the
  systemic claim. New pure `core/demand`: an issuer-signed retrieval **token**, a **PoR-bound
  delivery-ack** (the fetcher signs a Shacham–Waters proof over the delivered bytes, with the challenge
  bound to `serial‖object‖server`), and a **bank/redeem** that credits a per-object *witnessed-demand*
  counter once per token. It proves exactly one thing — **`#receipts(C) ≤ #issued-tokens-spent-on-a-signed-C-delivery`** —
  and, per the decision's doc-truth rule, deliberately does **not** prove demand *authenticity* (a
  self-fetch is a real paid delivery; a Douceur limit, re-priced by cost-to-wash in P3, never proven).
  - **NEUTRAL by construction.** A redeemed receipt is an *observable* (`Bank.Demand`) that is never
    wired to consensus standing — so even a forged or self-dealt receipt buys **zero** standing, keeping
    the γ→1/N shared-content firewall intact (fusing demand into standing stays gated on #182). Standing
    is bond-only today.
  - **Unforgeability red-team** (each a permanent regression): a token not issuer-signed, a
    tampered/lifted receipt (server/object/fetcher/sig), a receipt claiming object C' while holding C's
    bytes (the PoR binding, not just the signature), a data-less "delivery", and a double-spent serial —
    all rejected; only an honest signed delivery credits demand. The public-per-object-key tag-forgery
    residual is documented (H7 precedent; inert because demand is neutral).
  - P1 blind withdrawal, fetcher-unlinkability (needs D3/H8), P2 fair-exchange dispute, and P3
    cost-to-wash economics remain. Whole suite green with \`-race\`.
- **C2 metric wiring — cost-to-corrupt from the committed bond ledger, split-resistant shed** (2026-08-08,
  [#185](https://github.com/nerolabs/silt/issues/185)) — The "no quiet capture" axis (C2 / D-C2) gets a
  first-class, published concentration measurement. `chain.C2Metric()` computes
  `{NakamotoBonds, NakamotoOperators, CostToCorruptBytes, TotalBondedBytes, Margin}` over the
  **committed on-chain `BondReg` ledger** — never gossip, which kills the "lie about your size" *skew*
  half of the skew+split attack outright. It was previously a private helper that only gated the
  training-wheels shed; now it is a single measurement consumed by the shed and surfaced for operators.
  - **Split-half defense via an operator margin.** A `BondReg` carries no operator label, so real
    key→operator clustering is impossible on-chain; instead a config **`OperatorMargin M`** discounts the
    bond-distinct coefficient to `NakamotoOperators = ⌊k̂/M⌋`, and `Mature()` sheds the anchor
    training-wheels only when `k̂ ≥ MatureValidators × M` — so a stake split across many keys must clear
    `k·M` distinct bonds. `M=1` (default) is the legacy/single-operator behavior, unchanged; the daemon
    exposes `-operator-margin` and narrates the metric on every commit (`nakamoto N bonds → M operators |
    cost-to-corrupt … | wheels shed/engaged`).
  - **Honest residuals (D-C2, unchanged):** operator clustering is heuristic *by theorem* (Kwon) — `M`
    only bounds it; `M_est` under adversarial NodeID placement is unquantified; the honest-whale / real
    cartel is outside C2. Byzantine-robust *sampling* and the private-lookup committee-certification
    consumer (H8/#179) are future. Unblocks the external C2 red-team (#183). Full unit coverage of the
    metric arithmetic + the split-resistant shed; whole suite green with \`-race\`.
- **H7 proof-of-correct-repair — the false-repair red-team (acceptance gate)** (2026-08-08,
  [#95](https://github.com/nerolabs/silt/issues/95)) — The self-dealing adversary is driven against
  the **wired** verification handler over a live network (`core/node/redteam_repair_claim_test.go`),
  proving the crypto's verdict actually reaches the ledger, each case a permanent regression:
  - **(a) garbage claim → slash.** A claim naming a real position but a bogus shard id: the judge
    recomputes the position from the manifest-anchored survivors, sees the mismatch (a self-attributing
    fraud proof), and **slashes the claimant** — no bounty.
  - **(c) compute-but-don't-store → denied, never slashed.** A correct shard id on a data-less liar
    holder (keeps the proof + PoR tags, drops the bytes) fails its identity-bound retrievability
    challenge → **denied** but not punished (a shortfall may be transient). This also pins the
    **(b) anti-double-count** property: retrievability binds to the *named* holder, so "the correct
    bytes exist on the survivors" does not pay.
  - **Positive control** — an honest claim on a real holder clears both legs, the holder is paid, and
    **no standing moves** — so the deny/slash cases are discriminating, not blanket rejection.
  - **(d) quorum discovery** — every caretaker announcing under the `careKey` rendezvous is found, so
    none is silently excluded from the vote. *(Domain-diverse quorum SELECTION — refusing a
    single-domain quorum — is explicit deferred hardening, tracked with caretaker-selection work.)*
  Whole suite green with `-race`.
- **H7 finite-but-renewable durability — instrument `g` + the funded horizon (slice 3)** (2026-08-08,
  [#95](https://github.com/nerolabs/silt/issues/95)) — silt does not *promise* perpetual cold-data
  solvency (that promise is the Arweave endowment identity in credits, and it holds only while the
  credit-denominated cost of storage keeps falling — which 2020s hardware evidence questions). So
  durability ships as an explicit **finite-but-renewable** contract, and this slice makes where an
  object sits on it **measurable** (decision D-S7):
  - The escrow now tracks a **repair count** (`PayBounty` increments it), and a per-object
    `ports.DurabilitySnapshot` (reserve, lifetime funded/paid, repairs) crosses the `CreditLedger`
    interface — read-only, classified `neutral` under the Invariant-A guard.
  - New pure instruments in `core/credit` read a snapshot: **`CostPerRepair`** (realised credits per
    shard-repair), **`Horizon`** (how long the reserve lasts at the *observed* burn — returning a
    `finite` flag so "no burn yet" reads as *unproven*, never *perpetual achieved*), and **`G`** —
    instrument *g*, the annualized trend of cost-per-repair, signed so `g > 0` means cost is
    **declining** (the condition under which "perpetual" becomes earnable). `g` stays **measured**,
    never assumed.
  - A bounty payment now narrates the drawn-down reserve and cost-per-repair (`Node.DurabilitySnapshot`
    exposes the accounting for the observatory). Full unit coverage of the instruments + a repair-loop
    sim asserting the snapshot's repair count matches bounties released and the funded horizon is a
    positive finite runway; whole suite green with `-race`.
- **H7 self-funding durability — the serve auto-skim goes live** (2026-08-08,
  [#95](https://github.com/nerolabs/silt/issues/95)) — The escrow that pays repair bounties is now
  topped up by the object's own traffic. The `MsgFetchChunk` serve path resolves each coded shard's
  object root from its storage proof and routes the serve through `RecordServeToObject`, which
  diverts a protocol-fixed slice (`SkimNum/SkimDen`, 1/8) of the serve revenue into *that object's*
  durability reserve — so **popular data pays for its own repair** while the server keeps the net.
  Shards with no proof-anchored root (manifest chunks, uncoded files) keep the plain serve.
  - **Publisher/operator funding API** — `Node.FundDurability(root, amount)` prepays an object's
    reserve from the node's own balance (a pure balance move, never standing), so cold data can be
    endowed to outlive churn before it is popular enough to self-fund; `Node.DurabilityReserve(root)`
    reads the remaining horizon. `ports.CreditLedger` gains `RecordServeToObject`. *(A publisher-side
    CLI subcommand waits on the client credit-balance model; the node API is the entry point today.)*
  - **The invariant holds.** Serve income funds the balance economy and the durability reserve —
    **never** standing. The integration sim retrieves a whole file, watches the reserve fill from the
    serves (a *slice* of the bytes, not the whole thing), and asserts no node's `Reputation` moves.
    Full unit + sim coverage; whole suite green with `-race`.
- **H7 proof-of-correct-repair — the node/network quorum wiring** (2026-08-07,
  [#95](https://github.com/nerolabs/silt/issues/95)) — The `core/repairproof` gate is now wired into
  the live repair loop, so a durability bounty actually flows on a *verified* repair. When a
  caretaker rebuilds a lost shard and places it on a fresh holder (`repairStripe`), it emits a
  `MsgRepairClaim` naming that holder; the object's other caretakers — reached through a new
  **`careKey` rendezvous** (`hash(root ‖ "silt/care/v1")`, announced on `Care`), since only a
  care-link holder has the layout key needed to judge — each independently run both legs:
  - **Verify** (`handleRepairClaim`, `core/node/repairclaim.go`) — reload the layout, fetch k
    survivors *by column* (verifying each against its committed id, dropping what it didn't already
    host — a paramedic, not a hoarder), `VerifyByRecompute` the claimed position, then challenge the
    holder's retrievability under the identity-bound `RepairChallengeSeed`, and `Decide`.
  - **Settle on the LOCAL ledger** — release pays the *new holder* from the object's escrow
    (`PayBounty`, capped by the rarest-shard `BountyFor` multiplier); a self-attributing correctness
    lie slashes the *claimant* (`SlashFalseRepair`). Credit is per-node-local accounting, so each
    caretaker-judge settles independently and the τ-of-q quorum is the emergent property that τ
    honest judges reach release — no on-chain bounty transaction.
  - **The invariant holds through the wire.** A bounty is a pure *balance* motion — the integration
    sim churns a stripe, watches a peer caretaker verify and release the reserve, and asserts **no
    node's consensus standing moves at all**, so the γ→1/N shared-content hole stays shut.
    `ports.CreditLedger` gains `PayBounty`/`SlashFalseRepair`/`FundEscrow`/`EscrowBalance`; new
    `RepairBountyBase`/`RepairQuorumTau` config (bounty economy off by default). Full unit coverage
    (settlement truth table) + happy-path sim; whole suite green with `-race`. *(The full
    self-dealing red-team — garbage claim → slash, relay double-count → denied, compute-but-don't-store
    → denied, quorum domain-packing — and the caretaker-discovery hardening land next.)*
- **H7 proof-of-correct-repair — the verification layer, slice 2 (logic + wire)** (2026-08-07,
  [#95](https://github.com/nerolabs/silt/issues/95)) — A repair bounty must pay only for a *real,
  correct* repair, never a bare claim. New `core/repairproof` composes the gate, unit-tested end to
  end short of the network wiring:
  - **Correctness leg** (`VerifyByRecompute`) — reconstruct the lost shard from k survivors and check
    it is byte-identical to the manifest-committed shard ID. Sound, pure-Go, publicly checkable,
    content-blind. *(A soundness pressure-test proved the plaintext-blind homomorphic-commitment path
    impossible in pure Go over silt's GF(2⁸) storage — there is no ring homomorphism GF(2⁸)→F_r — so
    M0 ships this recompute floor; the blind upgrade is a documented fast-follow. See
    [`docs/design/h7-proof-of-repair.md`](docs/design/h7-proof-of-repair.md) §3, §13.)*
  - **Retrievability leg** (`VerifyRetrievability` + `RepairChallengeSeed`) — a Shacham–Waters PoR
    challenge bound to the holder's own node identity, closing the relay/double-count attack (reuses
    `core/por`).
  - **Release/slash gate** (`Decide`) — release iff correctness holds *and* a τ-of-q retrievability
    quorum confirms; a failing correctness recompute is self-attributing fraud → slash. Backed by a
    new `credit.SlashFalseRepair` press (classified `reduces` under the Invariant-A guard: it can
    only ever *lower* standing).
  - **Repair-role model** decided from the real code: silt's repair is a *paramedic split* (the
    caretaker reconstructs but keeps nothing), so the bounty pays the **new holder** of the rebuilt
    shard (§8b). Wire types (`MsgRepairClaim`/`MsgRepairVote`, `RepairClaim`) landed; the node quorum
    handler + hot-path hook + sim/e2e are the next slice.
- **H7 durability-escrow primitives — the S7 funding layer, slice 1** (2026-08-06,
  [#95](https://github.com/nerolabs/silt/issues/95)) — The repair loop that keeps content alive
  under churn must be paid in equilibrium, not charity (the wound that killed Freenet/GNUnet).
  New in `core/credit/escrow.go`: a **per-object durability reserve** (`FundEscrow`), keyed by an
  object's root, that pays repair bounties; an **auto-skim** (`RecordServeToObject`) that routes a
  protocol-fixed fraction — `SkimNum/SkimDen`, 1/8 — of each object's serving revenue back into
  *that object's* reserve, so popular data self-funds its durability while cold data draws down
  what it prepaid; a **rarest-shard bounty multiplier** (`BountyFor`) that scales the payout by how
  under-replicated a stripe is, so repairing the last spare before data loss pays the most; and a
  `PayBounty` draw-down that pays what the reserve can cover (a short reserve = the object's funded
  horizon running out, *finite-but-renewable*, not an overdraft).
  - **The one load-bearing invariant is enforced structurally.** The durability budget lives in the
    *balance* economy and confers **zero** consensus standing — a durability credit that bought
    standing would re-open the shared-content γ→1/N hole (one physical copy of an erasure-coded
    shard answering for N pledges). The `Invariant-A` reflection guard (`invariant_a_test.go`) now
    classifies every escrow press `neutral` and fails the build if a new one ships unclassified;
    the behavioral half fires funding, skimming, and bounty-payout against a bondless identity and
    asserts `Reputation` never rises above zero. Standing is still minted by the bond press alone.
  - Prototype-first: these are the ledger primitives. Wiring the auto-skim into the live serve path
    and gating `PayBounty` on a verified proof-of-repair transcript are later H7 slices (2 and 3).
    Full unit coverage; whole suite green with `-race`.

### Changed
- **External-audit honesty propagation: held-in-tension residuals carried from the spec down
  to the tenets, risk surface, and public site** (2026-08-06) — Two independent audits of the
  docs pass (a research *comprehension* audit + a red-team *intention* audit) found
  comprehension faithful but a **propagation gap**: the honesty that was correct in `m0.md §10`
  / issue #182 hadn't reached the tenet layer, the risk-tracking surface, or the public pages,
  so three things read as *achieved* that are deliberately *open*. No code changed. Fixes:
  - **The S7 "one ledger" fusion** (served-content ⇄ standing) reworded across `TENETS.md` S7 +
    `m0.md §5` from an achieved fact to the **design goal** — today standing comes **only** from
    the dedicated identity-keyed bond plot, **separate** from served content, gated on the
    γ→1/N problem (#182). A builder implementing the old wording would have re-opened the Sybil
    break the separation prevents.
  - **`C_honest = D×A×T×B`** marked *target composition vs. shipped subset* (`m0.md §3`, TENETS
    Part 0): today standing is gated by the **bond (D) axis alone** — B (served demand) is
    unbuilt (#181), A (address diversity) is at the DHT layer, not in the standing number — so
    C1 is a *conditional* claim. Added the missing served-demand row to the `m0.md §6` as-built
    map (NOT SHIPPED → #181).
  - **γ→1/N** added as an explicit open-risk row in `risk-register.md` + `threat-catalog.md`;
    the "proof-of-repair now EXISTS" durability headline softened to *construction designed,
    not yet built (H7/#95)* across `threat-catalog.md`, `TENETS.md`, `decisions.md`.
  - **D-PRIV propagation:** `TENETS.md` Part VIII table row "Privacy of *access* is absolute"
    corrected to the refusal-to-surveil form; `decisions.md` "publish-unlinkability is delivered"
    → *chain-layer only; transport IP+timing OPEN until D3 (H8/#179)*.
  - **Public site regenerated** (`index.html`/`node.html`/`docs.html`): the Sybil-standing copy
    ("reputation = audits + bytes served / bandwidth counts toward reputation") corrected to
    bond-backed standing; the unlinkability hero requalified (opt-in blind tokens + IP+timing
    caveat); "alive forever" → finite-but-renewable; "no token" → "no *speculative external*
    token"; "private by architecture" → content-blind.
  - **C2 "no quiet capture"** promoted to a first-class decision entry (`k*≥k̂/M`, Kwon floor,
    honest-whale + adversarial-placement residues); added risk rows for `g≤0` and CPR under
    adversarial NodeID placement; reconciled the `threat-model.md` BFT self-contradiction.
- **Full non-code file audit + remediation; stray binary removed** (2026-08-06) — Audited
  all 106 tracked non-code files (purpose · last-updated · needed? · safe-to-remove/archive ·
  staleness). Findings actioned; no Go behavior changed except one web-UI default (below).
  - **Stray removed:** `shardnet` — a 5.1 MB Mach-O binary committed under the project's old
    name — deleted and gitignored. No other committed strays or dead files found.
  - **The one factual contradiction fixed:** `docs/risk-register.md` still said center-less
    proof-of-repair was "routed to research"; it's **delivered** (D-S7) — corrected, plus
    finite-but-renewable durability and D-DEMAND (cost-to-wash pricing).
  - **`docs/threat-model.md` reconciled** (public disclosure doc): the Sybil/eclipse, PoR,
    free-rider/wash, colluding-quorum, and trust-assumption sections rewritten from the old
    "reputation quorum / storage bond / DHT eclipse unhardened / Gate 4" framing to the
    current **C1 + C2 composition** (objective bonded fork-choice, H5 eclipse hardening,
    D-DEMAND wash re-pricing, private-by-default).
  - **`website/docs.html`** consensus section + meta refreshed from "reputation-quorum" to
    the objective bonded-quorum / C1·C2 framing; link-format copy corrected for
    private-by-default.
  - **H6 behavioral gap closed:** `cmd/silt/ui/publish.html` defaulted the web publish mode
    to `convergent`; now defaults to **private** (matching the CLI), with the confirmation-
    attack caveat.
  - **Staleness sweep:** `docs/math/02` + `docs/math/07` (convergent-as-default → private),
    `docs/math/05` (retired "Gate 4 #90" citation), `docs/math/08` (H4 Byzantine quorum
    note), `docs/design/cross-network.md` (relay incentives → D-DEMAND), `docs/threat-catalog.md`
    + `docs/safety-denylist.md` (backfilled the 08-06 commission facts).
  - **Archive hygiene:** `docs/fresh-eyes-council.md` archived (a new council brief added at
    `docs/reviews/fresh-eyes-council-brief.md`); `docs/design/bond-audit.md` archived with a
    live wire-protocol stub left in place; 6 broken intra-archive relative links and 2 stale
    "LIVING/current" banners fixed; `archive/README.md` index updated. `.gitignore` deduped.
- **ROADMAP + BACKLOG reconciled to the current strategy; the retired Gate 0→6 spine
  removed** (2026-08-06) — Both planning docs still narrated the old builder-phase spine
  ("V1 = Gate 0→6, **Gate 4 is the M0 mechanism to build**"), which predates the mechanism
  being built, the composition reset, and the research commission. `ROADMAP.md` rewritten to
  the honest current status (storage plane field-proven; M0 mechanism BUILT + H1–H6 hardened;
  mission reframed as **C1 + C2 held in tension**; commission answered) and the **forward
  tracks** that replace the gate spine: **build** (H7 durability/proof-of-repair — next; H8
  metadata privacy/D3; H9 takedown CT-log + non-globality metric; D-DEMAND blind receipt; the
  C2-metric-from-ledger wiring; registry economics), **verify** (multi-machine field test +
  external red-team vs C1/C2 — the gate to "M0 held"), and the **research frontier**
  (shared-content sealing boundary; MSR proof-of-repair; CPR under adversarial placement).
  `BACKLOG.md` slimmed to genuinely-open captured ideas + repointed at
  `docs/design/m0.md` / `docs/decisions.md` as the source of truth (shipped placement /
  networking / observability / fresh-eyes work moved out — it lives in git + buildlog). No
  code changed. GitHub issues reconciled in the same pass (Gate-4 mechanism issues closed as
  built; new build/verify/research-frontier tracks filed).
- **Research commission answers folded into the decision ledger; the two routed-to-research
  constructions now EXIST** (2026-08-06) — The follow-up research commission
  (`silt-reviews/research/research-outcome/commission/`, eight footnoted memos) answered the
  questions `docs/reviews/research-brief.md` had routed out. Recorded across
  `docs/decisions.md`, `docs/design/m0.md`, and `docs/TENETS.md`; no code behavior changed.
  - **D-S7 — construction DELIVERED + durability restated finite-but-renewable.** Center-less
    **proof-of-correct-repair now exists** as a composition of proven parts (a transparent
    binary-field polynomial commitment [FRI-Binius, no trusted setup] for *correctness* +
    Shacham–Waters PoR for *retrievability* + a DAS quorum for *center-less checking*) — ~100 B
    proof, no plaintext seen, no new primitive for the plain-RS case → build track **H7**.
    Durability ships as an explicit **finite-but-renewable** contract, not "perpetual":
    perpetual cold-data solvency is the Arweave endowment identity in credits and holds only
    while a positive credit-denominated cost decline `g > 0` (which 2020s hardware no longer
    guarantees), so silt funds a renewable horizon and **instruments `g`** as the number that
    decides perpetual-vs-finite. (MSR/regenerating-code proof-of-repair stays genuinely open,
    off the critical path.)
  - **D-TAKEDOWN — non-globality metric CONSTRUCTED.** A *survivor Nakamoto coefficient over
    failure domains*, published as a certified lower bound `≥ t` via a **ZK threshold
    predicate** that reveals only the scalar `t` (defeating the discovery-oracle) — as real as
    the (non-cryptographic) independence oracle. Stays low-urgency → H9.
  - **D-DEMAND (new decision).** Standing is priced on **cost-to-wash, never receipt count**.
    The blind demand receipt (Chaumian token + PoR-bound delivery-ack + quorum-as-TTP fair
    exchange) delivers unforgeable-delivery + fetcher-unlinkability, but **demand *authenticity*
    is a Douceur limit** — self-dealing is uncloseable by any receipt; wash is re-priced (burned
    fee + bonded-fetcher credential), not proven away.
  - **The core open problem, named precisely.** `B5` proves **C1 (no discount) is a theorem
    under H1–H3**; the single surviving economy of scale is the **shared-content sealing
    boundary** (plain PoR over shared erasure-coded shards leaks γ→1/N, closed only by
    identity-keyed PoRep sealing). silt is **not exposed today** — standing uses a dedicated
    identity-keyed bond plot, not the shared shards — but fusing served content into standing
    without leaking γ→1/N is the open, academic-collaborator task (`docs/design/m0.md` §10).
    Cross-cutting engineering find (`B1`): compute the C2 concentration metric's weight from the
    **committed on-chain bond ledger, not gossip** — one measurement feeds three seams.
- **M0 reframed as a systemic composition (not a Sybil-proof primitive); tenets amended and
  docs reset** (2026-08-05) — Adopting the research capstone (`09-m0-as-composition.md`), M0's
  Sybil corner is now stated as a **systemic** claim — **C1 (no discount) + C2 (no quiet
  capture)**, held in tension — rather than a per-primitive "Sybil-proof" claim that is false by
  theorem (Douceur: no single primitive prevents Sybils under free identity + no permanent
  center). This changes what "done" means: a primitive failing a standalone is-it-Sybil-proof
  test is *expected*, not an M0 failure; the verdict target is the composition and its seams.
  - **`docs/TENETS.md` amended** (see the amendment log). Decisions derived from the accepted
    research package and recorded: **D-PRIV** — immutable #4 requalified from an absolute
    ("access never observable") to *refuse-to-surveil* (absolute) + *access-unobservability held
    in tension* at the metadata layer (the anonymity trilemma is a hard wall). **D-S7** — S7 now
    states the durability funding model (internal escrowable credit reserve; **no *speculative
    external* token**); center-less proof-of-repair is the open construction, routed to research.
    **D-TAKEDOWN** — immutable #5 commits every honored revocation to a CT-style transparency log
    toward a formal non-globality guarantee. **D-DISCLOSURE** — new Don't #8 (no decryption
    backdoor at core). B8/S7/immutable-#3 threaded with the composition thesis (C1/C2; the
    one-ledger S7↔Sybil-budget fusion; the young→mature maturation bet).
  - **New `docs/design/m0.md`** — the single M0 spec (thesis + interlock + surface map S1–S8 +
    the 7 composition seams = the red-team/build target + open decisions + open problems).
  - **New `docs/decisions.md`** — the decision ledger, each entry splitting derived direction
    from deferred construction.
  - **New `docs/reviews/research-brief.md`** — open questions for the research team (the two
    constructions the memos self-flagged non-existent — center-less proof-of-repair and the
    non-globality metric — plus the seam stress-tests).
  - **`/archive/`** — the finding-by-finding history moved out of the live tree (5 M0 design
    notes, 5 red-team/acceptance/audit reports, the genesis handoff) behind an index README;
    nothing deleted. The live tree now carries one current (composition) viewpoint.
  - **Every remaining non-code doc reconciled** to the composition framing (README, ROADMAP,
    threat-catalog, the 3 review briefs, and 10 others). No code behavior changed.

### Fixed
- **H6 (privacy, Memo 02): default publish is `private` — no existence oracle for guessable
  content** (2026-08-05) — convergent encryption derives the key from the plaintext, so the
  content address is a deterministic function of the plaintext: anyone who GUESSES it can
  compute the root and look it up to confirm you stored it (the confirmation attack), and
  it shipped as the DEFAULT. H6 flips the default publish mode to `private` (a random
  per-file key) across every publish path — `silt add`, `swarm add`, and the web UI — so
  identical content encrypts differently each time and can't be probed for; convergent is
  now explicit opt-in and prints a confirmation-attack warning. Regression:
  `core/pipeline/redteam_h6_test.go` — the attacker computes the convergent root of a
  guessed plaintext; under convergent a registry probe HITS (the oracle, documented), under
  the private default it MISSES, and two private uploads of identical content don't even
  collide. The Memo 02 "Proof-of-Ownership" idea was deliberately not added: a PoW-to-serve
  gate contradicts silt's capability model (the link/manifest IS the read capability) and
  possession is already gated by store-time hash verification + PoR audits, so private-by-
  default is the substantive fix (reasoning recorded in the strategy doc §7 H6).
- **H5-B (DHT eclipse, Memo 08): failure-domain diversity — a single-domain key-surround
  can't suppress discovery** (2026-08-05) — H5-A stopped provider records being *forged*;
  this stops them being *suppressed*. An adversary that grinds the NodeIDs closest to a
  content key (a ~$4 /24 key-surround) could hold every slot a lookup converges on and
  simply return nothing. Fix, reusing the gossiped failure-domain (`Domain`) signal as the
  diversity dimension: (1) the routing table caps same-domain peers **per bucket**
  (`dht.Table.SetDiversity`), so a one-domain Sybil cluster can't fill the buckets near a
  key and evict honest peers; (2) provider records are announced to a domain-**spread**
  near set, not just the NodeID-closest (`announceTargets`/`diverseNear`), so honest nodes
  in other domains hold the record; (3) after the distance walk converges onto the
  surrounding NodeIDs, resolution **sweeps** that domain-spread set (`sweepProviders`), so
  the honest holders are actually queried. `DHTDomainCap` gates it (0 = off); default
  `-dht-domain-cap 2` for the daemon and the ephemeral fetcher. Regression:
  `core/node/redteam_h5b_test.go` — an adversary grinds the 10 closest NodeIDs to a key
  (one domain, suppressing); with diversity OFF the key is undiscoverable, with it ON
  discovery succeeds through honest other-domain nodes; plus unit tests for the
  domain-spread near set and the per-bucket routing cap. Residual: `Domain` is
  self-reported — binding it to the transport-observed /24 (or per-AS) is the full-strength
  hardening. Real-TCP `e2e` green; this completes surface S5 (with H5-A).
- **H5-A (DHT eclipse, Memo 08): self-certifying provider records — records can't be
  silently forged** (2026-08-05) — DHT provider records were unsigned NodeIDs: a node
  holding the k-closest slots to a content key could fabricate provider records for
  identities that never announced, or inject fake providers into the records it re-serves
  on lookup (the forgery half of the ~$4 key-surround). Fix: `ports.ProviderRecord` is a
  signed "I hold content under key K" claim bound to the provider's identity
  (`sha256(pubkey) == ID`) and the key, with an optional expiry. A node signs its own
  announcements with its identity key (`SetSigner`), the store path (`acceptAnnounce`)
  rejects any record that isn't a valid self-announce for the queried key,
  `MsgGetProvidersReply` re-serves the signed records, and a fetcher
  (`acceptedProviderIDs`) drops any record not signed-for-this-key-and-fresh — so a
  forged, mis-signed, expired, or cross-key-replayed record is silently discarded, while a
  fetcher still hash-verifies chunk bytes on receipt. `RequireSignedProviders` is on by
  default for the daemon (`-signed-providers`); unsigned legacy records still flow when
  it's off (sim/trusted). Wire: new `ProviderRecord` type + `Provider`/`ProviderRecs`
  message fields, mirrored in the tcpnet CBOR frame. Regressions: `core/node/redteam_h5_test.go`
  (a signed record binds to identity+key; a third-party or mis-signed announce is rejected
  at the store; a fetcher drops injected forged / cross-key records; unsigned records flow
  only in non-strict mode), real-TCP `e2e` green under strict signing. **Follow-up (H5-B):**
  the *suppression* half of key-surround (prefix-diversity routing + disjoint-path/wide-
  region announce, so a key stays discoverable when one /24 owns the k-closest NodeIDs).
- **H4 (consensus safety, Memo 05): Byzantine quorum sizing + Nakamoto-coefficient shed
  metric** (2026-08-05) — consensus safety rested on a FIXED quorum (default 3) and a
  training-wheels shed triggered by a HEAD-COUNT of distinct validators. Both are Sybil-
  fragile: a fixed 3 among 30 validators no longer guarantees two quorums share an honest
  node (quorum-intersection safety is lost as the set grows), and one operator spinning up
  many keys could trip the head-count maturity, then capture consensus once the anchors
  shed. Fix, per Memo 05 (*safety is quorum arithmetic at the Byzantine threshold, not
  reputation weight*): (1) `Config.ByzantineQuorum` sizes a commit's support set (proposer
  + attesters) at a supermajority **n−f** of the qualified bonded set (f = ⌊(n−1)/3⌋), so
  any two quorums intersect in ≥ f+1 ≥ 1 honest validator; the proposer gathers
  `max(floor, RequiredQuorum())` and `ValidateCommit` enforces it. (2) `Mature()` now
  measures the **Nakamoto coefficient** over the participating non-anchor bonded set
  (`validatorsSeen ∩ current bond`) — the min number of bond-distinct operators needed to
  reach ⅓ of the weight — which is participation-gated (no fake-genesis decentralization),
  weight-aware (a set dominated by one bond has coefficient 1 → stays immature no matter
  how many satellite keys), and revertible (a lapsed bond drops out → the wheels re-engage,
  the post-shed escape hatch). Both default-on for the untrusted objective posture
  (`effectiveByzantineQuorum`, `-byzantine-quorum`). Regressions: `core/chain/h4_consensus_test.go`
  (`TestBFTQuorumIntersectionAboveFaultBound` proves two quorums always intersect above the
  fault bound for every set size; `TestByzantineQuorumScalesWithValidatorSet` +
  `TestFixedQuorumUnsafeWithoutByzantineSizing`; `TestMaturityNakamotoResistsOneOperator`
  shows one operator's many keys can't trip the wheels), `cmd/silt/invariant_b_test.go`
  (S4 default-on). Residual (documented): an operator that splits stake into many EQUAL
  bonds still inflates the coefficient — stake concentration is invisible on-chain — but it
  pays the full cost-to-corrupt and the Byzantine quorum bounds it to ≤ ⅓ of weight.
- **H2 / RT-2 (Sybil, High): bond standing decays across time by default — release-and-
  coast denied** (2026-08-05) — the blind red team broke the Sybil corner (over the G2
  fix) through the *time* axis: a validator registered a genuine bond once, **released
  the plot**, and kept voting forever off that single one-time proof, because the bond
  TTL (`BondTTLBlocks`) shipped **off by default** — the third "fixed but off by default"
  instance. It could not simply be flipped on: renewal happened only when a validator
  *proposed*, so an attest-only validator would never renew and would lapse, costing the
  quorum its weight (a liveness trap). Fix: a **non-proposer renewal path** —
  `node.SubmitBondRenewal` broadcasts a fresh self-signed `BondReg` (new
  `MsgSubmitBondReg`); a receiver re-verifies it for the current head
  (`chain.ValidateBondReg`) and queues it (`pendingBondRegs`); the next proposer folds the
  queued peer regs (deterministically ordered, head-filtered so one stale reg can't poison
  the block) into its block, mirroring `pendingSlashes`. The chain-sync sweep drives
  renewal, so an attest-only validator renews without ever proposing. **Only then** is the
  TTL made safe-by-default on the untrusted objective posture (`effectiveBondTTL`, mirroring
  the anti-release floor; explicit `-bond-ttl 0` is the trusted opt-out). Regressions: sim
  `TestObjectiveBondRenewalSustainsAttestOnlyValidator` (attest-only validator sustains
  standing across many TTL windows via the wire renewal path while a released validator is
  pruned — no liveness regression), `core/node/redteam_rt2_test.go` (TTL off ⇒ coast
  survives, the vuln; TTL on ⇒ released plot decays out), `cmd/silt/invariant_b_test.go`
  (the untrusted default turns the TTL on).
- **H3 (Sybil, systemic): Invariant-A/B guardrails so a standing press or an off-by-default
  mechanism cannot ship unaudited** (2026-08-05) — the strategy doc's two meta-patterns
  ("we fix instances, not classes" and "fixed but off by default") each bit us three-plus
  times (F1→G2→RT-1; F6→F4→G4→RT-2). Turned both classes into compile-and-test obligations:
  `core/credit/invariant_a_test.go` enumerates every standing-granting press (a reflection
  guard forces each `*Ledger` method to be classified `mints`/`reduces`/`neutral`; a
  behavioral guard proves no non-`mints` press lifts a bondless identity; the sole `mints`
  press — the bond — is asserted identity-bound + deduped + bond-gated), and
  `cmd/silt/invariant_b_test.go` builds the default untrusted-validator config and asserts
  it denies the attack per mechanism (S1 anti-release floor on, S3 bond-TTL on). A new
  press that skips classification or a mechanism that ships off-by-default now fails loudly.
- **H1 / RT-1 (Sybil, Critical): PoR audits no longer mint consensus standing —
  a disk-less relay farm earns nothing** (2026-08-05) — a fresh blind red-team broke
  the Sybil corner (over the G2 fix) via the proof-of-retrievability audit press:
  `credit.Reputation` added `auditsPassed·25` with **no bond gate**, and the PoR proof
  was a pure function of `(chunkID, challenge, data)` — not bound to the prover, and
  challenged with a shared, publicly-derivable seed. So a data-less identity could
  **relay** an honest holder's aggregated `(μ, σ)`, pass, and reach propose/attest
  eligibility (100 rep) with **zero storage** — the code's own "a liar without the
  bytes cannot answer" comment was false (relay doesn't need the bytes, only a holder
  that has them). Fix (architectural, per `docs/design/m0-hardening-strategy.md`
  Invariant A + research memo 03: *plain PoR over shared content is not Sybil-
  resistant*): **PoR audits grant no Sybil-resistant standing** — removed the mint, so
  standing rests on the identity-bound storage bond alone; audits now fund only the
  balance economy and remain a *negative* integrity signal (a failed audit still
  subtracts, and can never be Sybil-amplified). Defense-in-depth: the challenge is now
  **identity-bound** (`porProverSeed = H(base‖proverID)`), so a relayed proof for one
  identity fails another's verify. Regressions: `core/credit/redteam_rt1_test.go` (audit
  passes grant 0 standing without a bond; an **Invariant-A property test** that no press
  mints standing without a bond; failed audits still penalize), `core/node/redteam_rt1_test.go`
  (relayed proof denied), `sim/por_standing_test.go` (holder passing audits over the wire
  earns 0 standing without a bond). Standing-granting sims/tests updated to earn standing
  via the bond press. **Residual (tracked):** a *colluding bonded holder* can still
  recompute a proof per Sybil to farm *balance* (not standing) — closing that needs
  sealed real-content replicas (backlog H7). **Honest status: built + covered, awaiting
  external re-verification** (B8).
- **G2 (Sybil, Critical): the storage bond is now a VERIFIED proof-of-space —
  prefix plots can no longer back N standings from one disk** (2026-08-05) — the
  fix-verification red-team broke the Sybil corner a second time, over the F1 fix
  code, with **prefix plots**: `plotBlock`/`parentIndices` keyed only on
  `(secret, i)` and never on the total block count `n`, so blocks `0..m-1` of an
  `n`-block plot were **byte-identical** to a standalone `m`-block plot with its
  OWN distinct Merkle root — and `VerifySpaceTime` only checked Merkle *inclusion*,
  never recomputed a *label*. Per-root dedup (F1) keys on *equal* roots, so it was
  structurally unable to catch a family of *distinct* prefix roots: the scheme was
  proof-of-STORAGE, not proof-of-SPACE, and one physical plot backed ~`N`
  standings (marginal cost of one more Sybil ≈ one 4 KiB block). The fix (a
  graph-labeling proof-of-space over silt's existing DRSample graph — DFKP CRYPTO'15
  / ABH CCS'17, adopted not invented) seals the plot from a **public, identity- and
  size-bound seed** `H("silt/bond/plot/v3" ‖ pk ‖ n)` folded into both the labels
  and the parent draws, and adds a **labeling-consistency challenge**: the answer
  opens `k` challenged nodes with their predecessor and DRSample parents (Merkle-
  proven), and the verifier **recomputes** each label from the opened parent bytes
  under `H(pk, n)` and requires a match. Because the seed is public the verifier can
  do this without holding the plot, so identity and size become **checked** properties
  of the plot, not claimed ones: a prefix, a foreign-identity plot, or arbitrary
  committed bytes all fail the recompute. **N standings now require N plots.** `k` is
  a per-network knob (`-bond-label-k`, `Config.BondLabelSamples`, default 64;
  soundness error ≤ `(1-ε)^k` against an ε-short prover); leaving it unset resolves
  to 64 inside `core/bond`, so the check is never silently disabled. The seed and the
  labeling check ship **together** (a public seed without the check would regress
  griefing), and G3's "proof beats declaration" rule is load-bearing for the public
  seed's griefing-safety. Plot format **v2 → v3** — a one-time fleet re-plot; the
  disk version guard forces it so a restart never reloads an insecure v2 plot.
  Regressions: `core/bond/redteam_g2_test.go` (a prefix passes possession but fails
  the labeling check; a plot for one key fails under another; arbitrary bytes fail;
  a prefix *family* forges **zero** standings; k unset still denies),
  `sim/bond_sybil_g2_test.go` (a Sybil pointing at another node's plot earns no
  standing over the live-audit wire), `adapters/diskplot` (a v2 file loads as
  absent → re-plot), and the objective/audit e2e paths carry the ~1.5 MB label proof
  over TCP and on-chain. Design: [docs/design/m0-sybil-rebind.md](docs/design/m0-sybil-rebind.md).
  **Honest status: built + covered across all three tiers, awaiting external
  red-team re-verification — not self-certified held** (immutable B8: the tight
  `ε→k` constant and the on-chain proof-size / asymmetric-`k` mitigation are the
  carried open risks in the design note §8).
- **Retest G4-residual: the anti-release floor is now ON BY DEFAULT for an untrusted
  validator** (2026-08-05) — `#163` shipped the floor + re-challenge mechanism but
  defaulted both knobs to `0`, and the daemon did not auto-enable them on the
  earned-standing M0 path (unlike `-objective`, which *is* auto-on when `-min-rep > 0`).
  So a stock, doc-following open validator still admitted a sub-floor, releasable bond
  to full objective standing — **fixed but off by default is not fixed.** The
  anti-release floor now gets the same treatment `-objective` has: it defaults to a
  **derived 1 GiB** for an untrusted validator (`-validator` + `-min-rep > 0` +
  objective), the value the flag's own arithmetic implies (~270 MB/s plot throughput ×
  the ~2 s challenge window ≈ 540 MiB, with ~2× margin). The daemon **fails closed** if
  `-bond` is under the floor — an actionable refusal beats running a validator that
  silently earns nothing — and an operator can still opt out **explicitly** with
  `-min-bond-floor 0` for a trusted/demo swarm. A non-validator is unaffected.
  Regression: `cmd/silt/bondfloor_default_test.go` (the derived floor exists, exceeds
  what re-plots inside a challenge window, denies the default 64M bond, and an explicit
  choice — including `0` — always wins). Docs + the local walkthroughs opt out
  explicitly and now document the floor.
  **Known gap, deliberately NOT defaulted on:** `-bond-ttl` (the objective re-challenge
  cadence) stays off, because bond renewal currently happens only when a validator
  *proposes* (`chainrole.go`), and proposing is event-driven — an attest-only validator
  would never renew and would lapse, costing the quorum its standing. Defaulting the TTL
  on requires a renewal path for non-proposers first; tracked as follow-up.
- **Retest G4 (Sybil/time, High): the objective validator set now enforces an
  anti-release floor and re-challenges bonds on a cadence** (2026-08-04) — the fresh
  pass found the "time" half of proof-of-space-TIME was not enforced on the OBJECTIVE
  fork-choice path: `c.bonded` was set once at registration on a one-time proof and
  never decayed or re-challenged, and `chain.Config` had no anti-release floor at all
  (only `MinBond`). So (a) a sub-floor bond — small enough to release and re-plot
  inside a challenge window — earned full objective standing, and (c) a validator
  could prove once, RELEASE its plot, and keep voting forever with zero resident
  storage (the node-side floor + live re-challenge lived only in the credit ledger the
  objective set never reads). Two additive, deterministic knobs close it:
  `Config.MinBondBytes` (an objective anti-release floor — a bond below it earns no
  standing, rejected on the normal path and uncredited at genesis) and
  `Config.BondTTLBlocks` (objective standing LAPSES this many blocks after a
  validator's latest registration unless it renews with a FRESH space-time proof —
  height-driven, so every replica decays in lockstep). A validator that releases its
  plot cannot answer the fresh challenge to renew, so its vote decays to nothing. The
  daemon wires both: the existing `-min-bond-floor` now also feeds the chain floor,
  and a new `-bond-ttl` sets the cadence. Both default to 0 (off), so legacy/sim
  configs are unchanged. Regressions: `core/chain/redteam_verify_objective-antirelease_g4_test.go`
  (sub-floor bonds earn zero standing / are rejected; standing decays without renewal
  and persists with it) and `core/node/redteam_verify_objective-antirelease_g4_test.go`
  (through the real `bond.VerifySpaceTime`: a validator that stops renewing lapses; a
  continuously-renewing one keeps standing).
- **Retest G3 (Accountability, High regression): a genesis bond-squat can no longer
  lock out an honest validator** (2026-08-04) — the fresh pass found the F1 per-root
  dedup (`#158`) became a griefing lever when combined with the pre-existing
  unvalidated genesis `BondRegs`: a malicious genesis pre-squats an honest
  validator's real plot root under an attacker key (no space-time proof — genesis
  regs are declared), so when the true holder later registers that root on the
  normal path with a REAL, verifier-accepted proof, `apply()`'s first-owner dedup
  sees the root already claimed and drops the honest credit — the holder earns 0,
  the squatter keeps unbacked standing. Fix: **proof beats declaration.** `apply()`
  now tracks whether a root's owner claimed it with a verified proof (a height>0
  registration, gated by `validateBondRegs`) or a mere declared genesis reg
  (`bondRootProven`); a verified registration DISPLACES an unproven declared claim
  (stripping the squatter's standing), while every other collision still earns
  nothing — so once proven, first-proven-owner wins and F1 is preserved. Regressions:
  `core/chain/redteam_verify_genesis-bondsquat_g3_test.go` (inverted PoC: V's proof
  displaces the squat; a second identity still can't share the proven root) and
  `core/node/redteam_verify_genesis-bondsquat_g3_test.go` (a real live bond
  registration displaces a genesis squat through the objective space-time verifier).
- **Retest G1 (Accountability, Critical regression): a genesis block can no longer
  carry an equivocation Slash** (2026-08-04) — the fix-verification red-team's fresh
  pass over the F1/F2 code found that `#158`'s on-chain `Block.Slashes` reopened, for
  a stronger lever, exactly the door `#159` (F3) closed for `Revocations`.
  `AppendGenesis` skips `validateSlashes`, and `apply()` unconditionally evicts every
  `Slashes` culprit (`slashed[id]=true`, dropped from `bonded`, barred from
  re-earning, carried through `adopt()`), so a genesis carrying an **unverified**
  Slash was a proof-free, pre-emptive, identity-level kill switch — a fortiori what
  immutable #5 forbids. A slash is only meaningful against equivocation within a
  chain's own history, of which a genesis has none, so `AppendGenesis` now **rejects**
  any genesis carrying `Slashes` (`ErrGenesisTakedown`), symmetric with the F3 guard;
  a slash must go through the normal path where `validateSlashes` → `VerifyEquivocation`
  gates it on a real double-sign proof. Regressions: `core/chain/redteam_verify_genesis-slash_g1_test.go`
  (genesis slash denied, victim keeps standing, normal-path slash still fires on a real
  proof) and `core/node/redteam_verify_genesis-slash_g1_test.go` (a node in objective
  mode never establishes a genesis that evicts an honest bonded validator).
- **Blind red-team F4 (integrity, S1): the auditor no longer trusts a prover's
  self-reported PoR block count on the file's last shard** (2026-08-04) — the audit
  graded every leaf but the last against a block count it recomputed itself, while
  the LAST leaf took a lenient "tail" branch that accepted any `1..wantFull`. Since
  `porChallenge` clamps the sample space to the prover's reported count, a liar
  holding only block 0 of an N-block shard could report `PorBlocks=1`, be challenged
  on block 0 alone, and pass — earning rent while holding ~1/N of the shard, with no
  slash and no repair. The premise behind the leniency was wrong: `chunk.Split`
  zero-pads the last frame up to `ChunkSize` (the true length rides in the frame
  header) and erasure pads short stripes, so **every stored shard is full-size on
  the wire** — there is no short tail to accommodate. The auditor now demands the
  same recomputed full block count for **every** leaf, so a prover can never shrink
  its own challenge. Regressions: `core/node/redteam_verify_liar-por_0_test.go`
  (inverted PoC — the shrink liar's grading predicate now fails, an honest holder
  still passes) and `sim/audit_tailshrink_test.go` (integration: a shrink liar on a
  single-chunk file's sole — previously lenient — leaf is slashed into debt while
  honest holders pass).
- **Blind red-team F3 (Accountability): genesis can no longer pre-emptively revoke
  a never-published root** (2026-08-04) — `AppendGenesis` calls `apply()` directly
  and skips `validateTakedowns`, so a genesis block could carry `Revocations`
  naming a root never published — a pre-emptive takedown, exactly what immutable #5
  forbids ("a takedown is never pre-emptive"), honored forever by any node running
  `-honor-chain-revocations`. `AppendGenesis` now **rejects** any genesis carrying
  `Revocations` or `Unrevocations` (`ErrGenesisTakedown`): a genesis seeds entries
  and declared launch bonds only; a takedown must go through the governed normal
  path where `ErrRevokeUnknownRoot` enforces that the root already exists.
  Regression `core/chain/redteam_verify_censor_0_test.go` (inverted PoC: genesis
  takedown rejected; the normal-path existence guard still fires).
- **Blind red-team F1 (Sybil, Critical) + F2 (equivocation slash inert): the
  objective validator set now honors the two defenses it was bypassing**
  (2026-08-04) — a second, blind red-team pass (`ae005e9`) found that promoting
  objective on-chain-bond fork-choice to the M0 default (#154) made it authoritative
  for standing, but it skipped two defenses that lived only in the
  non-authoritative `core/credit` reputation ledger. Both are now carried into
  `core/chain`. **F1 — per-root bond dedup:** the objective set never checked that
  a bond `Root` was unclaimed, and the space-time proof is not identity-bound, so N
  cheap identities could register the *same* plot's root+answer and each earn full
  `MinBond` fork-choice weight — one 4 MiB disk buying a whole write quorum.
  `apply`/`validateBondRegs` now enforce a `bondRootOwner` map (a root credits AT
  MOST ONE identity, the first to claim it; the owner may renew), so N Sybils cost
  N independent bonds again. **F2 — on-chain equivocation slash:** `SlashEquivocation`
  only mutated the reputation ledger, which objective mode never reads, so a proven
  double-signer kept full eligibility and weight. Slashing is now an **on-chain
  record** (`Block.Slashes`, a self-verifying equivocation proof) that on commit
  **evicts** the culprit from `c.bonded` and bars it from re-earning standing —
  applied in lockstep on every replica; a forged slash is rejected
  (`ErrBadSlash`), so forged-slash griefing stays denied. The node records
  detected equivocations on-chain in the next block it proposes. Regressions:
  `core/chain/redteam_verify_*` (shared-root denied, slash evicts, forged slash
  rejected) and `core/node/objective_slash_test.go` (over the loop: a node detects,
  records, and every replica evicts).

### Security
- **M0 composition: every red-team finding (F1–F7) fixed and covered by tests —
  awaiting external re-verification** (2026-08-04) — following the red-team break
  below, all seven findings now have a shipped fix with unit + in-process
  simulation coverage, and real-TCP e2e where a daemon surface exists. Sybil
  (byte-binding over a depth-robust graph + read-bound VDF + anti-release floor),
  Privacy (ephemeral publish identity + prepaid Chaumian credits + canonical
  issuer set), Accountability (existence-checked, per-operator, reversible
  takedowns), Consensus (objective on-chain-bond fork-choice with an anchor
  cold-start; F7 resolved by F6 + sound same-height slashing). **The per-finding
  fix + how-to-verify guide for the next reviewer is
  `docs/reviews/M0-REDTEAM-VERIFICATION.md`.** This is the builder's response, NOT
  a self-certification: M0 is *held* only when a fresh external red-team denies all
  three failure modes. Deliberately deferred residuals (honestly recorded): the
  public-IP issuance IP+timing refinement (F4; the stronger NodeID/fee/subset links
  are severed and NATed clients already relay), and flipping the objective-mode
  default (a launch-config decision).
- **M0 external red-team: primitives real, composition unproven, M0 not yet
  held** (2026-08-04) — the independent M0 red-team ran against shipped code
  (`c1397e0`) and **broke all three corners in the novel composition**. The
  adopted primitives held (the Wesolowski VDF and the Shacham–Waters PoR were
  attacked and denied). Full report: `docs/reviews/M0-REDTEAM-REPORT.md`;
  live status carried in `docs/design/gate4-m0-mechanism.md`. This supersedes
  earlier changelog language that presented the corners as resolved.
  - **Accountability** — 🟢 **FIXED (below, #136).**
  - **Sybil** — 🔴 **BROKEN (F1/F2/F3):** the PoST plot binds only the 32-byte
    block *leaves*, not the block bytes, so a prover holds ~1/128 of the storage
    it is charged for (→0 for small bonds, re-plotted inside the VDF window); and
    the VDF "time" half gates nothing because its challenge input is public.
    Earlier entries claiming "N distinct blobs of real storage" and "cannot
    release the space and re-plot" are **false against this attack** and are
    corrected in-code (`core/bond/bond.go`). Fix = bind to block bytes
    (memory-hard/DRG) + a pre-VDF plot read; mechanism design turn.
  - **Privacy** — 🔴 **BROKEN (F4):** the D3 issuance-mixing layer was never
    shipped, so `AcquireToken` de-anonymizes the publisher at token acquisition
    by IP+timing (and the fee debit). The residual was previously described as a
    "narrowed anonymity set"; in shipped code it is a **singleton** (direct
    de-anonymization). Fix = route issuance over the content-blind relay, epoch
    batch, decouple the fee; privacy design turn.
  - **Consensus (D2)** — 🔴 **BROKEN (F6/F7):** fork-choice weight is the
    subjective local reputation view, not objective on-chain bond, so two honest
    replicas diverge permanently; and cross-height double-backing evades the
    equivocation slash. Fix = objective bond-weighted fork-choice (depends on the
    Sybil fix) + slashing that distinguishes malicious double-backing from honest
    reorg-following; consensus design turn.

### Docs
- **M0 mechanism design turn: per-corner fix write-ups** (2026-08-04) — the three
  broken corners each get a skeptic-readable design doc that names the exact
  break (`file:line`), the adopt-don't-invent fix, the composition, the schema
  touch, and a falsifiable denial with the red-team's own PoC inverted as
  regression. **Sybil (F1/F2/F3)** — `docs/design/m0-sybil-bond.md`: a proven
  depth-robust graph over full-byte labels (closes the 1/128 gap) + a pre-VDF
  plot-read seed (releasing the space forfeits the answer). **Privacy (F4)** —
  `docs/design/m0-privacy-issuance.md`: D3 issuance-mixing — relay + ephemeral
  transport, epoch batching, canonical validator set, and a prepaid blinded-credit
  fee decoupling. **Consensus (F6/F7)** — `docs/design/m0-consensus.md`: objective
  on-chain PoST-bond fork-choice weight + Casper-FFG-style surround-vote slashing
  that spares honest reorg-followers. The Sybil bond is the keystone (consensus
  depends on it); privacy is independent. Linked from
  `docs/design/gate4-m0-mechanism.md`. Design only — no code changed.

### Fixed
- **Consensus (red-team F7): cross-height double-backing resolved — by F6 plus
  sound same-height slashing, without slashing honest reorg-followers**
  (2026-08-04) — the report's F7 (sign fork A@1, sit out B@1, sign B@2 — never the
  same height on both, evading the same-height equivocation slash) is now resolved,
  and the resolution is the honest one rather than a wrong slashing rule. Worked
  through precisely and locked in `core/chain/redteam_f7_test.go`: **(1)**
  same-height double-signing is still slashed (`FindEquivocations`, the
  distinguishable misbehavior); **(2)** cross-height double-backing is *provably
  indistinguishable* from an honest reorg-follow from the blocks alone (a validator
  that attested A@1 then followed a heavier fork to attest B@2 produced identical
  evidence), so any rule slashing "signed two incompatible forks" would slash
  honest validators — a regression — and detection correctly does not flag it (the
  guard test); **(3)** objective fork-choice (F6) neutralizes it anyway — the
  double-backer cannot make both histories stand, the heavier-bond fork wins on
  every replica. The pre-F6 design had planned Casper-FFG surround-vote slashing;
  the analysis shows it is unnecessary here (F6 neutralizes) and, for this exact
  pattern, ineffective (the spans do not surround), so a finality gadget is not
  added for M0. `docs/design/m0-consensus.md` §2b carries the reasoning.

### Changed
- **The default `-token-quorum` publish now uses the prepaid-credit path (closes
  red-team re-verification #4)** (2026-08-04) — the re-verifier confirmed the
  fee-decoupling credit mechanism works but flagged that `cmd/silt/swarm.go` still
  acquired tokens via the legacy `AcquireToken`, so a default token-quorum publish
  still hit `ChargePublish(from)` per publish. `acquirePublishToken` now **mints
  one prepaid credit per validator** (the fee is charged at mint) and **spends
  them** for the k blind signatures, so the publish itself records no per-publish
  fee debit — the credit path the mechanism was built for is now the default
  publish path, exercised end-to-end over real TCP
  (`e2e/TestUnlinkablePublishOverTCP`). The whole flow runs from the swarm client's
  already-ephemeral identity. Residual (deliberately deferred, option B): the
  IP+timing transport link (relay-forced issuance + epoch batching) — NATed clients
  already relay; a public-IP client's issuance IP/timing is the last D3 piece.
- **Objective fork-choice is now the DEFAULT for an untrusted validator (closes
  red-team re-verification #6/#7)** (2026-08-04) — the fix re-verifier confirmed
  objective mode heals divergent replicas but flagged that it was **off by
  default**, so a stock validator swarm still ran the legacy subjective path that
  diverges under partition. `silt daemon -objective` now **defaults to `true`** and
  is active for any untrusted validator (`-min-rep > 0`); a trusted swarm
  (`-min-rep 0`, self-commit) auto-disables it, and the legacy subjective path is
  now an explicit, labeled opt-out (`-objective=false`, which prints that it does
  NOT hold the M0 denial under an adversarial partition). A multi-validator quorum
  still bootstraps from the declared launch `-anchors` (the honest trustless-
  cold-start boundary); without them the daemon warns and a multi-validator swarm
  will not commit, rather than silently running the divergent path. Verified e2e:
  `e2e/TestObjectiveConsensusCommitsOverTCP` now runs with **no `-objective` flag**,
  proving the default path is objective; the legacy-path e2e/example flows opt in
  with `-objective=false`. This makes "two histories both stand" unreachable with
  stock validator flags — the residual the re-verifier asked to close.

### Added
- **Design note: rebinding the storage bond to identity and size (M0 Sybil / G2)**
  (2026-08-05) — `docs/design/m0-sybil-rebind.md`. The Sybil corner is **open**: a
  red-team pass over the F1 fix code broke it again via **prefix plots** (blocks
  `0..m-1` of an `n`-block plot are byte-identical to a standalone `m`-block plot, and
  each prefix has its own distinct Merkle root, so per-root dedup never fires). The
  root cause is that `VerifySpaceTime` checks only Merkle **inclusion** and never
  recomputes a **label** — proof-of-storage, not proof-of-space — while identity is
  asserted by a signature over an attacker-chosen root rather than verified. The note
  specifies the fix (a public, identity- and size-bound plot seed plus a
  labeling-consistency challenge the verifier recomputes without holding the plot),
  its soundness parameters (`k ≥ λ·ln2/ε`), the wire format, the build sequence, and
  the ordering constraints — including that the public seed must **never** land before
  the labeling check, and that the G3 "proof beats declaration" fix is load-bearing for
  its griefing safety. Derived by an independent researcher pass with no build context.
  **Not yet built; M0 is not held.**
- **`silt daemon -honor-chain-revocations` and `-revoke`: operate on-chain
  takedowns, with an e2e proof (F5)** (2026-08-04) — the accountability fix's
  per-operator honoring and quorum-gated, existence-checked revocation are now
  operable from the binary. `-honor-chain-revocations` **subscribes** this
  operator to on-chain takedowns (default OFF — following the chain never imposes
  someone else's takedowns; the operator-local `-denylist` is always honored).
  `-revoke <root>` makes a validator **propose** an on-chain takedown of a root
  once it has earned standing and the root is committed (retried on the loop-safe
  clock; the chain enforces existence + quorum). This completes the F5 test pyramid
  with the **e2e tier** (`e2e/TestChainRevocationCommitsOverTCP`): a validator
  drives a quorum revocation of a published root over real TCP and it commits. The
  per-operator honoring is covered at integration (`sim/revocation_test.go`).
- **Anti-release bond floor (`-min-bond-floor` / `Node.MinBondBytes`): a bond too
  small to be safe against release + re-plot earns no standing (M0 Sybil F1/F2)**
  (2026-08-04) — the byte-binding + read-bound-VDF plot makes a released prover
  recompute (memory-hard) before it can answer, but that only bites if re-plotting
  the pledged size takes LONGER than the challenge window. At the measured plot
  throughput (~270 MB/s, `bond.BenchmarkSeal`) a 500 ms window re-plots ~135 MiB
  and this daemon's ~2 s window ~540 MiB — so a bond at or below that could be
  released and recomputed just-in-time. A bond below `Node.MinBondBytes` now earns
  **no standing**, self or peer, at the live audit: `bondAuditOnce` gates both the
  self-credit and the peer-credit on the floor, so a valid answer for a sub-floor
  plot proves nothing about sustained possession. Exposed as `silt daemon
  -min-bond-floor` (default `0` = off, since every fast test/demo/NAT config uses
  tiny bonds; an open deployment sets it above window × throughput, e.g. `1G`, and
  the daemon warns if `-bond` is below it). Coverage: **unit**
  (`core/node/bondfloor_test.go` — a sub-floor bond earns 0, an at-floor bond earns
  standing) and **integration** (`sim/bond_floor_test.go` — a sub-floor validator
  is denied standing over the live audit wire while an at-floor one earns it).
  `BondVDFDelay` remains the complementary time-floor knob. See
  `docs/design/m0-sybil-bond.md`.
- **`silt daemon -objective`: run consensus on objective on-chain-bond fork-choice
  (F6), with an e2e proof** (2026-08-04) — a validator can now enable objective
  mode from the binary: `-objective` (with `-min-bond`, and requiring `-anchors` +
  `-mature-validators > 0` for the cold-start) wires the on-chain-bond verifier and
  makes the validator register its real bond live as it proposes, so eligibility,
  quorum, and fork-choice weight come from verifiable on-chain bonds instead of the
  local reputation view. This completes the F6 test pyramid with the **e2e tier**
  (`e2e/TestObjectiveConsensusCommitsOverTCP`): two `-objective` daemons bootstrap
  via anchors and drive a real objective quorum commit over real TCP, and the file
  round-trips bit-perfect — the bond-registration-and-verification protocol works
  end to end, not just in the sim. Objective mode remains opt-in at the daemon
  (the default stays the legacy reputation path); flipping the shipped default is
  the remaining step, tracked in `docs/design/m0-consensus.md`.

### Fixed
- **Consensus (F6): the objective-fork-choice cold-start — an anchor-bootstrapped
  validator set that builds itself from real bonds** (2026-08-04) — objective mode
  had a chicken-and-egg: a validator must be bonded ON CHAIN to propose/attest, but
  the first block that records bonds must itself be proposed and attested. It is
  now solved with the existing training-wheels anchors: in objective mode a
  declared anchor is eligible to propose/attest **while the network is immature**
  (`Chain.launchAnchor`), so the declared launch set commits the early blocks;
  validators register their real bonds **live** as they propose
  (`Node.RegisterBondReg`, attached by `proposeBlock`; `Chain.Objective` /
  `NewBondReg` / `BondRegNonce` are the seam); and the anchor eligibility **sheds
  mechanically at maturity** (`Mature()`). It grants **eligibility, never
  fork-choice weight** — weight is always summed real bond, so a declared anchor
  can never outweigh a proven one, and a network that never decentralizes simply
  keeps its training wheels. Coverage: **unit**
  (`core/chain/objective_coldstart_test.go` — an anchor bootstraps an empty
  objective set then sheds at maturity) and **integration**
  (`sim/objective_coldstart_test.go` — an anchor-only network with a separate empty
  ledger per node bootstraps consensus, and proposers become really bonded
  on-chain by self-registration, agreed across replicas). **Residual:** the daemon
  `-objective` flag wiring + an e2e run over real daemons. See
  `docs/design/m0-consensus.md`.
- **Test coverage backfill (build-immutable): the Accountability fix (F5) now has
  the integration tier** (2026-08-04) — the F5 fix (on-chain revocation is
  existence-checked, per-operator opt-in, reversible) had unit + node-white-box
  coverage; this adds the integration tier over the full node loop
  (`sim/revocation_test.go`): a bonded quorum publishes a root, then commits an
  on-chain revocation of it over the wire, and — the load-bearing property — the
  takedown is honored **per operator**: a subscribing node
  (`SetHonorChainRevocations`) denies the root while a node on the **identical
  chain** that did not subscribe does **not** (never a global switch); and a
  quorum cannot revoke a root the chain never committed (`ErrRevokeUnknownRoot`).
  Adds `Node.WouldDeny` — operator-facing observability for the effective,
  per-operator takedown decision. **e2e is explicitly deferred:** the daemon does
  not yet expose chain-revocation *proposing* (no revoke command / auto-propose)
  or the honor-subscription flag, so the full quorum-revocation-honoring flow is
  not drivable end-to-end; it lands when those daemon features do. A stated tier
  choice, not a silent gap.
- **Test coverage backfill (build-immutable): the Privacy fixes (F4) now have the
  integration tier** (2026-08-04) — the fee-decoupling and canonical-issuer-set
  fixes shipped unit-only; this adds the outcome-driven integration tier.
  **Fee decoupling** (`sim/credit_fee_test.go`): a publisher mints prepaid credits
  over the real node loop (charged in bulk at mint), then publishes by SPENDING a
  credit over the wire — and its durable standing key balance is **unchanged by
  the publish** (the ledger-level link severed end-to-end), the token verifies,
  and re-spending a credit over the wire is refused (double-spend). **Canonical
  issuer set** (`core/node/objectivechain_test.go`): two nodes on the same
  objective genesis surface the IDENTICAL deterministic on-chain-bonded issuer
  set, and a node with no chain surfaces none. e2e for the transport-layer parts
  (relay + ephemeral + epoch) lands with those parts, which are not yet built.
- **Test coverage backfill (build-immutable): the Sybil fix (F1/F2) now has the
  integration tier it was missing** (2026-08-04) — the shipped Sybil fix carried
  only its unit tier (the red-team PoC inverted in `core/bond`); the build-immutable
  rule (V5) requires unit + integration + e2e. Added the **integration** tier:
  `sim/bond_release_test.go` drives the property through the live audit wire
  (gossip → `MsgBondChallenge` → answer → `VerifySpaceTime` → ledger) — a
  validator that pledges a bond, advertises it, then RELEASES the resident bytes
  (holding at most the 32-byte leaves, the attacker that frees the space to save
  disk) FAILS the live audit and earns ZERO standing, while an honest full-plot
  validator earns it. A `bond.Commitment.ReleaseBlocks` / `Node.ReleaseBond`
  adversary seam (cf. `SetLiar` for PoR) models the release. **e2e** is already
  covered by `e2e/TestBondEarnedStandingCommitsOverTCP` (two real daemons proving
  bonds to each other over real TCP, exercising the fixed read-bound-seed
  protocol); the released/leaves-only adversary is proven at unit+integration
  rather than e2e because forcing it end-to-end would mean shipping attack
  behavior in the production binary — an explicit, stated tier choice, not a
  silent gap.
- **Consensus (red-team F6): objective fork-choice is now wired into the node,
  with integration + unit coverage** (2026-08-04) — the F6 objective-weight
  mechanism (on-chain `BondRegs`) previously existed only in `core/chain` behind a
  verifier a caller had to supply. A node now wires it in one call:
  `Node.EnableObjectiveChain` injects the real space-time bond verifier
  (`bond.VerifySpaceTime`, the same check the audit loop runs), and
  `Node.RegisterBondReg` mints a signed registration from the node's held bond for
  live entry into the objective set (`chain.NewBondReg` / exported
  `chain.BondRegNonce`; `EnableBond` now records the identity signer so a bonded
  node can register before it joins consensus). Coverage now spans all three
  tiers per the build-immutable rule: **unit** — a live registration round-trips
  through the real verifier and a tampered space-time proof is rejected
  (`core/node/objectivechain_test.go`); **integration** — the red-team's
  non-healing-partition scenario inverted, with a **separate empty ledger per
  node** (so the local reputation view is useless, unlike `sim/reorg_test.go`'s
  shared ledger): the partition still commits and heals to the heavier-bond fork
  on every replica (`sim/objective_consensus_test.go`). **Residual:** turning
  objective mode on by default in the daemon (a genesis/anchor-seeded validator
  cold-start plus a live-registration submission path), and an e2e multi-process
  run, remain; see `docs/design/m0-consensus.md`.
- **Privacy (red-team F4 §2c): a canonical, on-chain issuer set so the validator
  subset a publisher asks leaks nothing** (2026-08-04) — a publisher previously
  acquired publish tokens from whatever validator subset its `-peers` gave it, so
  a colluding issuer minority could narrow the anonymity set by *which* validators
  a given publish asked. `Chain.CanonicalIssuers` (and `Node.CanonicalIssuers`)
  now derives a **deterministic** issuer set from the **on-chain bond** (the same
  objective `bonded` map that heals fork-choice, F6): bonded validators ordered by
  size then NodeID, identical on every replica. Every publisher asks the same
  validators, so the subset choice carries no signal. Regression
  (`core/chain/redteam_consensus_test.go`) proves two maximally-divergent replicas
  produce the identical ordered set. This is one of the three network-layer parts
  of D3; the transport parts (routing issuance over the content-blind relay from
  an ephemeral identity, epoch batching) are still pending, so IP+timing
  correlation remains until they land. See `docs/design/m0-privacy-issuance.md`.
- **Privacy corner (red-team F4): the per-publish fee no longer links a publish
  to its standing key** (2026-08-04) — token issuance de-anonymized the publisher
  two independent ways: over a non-anonymous transport (IP+timing) and via
  `ChargePublish(from)`, a **per-request debit of the durable standing account**.
  This lands the fee decoupling — **prepaid publish credits** (online Chaumian
  e-cash, Chaum 1982). A credit is a blind signature under the issuer's key but in
  a **separate FDH domain** (`blindtoken.BlindCredit`/`VerifyCredit`), so a credit
  can never be presented as a publish token or vice versa even under one key. The
  fee is charged **in bulk at mint** (a normal, charged token request blinded in
  the credit domain); at publish the requester **spends a credit** (`Message.Credit`,
  verified and marked spent in an online double-spend set) and the issuer does
  **not** charge the durable identity — severing the ledger-level link. The change
  is purely additive: a request with a credit spends it (no debit), a request with
  none takes the legacy charged path, so existing token flows are unchanged
  (whole suite + vet + `-race` green). New helpers `Node.AcquireCredits` (bulk
  mint) and `Node.AcquireTokenWithCredits` (spend). Regressions in
  `core/node/redteam_privacy_test.go` show a mint charging once, a publish
  charging nothing more, a spent credit refused (double-spend), a forged credit
  refused, and the credit/token domains proven non-interchangeable. **Residual
  (honest):** the **network-layer link** — routing issuance over the content-blind
  relay from an ephemeral identity, epoch batching, and a canonical validator set
  — is **not yet built**, so a colluding issuer minority can still correlate by
  IP+timing; the privacy corner does not fully hold until that lands. See
  `docs/design/m0-privacy-issuance.md`.
- **Consensus corner (red-team F6): fork-choice is now objective — honest
  replicas stop diverging** (2026-08-04) — fork-choice weight, the quorum count,
  and proposer/attester eligibility used the **local reputation view**
  (`c.rep(id)`), so two honest validators that had audited different peers
  computed different weights and forked permanently (the partition never healed).
  Fork-choice is now driven by **on-chain PoST-bond registrations**
  (`Block.BondRegs`): a validator records its bonded size with a fresh
  space-time proof any replica re-verifies (`SetBondVerifier`), bound to the
  block's parent so it can't be replayed to another height/fork and signed so it
  can't be claimed by a non-holder. Weight becomes the summed on-chain bond of a
  block's distinct attesters — a quantity **every replica recomputes identically
  from the chain** — so divergent local views can no longer disagree on which
  fork is heavier, and a lighter fork reorgs onto the heavier one on every honest
  node. The mechanism is **additive and opt-in** (`Config.MinBond > 0`): the field
  is `omitempty` so a block with no registrations hashes exactly as before (no
  `BlockVersion` bump), and the default path is unchanged — the legacy
  reputation-gated behavior and every existing test/sim are untouched. Regressions
  in `core/chain/redteam_consensus_test.go` show two maximally-divergent replicas
  computing the same weight, a partition healing to the heavier-bond fork, and a
  forged registration (bad proof or bad signature) denied. **Residual (honest):**
  the objective-mode wiring in the node/daemon (validators emitting registrations,
  a genesis-seeded validator cold-start, enabling `MinBond` in production) is a
  follow-up, and **F7** — cross-height double-backing evading the same-height-only
  equivocation slash — is not yet fixed (it needs Casper-FFG-style surround-vote
  slashing that spares honest reorg-followers). See `docs/design/m0-consensus.md`.
- **Sybil corner (red-team F1/F2/F3): the PoST bond now binds the bytes it
  charges for, and the VDF is bound to a plot read** (2026-08-04) — the external
  M0 red-team broke the Sybil corner three ways; the first two are now fixed at
  the mechanism level (`core/bond`), per `docs/design/m0-sybil-bond.md`.
  **(F1)** `plotBlock` derived each 4 KiB block from only the 32-byte *leaves* of
  its predecessor and parents, so a prover could store just the leaves (1/128 of
  the bond) and recompute any probed block on demand. Each block now depends on
  the **full bytes** of its predecessor and its parents, selected over a **proven
  depth-robust graph** (DRSample, Alwen–Blocki–Harsha CCS'17) instead of the old
  flat-uniform parents — so reconstructing a block requires the parents' bytes
  recursively and the pebbling cost is Ω(n); the rational strategy is to store
  the S bytes, and the charged size equals the resident footprint. `Verify` never
  recomputes a block, so it stays O(log n). **(F2)** `AnswerSpaceTime` seeded the
  VDF from the *public* `challengeSeed(root, nonce)`, so a zero-resident prover
  ran the VDF and then re-derived the sampled blocks — releasing the space
  forfeited nothing. The VDF is now seeded from a plot block **read before the
  VDF** (`seedIndex` → `challengeSeedST`): the answer carries that block plus its
  inclusion proof, the verifier recomputes the seed index and checks the proof,
  so a prover that released the space cannot produce the seed without the Ω(n)
  recompute. **(F3)** root-owner dedup is documented as only a same-root tiebreak;
  Sybil cost now lives in the byte-bound proof, and distinct identities still
  produce distinct plots. The plot on-disk format (`adapters/diskplot`) bumps to
  **version 2** so a restart re-plots rather than reloading the old, insecure
  labeling (one-time re-plot on upgrade). The red-team PoCs are adopted inverted
  as regressions (`core/bond/redteam_sybil_test.go`), and `BenchmarkSeal` records
  the plot/re-plot constant (~270 MB/s) behind the "re-plot ≫ epoch" tuning.
  **Residual (honest):** the *structural* anti-release binding is in; the
  *quantitative* floor — a minimum bond size and `BondVDFDelay` such that even the
  smallest allowed bond cannot re-plot within one challenge window — is a
  deployment-tuning follow-up, and consensus fork-choice weight (F6) still depends
  on this bond being real. See the design doc's open-risks section.
- **Accountability corner (red-team F5): on-chain revocation is no longer a
  global switch** (2026-08-04) — the external M0 red-team broke the
  accountability tenet three ways through the chain's takedown path: a quorum
  could revoke a root it never published (no ownership or existence check); the
  takedown was honored by **every** chain-follower with no opt-out — a global
  switch the tenets say cannot exist; and it was irreversible. All three are
  fixed. **(1)** `ValidateProposal` and the commit path now reject a block whose
  `Revocations` name a root never committed on this chain (`ErrRevokeUnknownRoot`)
  — a quorum cannot censor content that isn't on the ledger, nor a competitor's
  unpublished hash. **(2)** Honoring on-chain revocations is now a **per-operator
  subscription** — `ReplicaRegistry.HonorRevocations` and
  `node.SetHonorChainRevocations`, both default **off** — so following the chain
  never silently imposes someone else's takedowns; the effect is "proportional to
  who trusts you" (TENETS §9), the same voluntary stance as the operator-local
  denylist, never a universal switch. **(3)** Added an **un-revoke** record
  (`Block.Unrevocations`, quorum-gated and committed in the block hash) so a
  takedown is reversible by the same governance that imposed it, not a permanent
  asymmetry. The red-teamer's own PoC now fails at its ownership check; adopted
  inverted as `core/chain/redteam_f5_accountability_test.go` and
  `core/node/redteam_f5_subscription_test.go` (unit + node-integration; the
  operator-local takedown sim remains the e2e). Traces to immutable #5, Don't #2,
  S4. **The other red-team breaks (Sybil bond F1/F2, privacy issuance F4,
  subjective fork-choice F6, cross-height equivocation F7) remain open — see the
  M0 status note below — this fix closes the accountability corner only.**
- **Doc-truth reconciliation + a token round-trip playbook (acceptance round 3)**
  (2026-08-03) — the third acceptance re-run PASSED again (all 9 flows, all 8
  tenets, **zero code defects**); its findings were stale docs and one
  discoverability gap, several making the product look *worse* than it is. **(F1)**
  `risk-register.md` row 14 claimed a default publish still writes a permanent
  `Publisher→root` map — but the chain default now REJECTS Publisher entries
  (`-allow-publisher=false`), so a default publish records no author; updated to
  CLOSED-by-default, with blind tokens as the additional opt-in for full
  unlinkability. **(F2)** `threat-catalog.md` F1 still said "the RSA issuer key is
  in-RAM (persistence is a follow-up)"; it persists now (#126, `adapters/diskissuer`)
  — corrected. **(F3)** the website said publishing is "cryptographically
  unlinkable" as an unqualified property; qualified to "names no author by default —
  with opt-in blind tokens, cryptographically unlinkable," matching the honest
  in-repo docs. **(F4)** the headline walkthrough (`local-test-network.md`) never
  reached the trust-plane flows 4–7; added a "Tier 4 — become a validator" section
  pointing at `examples/` and `user-seam.md` §Role 4. **(F5, doc-note only per
  decision)** documented that a denied root reads to a fetcher as ordinary
  data-loss (compliant nodes answer "not found" rather than advertising a refusal —
  deliberate; the fetcher retrieves from another operator). **(F6)** the F7
  sub-claim "the tokens it issued stay valid across a restart" had no operator-level
  repro; added **`examples/flow-tokens-issuer-restart.sh`** — validators require
  blind tokens, a tokened publish commits (no Publisher), the issuer is restarted
  (its `issuer.key` reloads byte-identical, no re-mint), and a token issued by the
  restarted issuer still commits (peers accept it), with a token-less-publish-refused
  negative control. Also made `silt chain-status`'s hint line un-ambiguous to grep.
  No mechanism changed. Traces to **S5**.
- **Docs & UX polish (acceptance re-run new-F3/F4/F5/F6)** (2026-08-03) — four
  minor/cosmetic gaps the passing re-run surfaced, each a small correctness or
  clarity fix, no mechanism change. **(F3)** the Tier-1 "erasure by hand"
  walkthrough listed objects as flat under `.silt/objects/` and told you to
  `rm .silt/objects/<a-few>` — but objects nest one level under a 2-hex prefix
  (`.silt/objects/<xx>/<hash>`), so that command targets a whole prefix
  directory, and it could delete the single-copy manifest chunk and brick `get`;
  rewritten to use `silt info … -shards` to pick real data/parity shard hashes,
  delete them by their true path, and warn the manifest is single-copy on one
  node (`README.md`, `docs/local-test-network.md`). **(F4)** `silt daemon -h`
  described `-registry` as `http://host:port` — the exact form the key-pinning
  contract *refuses*; the flag help now reads `ID@https://host:port (key-pinned —
  copy the daemon's 'registry:' line verbatim)`. **(F5)** the website's feature
  list didn't mention NAT traversal (thoroughly documented in the repo but
  invisible to a site visitor); added a "Reaches across NATs" card. **(F6)**
  `silt get <siltcare:…>` refused with `link: not a silt:v1: link`, which reads
  like a typo rather than an intentional capability boundary; `link.Parse` now
  recognises a care link and says so, and `silt get` points to `silt info` /
  `silt daemon -care` and the full link (unit test pins the clearer error).
  Traces to **S5**. See the M0 acceptance re-run report.
- **Gate 4 (#52, acceptance F1): a restarted validator rejoins the chain instead
  of being stranded at its pre-restart height** (2026-08-03, D2) — the M0
  acceptance field test found the one blocker: kill a validator, let the network
  commit a block without it, restart it on the same `-store`, and it never caught
  up — it sat at its old height forever while the live set advanced, so over time
  the validator set could only shrink. Two compounding causes, both rooted in the
  same mistake — treating *reputation* (a live, local, NON-persisted view, re-earned
  by bond audits) as if it were a property of a *persisted* block. **(1) Reloading
  our own chain** re-ran every block — including the genesis — through the full
  commit gate (`chainstore.Replay` called `chain.Append`), so at boot, before any
  bond audit had run, the empty reputation view failed the very first block:
  `reputation below threshold: proposer <genesis-id> has 0, needs 100`. The genesis
  is designed to *bypass* that gate (`AppendGenesis`); replaying it through the gate
  cannot work. **(2) Catching up on missed blocks** fired `SyncChain` exactly once,
  at boot, gated on `-attesters`, and BEFORE `StartBondAudit` — so it ran against an
  empty reputation view (adopting nothing, since it can't yet tell which fork carries
  real standing) and then never retried. The in-process `consensus` sim hid both
  because it PRE-POPULATES reputation before the latecomer syncs. **The fix draws the
  trust boundary at whose disk it is.** Our OWN committed history is reloaded by
  `Chain.Reload`, which re-verifies each block's cryptographic integrity — hash
  ancestry, the proposer signature, and a quorum of distinct verifying non-proposer
  attester signatures (so bit-rot, truncation, or tampering is still caught, B7) —
  but NOT the time-varying reputation gate, which a validator already satisfied when
  it committed the block live; genesis reloads via `AppendGenesis` as it always
  should have. A PEER's fork is a different trust class and still goes through
  `Reconcile` with full reputation re-validation. Catch-up is now a periodic,
  retrying `StartChainSync` loop (`ChainSyncInterval`, default 30s), UNGATED on
  `-attesters` (it targets the explicit set plus every validator learned from a
  gossiped bond, so a node restarted with only `-bootstrap` still rejoins), and the
  daemon runs it AFTER `StartBondAudit` so peer standing is being re-earned — a later
  sweep, once audits land, adopts the missed blocks and persists them. Tested (V5):
  unit — replaying our own `[genesis, block1]` with an EMPTY ledger now rejoins at
  height, while a tampered block is still rejected (`ErrBadSignature`); node — a
  restarted validator adopts NOTHING while its standing view is empty and catches up
  the instant bond audits restore peer standing, and `syncTargets` includes a
  bond-learned validator with no `-attesters` given. Honestly labelled: fork-choice
  weight is still the locally-qualified reputation view (fully-objective,
  partition-independent on-chain PoST-bond weight remains the recorded D2 hardening),
  and a bespoke multi-daemon restart harness is deferred to the acceptance re-run —
  the field test roadmap #52 exists to prove. Traces to **M0**, **B7**, **D2**,
  **#52**. See `docs/design/gate4-m0-mechanism.md` §3e.
- **Gate 4 (acceptance F2/F7): the trust plane narrates itself — an operator can
  SEE standing, bond reload, and caretaker sweeps** (2026-08-03, S5) — the M0
  mechanisms worked but ran silent, so the acceptance operator had to read source
  to confirm the earned-standing and self-heal claims. Four honest-observability
  fixes, all at `-log info`: **(standing)** a validator now narrates its own
  consensus standing every bond-audit sweep and the verdict of every peer bond
  challenge (`standing`, `bond challenge`), so the earned-standing mechanism the
  whole of M0 rests on is visible rising and decaying rather than inferred from a
  diffed `chain.cbor`; **(bond reload)** a restart that RELOADS its plot now says
  `reloaded the … bond (no re-plot)` instead of the identical `sealed …` wording a
  first-time plot uses — the "no re-plot" guarantee held, but the log had actively
  suggested the expensive path ran (`EnableBond` now reports reloaded-vs-sealed);
  **(caretaker)** the repair sweep logs `stripe degraded, within repair slack —
  watching` when it sees a loss that parity/replication still covers, so an
  operator who kills a holder sees the caretaker NOTICE rather than apparent
  silence — repair fires (`stripe repaired`) only once losses exceed the slack,
  which with the default replication takes more than "a couple" of deaths, and
  `repair below k` already marks the can't-yet-reconstruct case; **(default on)** a
  validator with no `-log` flag now defaults to `-log info` — the M0 stakes mean
  the normal path should narrate itself in the field, not stay dark until someone
  knows to ask (non-validators are unchanged: logging stays off). The flagship
  self-heal walkthrough (`docs/local-test-network.md`) is rewritten to set honest
  expectations (why killing "a couple" of holders correctly heals nothing visible,
  how to actually strand a stripe, and `silt sim run churn` for the dense version).
  A read-only `Reputation` accessor was added to the `CreditLedger` port for the
  narration. No mechanism changed — this is pure observability. Traces to **M0**,
  **S5** (honest observability), **B5**. See the M0 acceptance report.
- **Docs (acceptance F4/F5/F6/F8): the getting-started guides match reality**
  (2026-08-03) — the acceptance operator hit four first-five-minutes doc snags,
  none breaking the product but each eroding "every step works / every counter
  reproduces": **(F4)** three guides (`README.md`, `docs/local-test-network.md`,
  `docs/v1-test.md`) said `add` "prints the root hash" / a "64-char hex string"
  then told you to `get <root>` — but `add` prints a full `silt:` **link** and
  `get`/`info`/`swarm get` need that whole link, so a literal newcomer hit an
  error; every such placeholder is now `<silt-link>` with the output described as
  a link (the top-level `silt` usage block was already correct). **(F5)** the
  quoted `sim run economy -seed 21` figures were stale — refreshed to the actual
  deterministic output (Gini 0.00 → 0.63, top earner ~1.25 MB, freeloader ~444 KB,
  20/36 second-round publishes ok). **(F6)** the `silt sim run` usage error listed
  only `scatter` and the top-level usage omitted half the scenarios — both now list
  all eight (`scatter, churn, economy, audit, capacity, consensus, bondstanding,
  takedown`), including the previously undocumented `bondstanding`. **(F8)** the
  `user-seam.md` store-layout table listed `chain/` (a directory); the committed
  history is a single `chain.cbor` file. Traces to **S5** (honest observability
  extends to the docs). See the M0 acceptance report.

### Added
- **Validator onboarding (acceptance re-run new-F1/new-F2): `silt id`, `silt
  chain-status`, and a runnable `examples/` playbook** (2026-08-03) — the M0
  acceptance re-run PASSED (all 9 flows, all 8 tenets, zero `broken`), leaving
  two "major" gaps that both blocked a literal newcomer from the validator flow
  without changing any mechanism. **(new-F1)** Role-4 setup was chicken-and-egg:
  `-attesters <ID_B>` needs B's NodeID, but nothing told you how to learn it
  before launch (the acceptance script resorted to booting a throwaway daemon to
  read its `peer:` line). New `silt id [-id-seed N | -store DIR] [-listen ADDR]`
  prints the NodeID a daemon *would* use without launching one — resolving the
  identity exactly as the daemon does — so the topology is wireable up front.
  **(new-F2)** there was no operator playbook for the multi-validator flows 5–7
  and no way to confirm convergence except hashing `chain.cbor` by hand. New
  read-only `silt chain-status [-store DIR]` prints a replica's head height, head
  hash, and block/entry counts — identical head height AND hash across replicas
  proves they agree; a rising head after a restart proves catch-up. And a new
  top-level **`examples/`** directory ships four bash playbooks
  (`flow2-publish-fetch`, `flow4-earned-standing`,
  `flows567-convergence-fault-restart`, `flow8-takedown`) — the flows-5–7 script
  IS the field test roadmap #52 owes itself, now runnable in one command. The
  playbooks track only the PIDs they start (no blanket `pkill`) and use both new
  commands. `docs/user-seam.md` Role 4 gains a concrete `silt id`-based recipe
  and points at `examples/`. All four playbooks pass end to end locally
  (including the restarted-validator chain catch-up on real daemons — the
  daemon-level confirmation of the F1 restart fix). Traces to **S5** (an operator
  can see and reproduce what's true), **#52**. Adopted from the M0 acceptance
  reproduction scripts.
- **Gate 4d (#93): the publish-token issuer key persists across restarts**
  (2026-08-03) — a validator that issues blind-signed publish tokens generated a
  FRESH RSA key on every daemon start, which orphaned every token it had already
  FRESH RSA key on every daemon start, which orphaned every token it had already
  signed (they no longer verify) and staled every issuer public key its peers had
  cached. A new `adapters/diskissuer` persists the key (PKCS#1 DER, written
  atomically with `0600`), and the daemon **loads-or-creates** it: first run mints
  the issuer identity, every restart keeps it — so outstanding tokens stay
  verifiable and the distributed issuer set is stable. A corrupt or foreign key
  file is a hard error, never silently overwritten with a new identity. Tested
  (V5): the restart property is pinned (two `LoadOrCreate`s over the same dir
  return the same key), plus save/load round-trip, clean-absent, and
  corrupt-file handling; the real daemon (e2e + Docker NAT) starts and persists
  the key. Honestly labelled: this is the issuer-key half of §3d's "issuer
  survives restart"; **on-chain issuer registration** (so the qualified issuer
  set is chain-verifiable rather than fetched ad-hoc) is the remaining §3d piece,
  and it pairs with the deferred D3 canonical-validator-set work. Traces to
  **M0** (the unlinkable-publish path stays live across restarts), **B7**. See
  `docs/design/gate4-m0-mechanism.md` §3d.
- **Gate 4f (#100): equivocation is provable and slashable — double-signing
  costs standing** (2026-08-03, D2) — the consensus analogue of a storage liar:
  a validator that signs two DIFFERENT blocks at the SAME height (trying to make
  two competing histories both look supported) is now caught and penalised. Two
  parts: **(prevention)** an honest validator records the block hash it signed at
  each height and REFUSES to sign a different block there — it never equivocates,
  even if two competing proposals reach it before either commits; **(penalty)** a
  `chain.Equivocation` is a compact, self-verifying proof (the two conflicting
  blocks; any node recomputes their hashes, confirms same height + different
  block, and that the culprit's signature — as proposer OR attester — verifies in
  both), and `chain.FindEquivocations` extracts every cross-fork double-signer
  from two competing histories. When a node reconciles across a fork it slashes
  each proven equivocator in its local ledger (`credit.SlashEquivocation`), a
  crushing, permanent reputation penalty that buries the culprit below any
  threshold — so its proposals are refused and its attestations stop counting
  toward any fork's weight. An honest validator signing sequential heights is
  never implicated (the heights differ) and a forged accusation fails (the
  signatures won't verify). Tested (V5): unit — a double-sign is provable, a
  sequential signer and an unsigned accusation are not, the same block is not a
  conflict, and every cross-fork culprit is found while one-fork signers are
  spared; node — a validator REFUSES a second block at a height it attested, and
  reconciling across a fork slashes the double-signer below zero. Honestly
  labelled: strict lock-on-attest can stall a height's liveness if a proposal
  fails and its attesters are needed again there — proper resolution is
  round-based unlocking (Tendermint POLC), a recorded 4f hardening; on-chain
  equivocation records so every replica slashes in lockstep (vs. each acting on
  what it observes) is the other recorded follow-up. Traces to **M0** (a
  double-signing proposer cannot stand two histories AND keep its standing),
  **D2**. See `docs/design/gate4-m0-mechanism.md` §3e.
- **Gate 4f (#100): the chain can reconcile forks — reorg to the heavier
  history** (2026-08-03, D2) — the registry chain was append-only with no
  reorganisation ("first valid block at a height wins"), and `SyncChain`
  silently `break`ed on divergence, so a partitioned or diverged validator
  stayed forked forever. It now heals: `Chain.Reconcile` re-validates a peer's
  full chain end to end in a throwaway replica and, iff that history is strictly
  heavier (ties broken by the lower head hash, so every honest node picks the
  same winner), **adopts it** — rolling state back to the shared genesis and
  forward onto the heavier fork. Because all derived state (`byRoot`, `spent`,
  `revoked`, `validatorsSeen`) is a pure function of the blocks, the reorg is a
  whole-state swap, not fragile per-record undo. Fork-choice weight is the
  cumulative count of DISTINCT qualified non-proposer attestations across the
  chain — the heaviest history is the one the most *earned standing* has
  committed to, not merely the longest (which a fast Sybil could extend);
  signatures are objective, the qualification bar is the local reputation view
  (which converges among honest replicas). The fork is genesis-anchored, so a
  peer cannot swap in a heavier FOREIGN chain, and every block is re-validated,
  so a lying peer wastes time but cannot feed an invalid history. `SyncChain`
  now reconciles against each peer's full chain — one uniform path for catch-up,
  fork-heal, and no-op (an equal-length fork is invisible to "give me blocks
  above my head", which is why it compares whole chains). Tested (V5): unit —
  a heavier fork is adopted, a lighter one rejected, ties break deterministically
  by hash, a foreign genesis is refused, an under-quorum fork is re-validated and
  rejected; integration — a 10-node network **partitions, each side commits its
  own history, then heals and the lighter side reorgs onto the heavier fork over
  the wire while the heavier side does not budge**. Honestly labelled:
  fully-objective, partition-independent on-chain PoST-bond weight is the
  recorded D2 hardening (a self-asserted or locally-qualified weight can diverge
  under an adversarial partition); equivocation evidence + slashing is the next
  4f increment; genesis-to-head diffs (vs. whole-chain fetch) are the scaling
  follow-up. Traces to **M0** (consensus can't be captured by an off-head or
  partitioning proposer), **D2**. See `docs/design/gate4-m0-mechanism.md` §3e.

### Changed
- **Gate 4b (#91): bind the bond plot to its identity — close the
  plot-amortisation gap** (2026-08-03) — the Sybil cost only holds if each
  identity holds its OWN distinct plot; previously nothing stopped a single
  operator from pointing N node identities at ONE shared plot (all advertising
  the same root, answering from one copy on disk), collapsing the per-identity
  cost from S to S/N. Two changes close it, together: **(C)** the plot is now
  sealed from a per-identity **secret** derived from the node's signing key
  (`EnableBond` takes the signer; `bond.Seal` takes the secret) rather than the
  public NodeID — so only an identity's owner can generate its plot, and an
  outsider cannot precompute a *victim's* root to grief it; and **(A)** the
  ledger binds each bond root to the first identity that proves it
  (`RecordBondChallenge` gains a `root`; a per-root owner map), so a root builds
  standing for **at most one identity** — N identities sharing one plot earn one
  bond's worth of standing, not N, forcing N distinct plots = N×disk. Honest
  identities never collide (distinct secret ⇒ distinct root), so the dedup only
  ever bites deliberate sharing. This upgrades design §6's open amortisation
  question from "hand it to the red-team" to a built defence — noting it is
  still not a proof of *correct* plotting (no PoRep/SNARK); the secret + dedup
  make sharing a root un-grief-able and uneconomical rather than impossible.
  Tested (V5): the M0 outcome is pinned — three identities proving one shared
  root leave only the first with standing while a distinct plot earns normally
  (failing-first: without dedup all three would clear the bar); distinct
  secrets yield distinct roots; and the over-the-wire bond audit + restart
  reload paths stay green under the new derivation. Traces to **M0** (the Sybil
  corner), **D1**. See `docs/design/gate4-m0-mechanism.md` §3b/§6.
- **Gate 4b (#91): the bond is now proof-of-space-TIME — the VDF is wired into
  the live bond audit** (2026-08-02) — completes the mechanism: standing is
  backed not just by held space (the plot) but by space held *across time*. A
  bond challenge now answers with a `core/vdf` proof over the fresh
  `(root ‖ nonce)` challenge, and the probed plot-block indices are derived from
  the *VDF output* — so a prover cannot know which blocks to keep ready until it
  has done `BondVDFDelay` sequential squarings, and therefore cannot release the
  pledged space and re-plot just-in-time, nor parallelise its way out of the
  elapsed-time floor. Verification stays O(log n) (checking a VDF is fast even
  though producing it was slow) plus the existing Merkle checks, so consensus
  cost on the core loop is unchanged. `core/bond` gains `AnswerSpaceTime` /
  `VerifySpaceTime` (additive — the space-only `Answer`/`Verify` remain), the
  answer carries the VDF proof inside the existing CBOR `Answer` (so no wire
  format change), and `core/vdf` gains `Default()` — the RSA-2048 challenge
  modulus, an unknown-order group needing no fresh trusted setup (a documented
  launch anchor; class groups are the setup-free upgrade). `BondVDFDelay` is a
  new node-config tuning knob (Evolving): a modest default keeps the
  deterministic sim fast, a real deployment raises it for a stronger time floor;
  `0` disables the time binding. The daemon inherits it from `DefaultConfig`
  (the #65 dropped-field discipline), and the `bondstanding` sim now exercises
  the whole space-time path over the wire. Tested (V5): held bonds answer, a
  space-only answer / wrong-delay / forged-VDF-output all fail, and the probed
  blocks provably derive from the work not the raw nonce. Honestly labelled:
  producing the VDF currently runs on the audit path; moving the heavy work
  fully off the core loop and persisting the plot across restarts (B2 / #93) is
  the next 4b step. Traces to **M0** (Sybil corner: space held across time),
  **D1**, **B2**. See `docs/design/gate4-m0-mechanism.md` §3b.
- **Gate 4b (#91): the bond is now a real space-hard plot, not independent
  blocks** (2026-08-02) — replaces the honestly-labelled placeholder in
  `core/bond` (each block was cheap iterated SHA-256 over `id‖index`, so an
  attacker could recompute any block on demand and store nothing) with a
  **sequential labeling plot**: block `i` depends on its identity, index,
  immediate predecessor, and a few pseudo-random *earlier* blocks (a chain plus
  long-range parents — a DAG). Because a block depends on earlier ones,
  recomputing a single probed block forces recomputing its whole dependency
  subgraph, and the long-range parents defeat cheap checkpointing — so the
  rational strategy becomes to **store the S bytes**, which is exactly the space
  being charged for. This makes N Sybil identities cost N distinct blobs of real
  disk, the property the reputation→quorum path always assumed but never charged.
  The challenge/answer/verify seam is untouched — `bond.Verify(root, size,
  nonce, Answer)` stays a stateless O(log n) Merkle check — so only *what fills
  the blocks* changed. Honestly labelled: space-hardness is heuristic (not yet a
  formally depth-robust graph or a memory-hard label function — the hardening
  path), and the *time* half (binding a fresh epoch challenge to the `core/vdf`
  delay so the space must be held across time and the challenge can't be
  precomputed) is the next 4b step. Tested (V5): determinism + identity-binding,
  the dependency lever (perturbing a predecessor or long-range parent changes
  the block — the space-hardness property the old independent blocks lacked),
  and parent indices are always earlier + deterministic. Traces to **M0**
  (Sybil corner), **D1**. See `docs/design/gate4-m0-mechanism.md` §3b.

### Added
- **Gate 4b (#93): the bond plot persists — a restart reloads it, never
  re-plots** (2026-08-03) — plotting the identity bond is deliberately expensive
  (that expense is the Sybil cost), so paying it again on every daemon restart
  would be wasteful and, for a large pledge, a long stall before a validator can
  prove standing. A new `adapters/diskplot` store persists the plot (one atomic
  file per identity: a small header with the block geometry and committed root,
  then the raw blocks), and `EnableBond` now **loads-or-plots**: if a persisted
  plot exists it is reloaded and its Merkle root **re-derived from the bytes and
  checked against the committed root** (B7 — persisted state is re-verified, not
  trusted), so a restart skips plotting entirely; a corrupt, truncated, or stale
  plot is detected and cleanly re-plotted. `core/bond` gains `Reconstruct` (rebuild
  a commitment from persisted blocks) and `Blocks()`; a new `ports.PlotStore`
  seam keeps the node pure (nil = memory-only, fine for sims). The daemon wires
  it alongside the proof store (inheriting the #69/#93 restart discipline).
  Tested (V5): the adapter round-trips and flags truncated/foreign files; a
  reloaded bond answers a space-time challenge; and the node-level restart
  outcome is pinned — a second start with the same identity **reloads instead of
  re-plotting** (asserted via plot/reload counters), while a corrupted plot
  re-plots to the correct identity-bound root. Traces to **M0**, **D1**, **B7**.
  See `docs/design/gate4-m0-mechanism.md` §3b/§3d.
- **Gate 4b (#91): verifiable delay function primitive (`core/vdf`)** (2026-08-02)
  — the sequential-work core of the proof-of-space-*time* bond, and the first
  4b construction piece. A VDF evaluates in a prescribed number of *inherently
  sequential* steps (you cannot parallelise your way to the answer) yet emits a
  short proof anyone verifies almost instantly — exactly what a bond needs to
  bind a fresh epoch challenge to real elapsed, non-parallelisable time, so a
  Sybil can neither retroactively fake having held its pledged space across the
  epoch nor buy its way out of the wall clock with more cores. The construction
  is Wesolowski's VDF (EUROCRYPT 2019), adopted not invented (B8): over a group
  of unknown order (`Z_N^*` for an RSA modulus `N`), `y = x^(2^T) mod N` by `T`
  sequential squarings, with `π = x^(⌊2^T/ℓ⌋)` for a Fiat–Shamir prime `ℓ`
  computed in `T` steps via long division (never materialising the `T`-bit
  exponent), and verify `π^ℓ·x^r ≟ y` for `r = 2^T mod ℓ` in O(log ℓ + log T) —
  cheap enough to stay on the core loop. Security rests on `N`'s factorisation
  being unknown (a documented trust anchor; the class-group variant removes it
  and is the noted upgrade path). Pure package (big integers and bytes only).
  Adversarially tested: relabelling a shorter computation as a longer one, a
  trivial `π=1`, tampered `y`/`π`, wrong-challenge, wrong-`T`, and non-canonical
  elements all fail; the delay loop is pinned against a direct `x^(2^T)`
  reference. Wiring the plot + epoch proof off-loop behind the existing
  `bond.Verify` seam is the next 4b change. Traces to **M0** (the Sybil corner:
  space-time held, not asserted), **D1**, and **B2** (the heavy work runs off
  the core loop). See `docs/design/gate4-m0-mechanism.md` §3b.
- **Gate 4a (#90): wire the real proof-of-retrieval into the live audit path**
  (2026-08-02) — the `core/por` primitive now *replaces* the toy scheme in the
  running node. An auditor verifies that a peer still holds a shard **without
  fetching the bytes**: at distribute time the publisher computes each shard's
  per-block authenticators under a key derived from the file's layout key
  (`node.DerivePorKey`, mirroring the link key hierarchy) and ships them beside
  the Merkle proof (`StorageProof.PorTags`); the storage node keeps them with
  the chunk; on challenge the prover aggregates its bytes + tags into a compact
  `(μ, σ)` response; the auditor derives the *same* key from its care-link and
  checks the response touching no data. `gradeAnswers` **loses its ground-truth
  fetch entirely** — a `liar` node that kept its tags but dropped the bytes now
  fails an audit that never fetches, and is slashed via `credit.RecordAudit`.
  The auditor recomputes each full shard's expected block count from the layout
  `ChunkSize` and rejects any prover under-reporting it (soundness against
  partial deletion for every full shard; the single short tail shard is the one
  documented residue for the V3 red-team). The key never crosses the wire and a
  storage node — lacking the layout key — cannot forge. Two hand-rolled codecs
  were extended so the tags don't vanish in the field (a #65-class trap): the
  TCP wire codec (`adapters/tcpnet`) and the on-disk proof store
  (`adapters/diskproofs`, so a restarted host can still prove what it
  re-announces, #69). Repaired/re-seeded shards are re-tagged from the
  caretaker's care-link. Coverage (V5): unit (deterministic key derivation +
  cross-capability agreement, GCM-overhead guard, wire + persistence
  round-trips), sim (liars slashed with **zero** ground-truth fetches during the
  sweep — proven by a per-kind message counter), and the real-daemon TCP + cross
  -NAT (incl. full-swarm restart) harnesses stay green carrying the enlarged
  proofs. Traces to **M0** (presence proven, not asserted), **B8**, and
  **B7/V3**. See `docs/design/gate4-m0-mechanism.md` §3a.
- **Gate 4a (#90): real proof-of-retrieval primitive (`core/por`)** (2026-08-02)
  — the first Gate-4 construction piece. A verifier holding a small secret key
  can now check that a prover still holds a chunk's bytes *without fetching
  them* — the property the toy scheme (`core/node/por.go`, which grades against
  ground truth it fetches itself) deliberately lacked. The construction is the
  private-verification Compact Proof of Retrievability of Shacham & Waters
  (ASIACRYPT 2008) — a homomorphic linear authenticator over the Curve25519
  field prime: per-block tags `σᵢ = f_k(i) + Σⱼ αⱼ·mᵢⱼ`, a seed-expanded
  challenge, and an O(s) aggregated response `(μ, σ)` whose size is independent
  of the chunk. A prover that deleted or altered any sampled block cannot make
  the verification equation hold without the secret αⱼ, which the tags do not
  reveal. The verify key is designed to ride the care-link, so caretakers audit
  over ciphertext while storage-node provers cannot forge. Pure package (bytes
  and keys only); wiring it into the manifest, node audit loop, and credit
  ledger is the next 4a change. Adversarially tested: tampered/deleted-block,
  key-less forgery, wrong-key, and wrong-unit proofs all fail. Traces to **M0**
  (the Sybil corner: presence proven, not asserted), **B8** (adopt the proven
  primitive), and **B7/V3** (a non-holder fails the challenge). See
  `docs/design/gate4-m0-mechanism.md` §3a.

### Fixed
- **Gates 1–3 completeness audit: closed missing regressions in the floors**
  (2026-08-02) — a pre-Gate-4 audit verified the landed floors (Gate 1),
  register-after-distribute (Gate 2, #65), and NAT traversal (Gate 3, #27/#111)
  are whole at all three test tiers, and fixed the coverage gaps it found. The
  register-after-distribute *failure* outcome had no regression: the one sim
  test touching an unplaceable scatter used the old `Add` (publish-up-front)
  path, so it couldn't catch a dangling entry. The gate is now a single tested
  helper, `pipeline.RegisterAfterDistribute` (publish iff the scatter
  confirmed), that both the `swarm add` and daemon-UI publish paths call
  instead of hand-rolling "publish iff `derr == nil`" — covered by a pipeline
  unit test (both branches) and a sim test that drives the real `node.Distribute`
  failure and asserts the registry is left empty (S5). The relay's per-target
  session cap (`PerPeerSessions`, the #65 knob) gained an isolation test proving
  one target's fan-out can't be throttled by — or monopolise beyond its slot —
  another's; previously only the global `MaxSessions` branch was exercised. The
  default `-dns-seed` is documented as a *deliberate* empty (neutral
  infrastructure, community-run seeds — #27 Part A), not an unfinished hole.
- **Transport frame cap was smaller than the minimum production chunk** (2026-08-02)
  — a whole chunk rides in one length-prefixed frame, but the inbound read
  loop's cap was 32 MiB while the *minimum* production chunk is 64 MiB, so every
  production-sized chunk was dropped on receipt; the swarm could only move
  sim-sized (64 KiB) chunks. The cap is now derived from the manifest chunk-size
  ceiling plus envelope overhead (`maxFrame = manifest.MaxChunkSize +
  frameOverhead`), so the wire can always carry a chunk the manifest layer
  accepts and the two limits can't drift. `Send` now also rejects an over-cap
  frame with an explicit error instead of emitting one the peer silently drops
  (S1/S3). Traces to S1/S3 and anti-persona #14. Closes #104.

### Security
- **Gate 1 (A5): panic-recover + fuzz the decode surface** (2026-08-02) — a
  daemon that crashes on a malformed frame can't be field-tested and can't
  carry the "credible from day one" claim, so every untrusted-input decoder is
  now proven not to panic and is caught if it ever does. New Go fuzz targets
  cover the whole decode surface — the manifest CBOR decoder, the chunk-frame
  length header (plus a Split/Join round-trip), `silt:`/`siltcare:` link
  parsing, chain block/blocks decoders, the tcpnet wire envelope, and the relay
  control frame; their seed corpora run as a smoke test on every push/PR and a
  new nightly workflow mutates each for a real time budget (millions of execs,
  zero panics found). Underneath that proof sits a defence-in-depth recovery
  net (`internal/safe`): the tcpnet read loop and the relay client/server frame
  loops drop the *connection* on any panic, and the node's event loop contains
  a panicking task so one bad frame fails the *request*, not the *process* — an
  event-loop panic is logged at error level (a top-severity bug until fixed),
  never silent. Traces to tenets S1/S3 and anti-persona #14. Closes #87.
- **Gate 1 (A6): bound the declared manifest chunk count + size** (2026-08-02) —
  a manifest arrives as reassembled chunk data and *declares* its own chunk
  count and sizes; a declared number is a claim, not a fact (tenet B7), so a
  tiny manifest that declares a huge chunk array was a cheap memory-exhaustion
  vector (anti-persona #14). The manifest CBOR decoder is now bounded
  (`MaxArrayElements = MaxChunks`) so an over-declared array is refused as its
  header is read — *before* the slice is allocated — across both the plain and
  the sealed (layout/secrets) decode paths. `Validate` and `OpenLayout` add
  semantic checks that reject an oversize declared chunk size or count cleanly,
  per request, with the node still up. Bounds are exported and documented
  (`MaxChunks`, `MaxChunkSize`), sized with headroom over the 64 MiB production
  chunk. Traces to tenets B7 and S1/S3. Closes #88.
### Security
- **Gate 1 (I1): lock the local UI / JSON API** (2026-08-02) — the daemon's
  local HTTP API sent CORS `*`, so any web page the operator visited could
  enumerate or drive their node. It is now locked: every request must carry a
  **localhost `Host`** (a DNS-rebinding page arrives as `evil.com` and is
  refused), any **cross-origin request from a non-localhost page** is rejected
  outright (localhost origins are *reflected*, not blanket-allowed, so the
  observatory still aggregates sibling daemons), and every **state-changing
  call requires a per-daemon bearer token** minted on first run
  (`<store>/ui-token`, 0600) and handed to the operator's browser on the UI URL
  (`/?token=…`). Reads keep their no-token localhost ergonomics. CORS `*` is
  gone. Traces to Don't #3 (access-unsurveilled), B4 (privacy by construction),
  and S4 (no seizable single point). Closes #89.
### Security
- **Chain permanence: version the Block schema before any Gate-4 record change**
  (2026-08-02) — `Block` carried no version, so any future change to *what the
  block hash commits to* or to *validation semantics* (real-bond commitments,
  mandatory tokens) would be a hard fork with nothing to gate the eras:
  `Decode`/`DecodeBlocks` would happily decode an old block and mis-validate it
  under new rules. Blocks now carry a `Version` (era) that `Hash` commits to and
  `Decode`/`DecodeBlocks` require — a version mismatch is an explicit
  `ErrBlockVersion`, never silent mis-validation, and because the hash covers it
  the era can't be swapped under a valid signature. Landed while the chain is
  still throwaway, so it costs nothing now and prevents a flag-day later; it is
  the prerequisite for the Gate-4 record-format changes (#90/#91/#92). Entry
  versioning is deliberately deferred: entries are always validated within a
  block whose version gates their rules, and standalone-registry entry
  semantics are what the tokened-publish design turn (#97) will settle. Closes
  #98.
- **Register-after-distribute: a failed scatter no longer leaves a dangling
  registry entry** (2026-08-02, Gate 2, #65) — `pipeline.Add` published the registry entry
  as its final step, *before* the caller distributed the chunks to peers, so a
  loud placement failure left an entry pointing at content that never landed
  (no link reaches the user, but the registry — and network-size estimates —
  count phantom content; tenet S5). Publishing is now split from staging: a new
  `pipeline.Stage` stores the chunks and sealed manifest and returns the entry
  *without* registering it; the networked publish paths (`swarm add`, web-UI
  publish) register **only after** distribution is confirmed. `Add` still
  stages-and-publishes in one shot for callers that don't distribute separately
  (local `add`, genesis, sim). Fetch-side retry and raised relay session limits
  (the rest of #65) already landed. Closes #65.

### Security
- **Unlinkable publish is now the default; the Gated registry is fenced off** (2026-08-02)
  (M0 privacy, #97/#99) — publishing recorded a permanent `Publisher → root`
  link on the append-only chain because the publish clients attached the node's
  durable identity by default. The chain never *required* it; it was being
  written gratuitously and can never be undone. Now: the `swarm add` and web-UI
  publish paths **attach no Publisher by default** (publish is unlinkable —
  carry a blind-signed token, or nothing), and the chain **refuses a
  Publisher-bearing entry** unless the deployment is explicitly trusted
  (`chain.Config.AllowPublisher`, daemon `-allow-publisher`; `swarm add
  -allow-publisher` to opt a single publish back in). Genesis is exempt (it
  seeds via `AppendGenesis` and its proposer is public by design). Tokens stay
  an orthogonal opt-in (`-token-quorum`/`-require-tokens`) for a *paid*
  unlinkable publish, so earned-standing commit without tokens still works. The
  credit-**Gated** registry — which hard-requires a Publisher and has no token
  path — is documented sim/test-only and **fenced off**: an `internal/depcheck`
  architecture test fails the build if any `cmd/` entry point constructs it (it
  is used only by the sim today). Traces to **M0** (privacy corner), **F1 /
  risk #14**, immutable #3 (no permanent linkage). Closes #97 and #99.
- **Hole-punch now actually fires end-to-end: two NATed daemons upgrade the relay
  path to a direct connection** (2026-08-02, Gate 3, #27/#111) — the Phase-3 wiring existed
  but never worked, and CI never caught it because it only ran the standalone
  probe, never the integrated daemons. Two bugs, both found locally via the
  Docker NAT harness (build-immutable V5): (1) the punch was only *requested* on
  a fresh relay **dial**, but a relay conn is reused for every subsequent frame,
  so a steady-state relay path never tried to go direct — now a reused
  relay-backed conn also (cooldown-gated) requests the punch; (2) the punch was
  requested but never **bound** — the relay control conn was dialed without
  `SO_REUSEPORT`, so the punch dial couldn't re-bind that port to reuse the NAT
  mapping the relay observed, so every attempt failed. The reuseport dial hook
  now lives in a shared `internal/reuseport` package used by both the transport
  and the relay client. Proven locally: cone punches (both daemons log a direct
  connection), symmetric correctly stays on the relay. `integration/nat/
  holepunch.sh` (cone + symmetric) is now wired into the `nat-holepunch` CI job
  so this can never silently regress again. Closes #111.

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
- **Publisher privacy: quorum-issued blind publish tokens** (2026-08-01) (#14 / F1): the
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
- **Launch-window training wheels** (2026-08-01) (#79, risk 15): a young network is the
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
- **Identity costs storage: bond-gated consensus standing** (2026-08-01) (#78): reputation —
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
- **Safe consensus defaults** (2026-08-01) (#79): `silt daemon -validator` now defaults to
  `-quorum 3 -min-rep 100` (was `-quorum 1 -min-rep 0`), so a lone or fresh
  node can no longer rubber-stamp the registry — writing requires earned
  standing and a real quorum. A trusted one-box swarm opts into self-commit
  explicitly (`-quorum 0 -min-rep 0`), which now prints a loud
  trusted-deployment warning rather than being the silent default. Outcome
  proven end-to-end: e2e `TestDefaultsRefuseRubberStampCommit` asserts the
  default refuses a lone commit, with `TestPublishCommitFetchOverTCP` (explicit
  `-quorum 0`) as the positive control.

### Added
- **Deterministic NAT/relay/hole-punch in the sim** (2026-08-01) (#27): the in-process
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
- **Hole-punching: relay paths upgrade to direct connections** (2026-08-01) (#27): when two
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
- **NATed nodes learn their public endpoint, STUN-style** (2026-08-01) (#27, the groundwork
  for hole-punching): when a node registers with a relay, the relay reports the
  `host:port` it observed the registration coming from — the node's NAT mapping.
  A node behind NAT cannot otherwise know its own public address, and
  hole-punching needs it (it's the endpoint a peer aims a simultaneous-open at).
  Surfaced as `relay.Client.Observed()` / `node.ObservedAddr()` and logged by
  the daemon. This is phase 1 of #27; the relay-coordinated punch, port-reuse
  dial, and relay→direct upgrade follow. The `integration/nat` harness asserts
  a NATed node learns its *mapped* public IP (the gateway's), not its LAN
  address.
- **Automated cross-NAT integration harness** (2026-08-01) (`integration/nat/`, and a
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
- **The daemon no longer silently drops config fields** (2026-08-01) (#71): `cmd/silt` built
  `node.Config` field-by-field, so any field added to `DefaultConfig` defaulted
  to its zero value in the real binary — how the #65 fetch-retry shipped inert
  and demand-responsive dispersion was off in the daemon while the roadmap
  listed it as done. The daemon and the ephemeral swarm add/get client now
  start from `node.DefaultConfig()` and override only what genuinely differs
  (the daemon's 2s `RequestTimeout`), so new fields are inherited by default.
- **A restarted daemon's content stays discoverable** (2026-08-01) (#69, found in the #65
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
- **Fetches survive a saturated relay** (2026-08-01) (#65): once the public rendezvous
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
- **Publish no longer returns a link for a file the swarm can't rebuild** (2026-08-01)
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
- **Publish no longer returns a link for content it never stored** (2026-07-30) (#60,
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
- **Ghost routing entries no longer break discovery at scale** (2026-07-30) (found in
  the 300-file scaling test, #43): every `swarm add`/`swarm get` ran as a
  short-lived client with a fresh identity, and nodes both routed to those
  clients and persisted them to `peers.json` — so a busy node's routing
  table filled with dead entries (in the test: 327 entries, 2 live, ~75%
  query timeouts), which broke provider discovery and made most fetches
  fail. Fixed at both ends: nodes persist only peers they have actually
  reached, and a short-lived client stamps its messages so peers never
  route to it.
- **Re-publishing identical content is idempotent** (2026-07-30) (#46): a failed
  publish could leave a root registered but return no link, and a retry
  then hit "root already published with different entry" — because
  idempotency compared the whole entry, including the per-invocation
  publisher identity. It now dedups on content, so a retry (or a second
  person adding the same file) succeeds instead of colliding.
- **NATed peers can actually converse** (2026-07-26) (found in the first real
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
  peers.json** (2026-07-26) — peer strings split on the first `@`, not the last, so
  `ID@relay:RID@host:port` parses instead of being silently dropped.

### Added
- **Opt-in in-RAM read cache for hot chunks** (2026-07-30) (`-cache SIZE`, default off;
  #42): a cache hit serves trusted bytes from memory, skipping both the
  disk read and the per-read hash re-verification. Read-through LRU,
  cache-on-read only, and Delete evicts so purged content is never served.
- **The daemon caretakes content published through its own UI** (2026-07-30) by default
  (`-care-published`, #44): without a caretaker a published file's
  redundancy only decays as nodes churn — now the publishing daemon
  repairs its own roots, and both the UI and CLI say whether a caretaker
  is running.
- **Paginated, shard-sorted roots list in the daemon UI** (2026-07-30) (#45): the
  "identifiers this daemon holds shards of" table now paginates and sorts
  by shards held, instead of rendering every row (unusable at hundreds).
- **A public build log** (2026-07-27) — a chronological "how it was built and why"
  narrative under `docs/buildlog/` (dated Markdown entries), rendered to
  `website/buildlog.html` by `scripts/gen_buildlog.py` on the same
  source-of-truth pipeline as the changelog and roadmap (CI fails if the
  page drifts). It's the *reasoning* behind the design — the forks, the
  dead ends, the decisions — distinct from the changelog (what shipped)
  and the roadmap (what's next), and strictly about building the
  infrastructure. Seeded with three entries: the one-process/ports-and-
  adapters prime directive, the placement spectrum, and cross-network
  reachability. Linked from the site's docs and footer.
- **`-log LEVEL` — narrate the normal path, not just failures** (2026-07-27) — both
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
- **Multi-process end-to-end tests over real TCP** (2026-07-27) (CI hardening,
  BACKLOG Phase 2) — a new `e2e/` suite builds the `silt` binary and
  runs three daemons as separate OS processes, publishes a 1 MiB file
  through the chain-backed registry over pinned HTTPS (driving a real
  consensus round to a committed block), then fetches it back across the
  swarm and asserts it returns bit-perfect. This exercises the whole
  wire path the in-process sim deliberately bypasses — exactly where
  #36's "a reply can never reach a NATed peer" bug hid until real
  sockets carried it. It runs as its own CI job; the unit and race jobs
  pass `-short` to skip the process spawning.
- **Relay discovery by gossip** (2026-07-27) (#27 polish) — a daemon offering `-relay`
  now stamps the service's dialable `host:port` on every outgoing
  envelope (borrowing the `-advertise` host when the relay listener is
  bound to a wildcard). Peers record these first-hand — a node only ever
  announces its *own* relay, and dialing pins the relay's identity, so
  gossip can direct but never impersonate. A daemon whose reachability
  verdict is NATed and that has no `-relay-via` adopts the first
  discovered relay automatically (and keeps watching until one appears):
  the two-Macs runbook now works with nothing but `-bootstrap`.
- **Two-slot address book: direct preferred, relay fallback** (2026-07-27) (#27
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

- **Relay** (2026-07-26) (#27, step 3 — the universal NAT fallback) — a NATed daemon can
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
- **`-debug` flag → `debug.log`** (2026-07-26) on both `silt daemon` and `silt client` —
  a leveled logger behind a new `ports.Logger` interface (core stays pure;
  the file sink is `adapters/logfile`). One grep-able line per event:
  transport failures (dials, handshakes, forged frames), node events
  (request timeouts, repairs, dispersion re-spreads, the reachability
  verdict), and daemon milestones (discovery, bootstrap). Quiet by default
  and free when disabled; with `-debug`, a failure in the field leaves an
  artifact that can be attached to a bug report. Groundwork for testing
  cross-network reachability (#27) on real networks, where failures are
  one-shot and remote instead of deterministic and replayable.
- **Zero-config LAN discovery** (2026-07-26) (#27, first rung of cross-network
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
- **Reachability check** (2026-07-26) (#27, our AutoNAT) — after bootstrap, a daemon asks
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
