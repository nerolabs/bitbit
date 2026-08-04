# Changelog

All notable changes to Silt are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/), and the project aims to
follow [Semantic Versioning](https://semver.org/).

This log is published at [silthq.com/changelog](https://silthq.com/changelog.html).

## [Unreleased]

### Security
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
  registry entry** (Gate 2, #65) — `pipeline.Add` published the registry entry
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
- **Unlinkable publish is now the default; the Gated registry is fenced off**
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
  path to a direct connection** (Gate 3, #27/#111) — the Phase-3 wiring existed
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
