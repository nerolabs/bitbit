# Risk Register

Ranked by exposure (likelihood × impact). Each risk has an owner theme,
a mitigation, and a status. This is a working document — update it as
mitigations ship. See the [fresh-eyes council](fresh-eyes-council.md)
for the reasoning behind each.

Status key: **built** (mitigation shipped) · **planned** (agreed, not
yet done) · **open** (needs a decision).

| # | Risk | L×I | Mitigation | Status |
|---|------|-----|------------|--------|
| 1 | **CSAM / illegal content published to the network.** | High × Severe | Quorum-governed, decryption-free takedown by opaque hash; operator denylists; moderation at the resolver layer. | takedown **built**; denylist distribution **planned** |
| 2 | **Node operators face legal liability** for content they unknowingly host. | Med × Severe | Publisher runs no infrastructure/policy; takedown mechanism; operator terms; entity to shield contributors; clear "not an evasion tool" stance. | mechanism **built**; entity **planned** |
| 3 | **Unaudited crypto/consensus fails at scale** (toy proof-of-retrieval, quorum-not-BFT chain, gameable reputation). | Med × High | Independent audit + threat model before any "production" claim; keep honest labeling; bug-bounty; Sybil/eclipse hardening. | **planned** |
| 4 | **Reputational capture** — Silt branded a "piracy/dark-web tool." | Med × High | Positioning discipline; crisis-comms plan; visible takedown + no-operator posture; avoid crypto/piracy launch channels. | **planned** |
| 5 | **The publishing org is treated as an operator** and pulled into liability or coercion. | Low × Severe | Structural: no project-run nodes, no project-run list, no override; software-publisher posture; entity holds only trademark/domain/releases. | posture **built** into design; entity **planned** |
| 6 | **Bus factor** — single maintainer; project stalls or can't respond to incidents. | Med × High | Grow contributors (CONTRIBUTING, CODEOWNERS, review-gated merges); documented incident/disclosure process; foundation. | review-gate **built**; rest **planned** |
| 7 | **Sybil / eclipse attack** on the DHT or reputation to force bad commits or hide content. | Med × Med | Identity = keypair; **reputation now costs challenged held storage** (T1/#82 bond), so mass-producing *standing* costs real disk; harden lookup; monitor. | identity **built**; reputation-cost **built** (T1); eclipse/lookup hardening **planned** |
| 8 | **Denylist abused for censorship** (quorum removes lawful content). | Low × Med | Takedown is append-only and auditable (every revocation is a signed, replicated record); operators choose which lists to honor; transparency log. | auditability **built**; transparency norms **planned** |
| 9 | **No funding model**; project can't sustain audits, hosting of seeds, or legal. | Med × Med | Grants/sponsorship (explicitly **not** a token); foundation to receive funds. | **open** |
| 10 | **Supply-chain / release integrity** — tampered binaries. | Low × High | Reproducible builds (CGO-off, `-trimpath`); sign + checksum releases; publish provenance. | reproducible **built**; signing **planned** (see ROADMAP) |
| 11 | **Asymmetric resource-exhaustion DoS** — cheap calls that cost nodes large CPU/disk/bandwidth (repair-storm, decode/challenge amplification, handshake flood, disk-fill). Un-WAF-able (P2P surface). | High × High | Per-peer resource accounting; configurable rate limits; probe-before-repair; bounded frames/manifests; fuzz + panic-recover. | **planned** (catalog §A) |
| 12 | **Sybil / wash-serving → reputation-quorum capture** — free identities farm reputation/credits and collude to force or veto commits (censorship). | Med × Severe | **Consensus standing now costs challenged, held STORAGE, not self-reported serving** (T1/#82): a validator proves an identity-bound storage bond, peers challenge it over the wire, standing decays if unproven — so wash-serving buys zero standing and N sybils cost N real bonds. Plus training-wheels (row 15). | **much reduced, built** (T1 #82, unit+sim+e2e); residual: bond is space-lite/in-RAM (not memory-hard) and PoW/stake still deferred |
| 13 | **Zero-day patch propagation** across a decentralized fleet without a central kill-switch. | Med × High | Criticality-graded, threshold-signed, recallable version-floor advisories; operator-controlled upgrade. | **designed** (network-protection.md); build **planned** |
| 14 | **Publisher deanonymization** — the chain records publisher NodeID per root, linking a keypair to all its publications. | Med × Med | **Decided (2026-07-31): blind-signed publish tokens** (Chaumian-style) — a publish is unlinked from the durable reputation key while the fee/anti-spam economics are preserved (the ledger issues N tokens; it can't tell which publish each became). Note: silt protects *who-reads* (access) far better than *who-writes* (authorship); this closes the authorship gap. | **BUILT** (T3 #84): quorum-issued (k-of-n) blind publish tokens — a committed entry carries the token, not a Publisher NodeID; unit+sim+e2e. Residual: colluding-validator anonymity-set narrowing; in-RAM issuer key (both labeled) |
| 15 | **Day-one smallness** — eclipse, quorum capture, version-floor evasion all peak on a tiny launch network. | High × High | Launch-as-control: seeded anchors (time-boxed), gated reputation ramp, maturity-scaled thresholds, shed on measured decentralization. | **training wheels BUILT** (T2 #83): while immature, commits require anchor sign-off; sheds mechanically once N distinct non-anchor validators have attested (unit+sim). Remaining: version-floor advisory (R4/#13) |
| 16 | **Local-API hijack** — DNS-rebinding/CSRF drives the daemon's UI/JSON API on localhost (publish, spend credits, read link-book). | Med × Med | `Origin`/`Host` allow-listing + per-daemon API token; scoped app capabilities. | **open** (catalog I) |

## Recent status (2026-08-01)

Shipped since this register was last revised (on the current build wave, not
yet merged to main):
- **Silent-loss on publish is CLOSED** (S3/B7): #60 (manifest chunk) and #64
  (data-shard stripe eroding below k) — publish now returns no link unless the
  content is provably reconstructable, else fails loud.
- **Restart no longer orphans stored content** (#69): storage proofs are
  persisted (`adapters/diskproofs`) and reloaded, so a restarted holder
  re-announces its coded shards under the right key.
- **Relay throughput raised** (#65): session limits 64/8 → 128/16, plus a
  fetch-side retry for transient relay saturation.
- **Hole-punching primitive proven** (#27): relay paths upgrade to direct
  through cone NAT (symmetric falls back), CI-gated.
- **Testing is now automated**: an `integration/nat` Docker harness exercises
  cross-NAT publish/fetch, relay, hole-punch, and restart against real kernel
  NAT, gating every PR — the two-machine manual rig is demoted to optional.
- **New risk filed:** #71 config-drift (the daemon builds `node.Config` by
  hand and silently drops `DefaultConfig` fields — how the #65 fetch-retry was
  briefly inert and demand-dispersion is currently off).
- Still open/top weakness: Sybil (PoW/stake deferred) and its downstream
  (wash-serving, quorum capture); the security + legal review below is
  unchanged as the highest-leverage remaining action.

## The through-line

Risks 1, 2, 4, and 5 are one risk seen from four seats: a neutral network
that can't see its cargo. The mitigations reinforce each other — the
takedown mechanism (built), the no-operator posture (built into the
architecture), and disciplined messaging (planned) together make the
legal, safety, and PR positions defensible at once. The single highest-
leverage remaining action is an **independent security + legal review
before any production launch**.
