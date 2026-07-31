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
| 7 | **Sybil / eclipse attack** on the DHT or reputation to force bad commits or hide content. | Med × Med | Identity = keypair (costly to mass-produce reputation); reputation earned via audits+serving; harden lookup; monitor. | identity **built**; hardening **planned** |
| 8 | **Denylist abused for censorship** (quorum removes lawful content). | Low × Med | Takedown is append-only and auditable (every revocation is a signed, replicated record); operators choose which lists to honor; transparency log. | auditability **built**; transparency norms **planned** |
| 9 | **No funding model**; project can't sustain audits, hosting of seeds, or legal. | Med × Med | Grants/sponsorship (explicitly **not** a token); foundation to receive funds. | **open** |
| 10 | **Supply-chain / release integrity** — tampered binaries. | Low × High | Reproducible builds (CGO-off, `-trimpath`); sign + checksum releases; publish provenance. | reproducible **built**; signing **planned** (see ROADMAP) |
| 11 | **Asymmetric resource-exhaustion DoS** — cheap calls that cost nodes large CPU/disk/bandwidth (repair-storm, decode/challenge amplification, handshake flood, disk-fill). Un-WAF-able (P2P surface). | High × High | Per-peer resource accounting; configurable rate limits; probe-before-repair; bounded frames/manifests; fuzz + panic-recover. | **planned** (catalog §A) |
| 12 | **Sybil / wash-serving → reputation-quorum capture** — free identities farm reputation/credits and collude to force or veto commits (censorship). | Med × Severe | Earned reputation; witnessed/challenged serving; training-wheels at launch; (PoW/stake **deferred**). | **open** (catalog B, D1, D3) |
| 13 | **Zero-day patch propagation** across a decentralized fleet without a central kill-switch. | Med × High | Criticality-graded, threshold-signed, recallable version-floor advisories; operator-controlled upgrade. | **designed** (network-protection.md); build **planned** |
| 14 | **Publisher deanonymization** — the chain records publisher NodeID per root, linking a keypair to all its publications. | Med × Med | Decide intended vs. leak; if a leak, unlink/blind; document the trade-off (reputation needs attribution). | **open** (catalog F1) — needs a decision |
| 15 | **Day-one smallness** — eclipse, quorum capture, version-floor evasion all peak on a tiny launch network. | High × High | Launch-as-control: seeded anchors (time-boxed), gated reputation ramp, maturity-scaled thresholds, shed on measured decentralization. | **planned** (network-protection.md) |
| 16 | **Local-API hijack** — DNS-rebinding/CSRF drives the daemon's UI/JSON API on localhost (publish, spend credits, read link-book). | Med × Med | `Origin`/`Host` allow-listing + per-daemon API token; scoped app capabilities. | **open** (catalog I) |

## The through-line

Risks 1, 2, 4, and 5 are one risk seen from four seats: a neutral network
that can't see its cargo. The mitigations reinforce each other — the
takedown mechanism (built), the no-operator posture (built into the
architecture), and disciplined messaging (planned) together make the
legal, safety, and PR positions defensible at once. The single highest-
leverage remaining action is an **independent security + legal review
before any production launch**.
