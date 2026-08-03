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
| 3 | **Unaudited crypto/consensus fails at scale** — the trust-plane mechanism is built but has had no independent review. | Med × High | Independent audit + threat model before any "production" claim; keep honest labeling; bug-bounty; Sybil/eclipse hardening. The three Gate-4 consensus gaps the build-vs-intention audit (2026-08-02) named are now CLOSED: equivocation detection + slashing and fork-choice reconciliation (#100), and a real verify-without-fetch PoR + proof-of-space-time bond replace the placeholders. See `docs/design/gate4-m0-mechanism.md`. | mechanism **built + internally tested** (#117–#126); **independent adversarial review + field test is the remaining bar** (see `docs/reviews/`); residuals honestly labelled (locally-qualified fork-choice weight; heavy prover work partly on-loop) |
| 4 | **Reputational capture** — Silt branded a "tool for wrongdoing / dark-web tool." | Med × High | Positioning discipline; crisis-comms plan; visible takedown + no-operator posture; avoid crypto and wrongdoing-signaling launch channels. | **planned** |
| 5 | **The publishing org is treated as an operator** and pulled into liability or coercion. | Low × Severe | Structural: no project-run nodes, no project-run list, no override; software-publisher posture; entity holds only trademark/domain/releases. | posture **built** into design; entity **planned** |
| 6 | **Bus factor** — single maintainer; project stalls or can't respond to incidents. | Med × High | Grow contributors (CONTRIBUTING, CODEOWNERS, review-gated merges); documented incident/disclosure process; foundation. | review-gate **built**; rest **planned** |
| 7 | **Sybil / eclipse attack** on the DHT or reputation to force bad commits or hide content. | Med × Med | Identity = keypair; **reputation now costs challenged held storage** (T1/#82 bond), so mass-producing *standing* costs real disk; harden lookup; monitor. | identity **built**; reputation-cost **built** (T1); eclipse/lookup hardening **planned** |
| 8 | **Denylist abused for censorship** (quorum removes lawful content). | Low × Med | Takedown is append-only and auditable (every revocation is a signed, replicated record); operators choose which lists to honor; transparency log. | auditability **built**; transparency norms **planned** |
| 9 | **No funding model**; project can't sustain audits, hosting of seeds, or legal. | Med × Med | Grants/sponsorship (explicitly **not** a token); foundation to receive funds. | **open** |
| 10 | **Supply-chain / release integrity** — tampered binaries. | Low × High | Reproducible builds (CGO-off, `-trimpath`); sign + checksum releases; publish provenance. | reproducible **built**; signing **planned** (see ROADMAP) |
| 11 | **Asymmetric resource-exhaustion DoS** — cheap calls that cost nodes large CPU/disk/bandwidth (repair-storm, decode/challenge amplification, handshake flood, disk-fill). Un-WAF-able (P2P surface). | High × High | Per-peer resource accounting; configurable rate limits; probe-before-repair; bounded frames/manifests; fuzz + panic-recover. | **planned** (catalog §A) |
| 12 | **Sybil / wash-serving → reputation-quorum capture** — free identities farm reputation/credits and collude to force or veto commits (censorship). | Med × Severe | **Consensus standing now costs challenged, held STORAGE, not self-reported serving** (T1/#82): a validator proves an identity-bound storage bond, peers challenge it over the wire, standing decays if unproven — so wash-serving buys zero standing and N sybils cost N real bonds. Plus training-wheels (row 15). | **much reduced, built** (T1 #82 + Gate 4, unit+sim+e2e); the bond is now a real **proof-of-space-time** (a space-hard identity-bound plot × a Wesolowski VDF, persisted, bound so N Sybils cost N real disks — #119–#123), and **equivocation/double-signing is now slashed** (#100/#125). Residuals: not yet a formally depth-robust / memory-hard label function; PoW/stake on *minting* still deferred; no external audit yet |
| 13 | **Zero-day patch propagation** across a decentralized fleet without a central kill-switch. | Med × High | Criticality-graded, threshold-signed, recallable version-floor advisories; operator-controlled upgrade. | **designed** (network-protection.md); build **planned** |
| 14 | **Publisher deanonymization** — the chain records publisher NodeID per root, linking a keypair to all its publications. | Med × Med | **Decided (2026-07-31): blind-signed publish tokens** (Chaumian-style) — a publish is unlinked from the durable reputation key while the fee/anti-spam economics are preserved (the ledger issues N tokens; it can't tell which publish each became). Note: silt protects *who-reads* (access) far better than *who-writes* (authorship); this closes the authorship gap. | **BUILT but OFF by default** (T3 #84): quorum-issued (k-of-n) blind publish tokens — a committed entry *can* carry the token, not a Publisher NodeID; unit+sim+e2e. **Audit 2026-08-02: `-require-tokens` defaults to 0, so the default publish still writes a permanent `Publisher→root` map to the append-only chain (#97), and the non-chain `Gated` registry hard-requires a Publisher (#99).** Until the default flips, unlinkability is available, not the default. Residual: colluding-validator anonymity-set narrowing (the D3 mix/relay + epoch-batched issuance is deferred); unlinkability is cross-layer (DHT/transport also leak NodeID). The issuer key now **persists** across restarts (#126); on-chain issuer registration remains |
| 15 | **Day-one smallness** — eclipse, quorum capture, version-floor evasion all peak on a tiny launch network. | High × High | Launch-as-control: seeded anchors (time-boxed), gated reputation ramp, maturity-scaled thresholds, shed on measured decentralization. | **training wheels BUILT** (T2 #83): while immature, commits require anchor sign-off; sheds mechanically once N distinct non-anchor validators have attested (unit+sim). Remaining: version-floor advisory (R4/#13) |
| 16 | **Local-API hijack** — DNS-rebinding/CSRF drives the daemon's UI/JSON API on localhost (publish, spend credits, read link-book). | Med × Med | `Origin`/`Host` allow-listing + per-daemon API token; scoped app capabilities. | **open** (catalog I; #89) |
| 17 | **Chain-permanence traps** — the chain is append-only with no reorg, so a wrong record shape written before the fix is unrecoverable. Three live: publisher-linkage on by default (#97), unversioned `Block`/`Entry` schema so any Gate-4 record change is a silent hard fork (#98), and the `Gated` registry requiring a Publisher (#99). | Med × High | Fix all three **before any persistent network writes blocks**: default to tokened publish, add a `Version` field + decode guard, fence off / tokenize `Gated`. Surfaced by the build-vs-intention audit (2026-08-02). | **open**, issue-tracked (#97/#98/#99) |

## Recent status (2026-08-02)

- **Build-vs-intention audit** (`docs/reviews/build-vs-intention-2026-08-02.md`):
  code-grounded check of the build against the immutables + M0 + gate spine.
  Verdict: architecture sound, seams clean (Gate 4 is a swap, not a rewrite), no
  immutable violation forces a reversal. Named three append-only-chain permanence
  traps to fix before real blocks (#97/#98/#99), one missing consensus defense
  (#100, equivocation/slashing), and pre-code Gate-4 constraints
  (`docs/design/gate4-m0-mechanism.md`). Rows 3, 12, 14 updated; row 17 added.

Shipped and merged to main since this register was last revised:
- **Trust plane: the real M0 mechanism is now BUILT and internally tested**
  (Gate 4, PRs #117–#126), replacing the earlier honestly-labeled placeholders.
  A verify-without-fetch **proof-of-retrieval** (#117/#118); a
  **proof-of-space-time bond** — a space-hard identity-bound plot × a Wesolowski
  VDF, persisted across restart, bound so N Sybils cost N real disks
  (#119–#123); **standing** as the time-integral of bond + audit gating
  consensus and revocation; **fork-choice reconciliation** so partitions heal to
  the heavier-standing chain (#124); **equivocation** that slashes double-signers
  (#125); a **persisted issuer key** (#126); on the launch-window training wheels
  (T2/#83) and blind publish tokens (T3/#84) already landed. Proven at unit +
  in-process sim + real-daemon e2e (including a two-validator consensus commit
  over TCP). **Remaining before it is *proven*: independent adversarial review
  (see `docs/reviews/`) + the multi-machine field test (#52).** Residuals honestly
  recorded in the CHANGELOG and design §6 (not yet a formally depth-robust /
  memory-hard label function; locally-qualified fork-choice weight; the D3
  issuance-mixing; on-chain issuer/equivocation records).
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
- **Config-drift CLOSED** (#71): the daemon had built `node.Config` by hand and
  silently dropped `DefaultConfig` fields (which had left the #65 fetch-retry
  briefly inert and demand-dispersion off); the daemon now derives from
  `DefaultConfig` so those defaults hold.
- Sybil and its downstream (wash-serving, quorum capture, DHT eclipse) was the
  top open weakness; the chosen non-token answer — work-backed, identity-bound
  standing that costs challenged **space-time** — is now built (row 12) rather
  than a first cut. Residual: the bond is not yet a formally depth-robust /
  memory-hard label function; cheap identity *minting* (PoW/stake deferred) and
  DHT-level eclipse remain unpriced; and the plot-amortization binding, while
  closed by per-identity secrets + root dedup, is not a zero-knowledge proof of
  correct plotting. **The independent security review below is the
  highest-leverage remaining action** and is now scheduled (`docs/reviews/`).

## The through-line

Risks 1, 2, 4, and 5 are one risk seen from four seats: a neutral network
that can't see its cargo. The mitigations reinforce each other — the
takedown mechanism (built), the no-operator posture (built into the
architecture), and disciplined messaging (planned) together make the
legal, safety, and PR positions defensible at once. The single highest-
leverage remaining action is an **independent security + legal review
before any production launch**.
