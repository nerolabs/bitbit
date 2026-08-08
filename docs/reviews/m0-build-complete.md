# M0 build backlog — COMPLETE (status + owned-residuals reference)

**Status (2026-08-08):** the M0 build backlog is **complete**. This is the counterpart
to `m0-remaining-backlog.md` (the earlier "what is NOT done yet" brief) — that list is
now cleared. What remains for M0 RC is the **verify gate**, not more building.

> **⚠️ HOW TO USE THIS — read before sharing with a review team.** Your **blind brief**
> (`m0-redteam-brief.md` for the red team, `m0-acceptance-brief.md` for acceptance) is
> the **primary directive** — it stands alone; you find breaks / test flows
> independently from the code and the public docs. This file is an **owner status +
> owned-residuals reference**. If the owner shares it with you, use **§4 (held-in-
> tension residuals) ONLY, to AVOID RE-REPORTING owned items** — exactly as a prior
> blind pass was told to skim the old "not-done" list. **Do NOT treat §2 (built
> surface) or §3 (what to red-team) as a hunting checklist** — reading them as an answer
> key defeats a blind pass. Challenge whether the *framing* of the residuals is honest,
> not whether they exist.

---

## 1. What the M0 build delivers (the systemic claim)

M0's Sybil corner is **not** a per-primitive "Sybil-proof" claim (false by Douceur). It
is a **composition in tension**: **C1 (no discount)** — standing costs real held storage,
no cheaper — **+ C2 (no quiet capture)** — concentration is measured and the training
wheels shed only on measured decentralization. Both are held-in-tension, never "closed."
Consensus standing is **earned by storage** (an identity-bound space-time bond), never
bought with chatter; served-demand is a **neutral observable** that confers zero
standing (the γ→1/N firewall). Source of truth: `docs/design/m0.md`, `docs/decisions.md`.

## 2. What was built (this arc) — the attack surface

- **D-DEMAND** (blind demand receipt, `core/demand`, neutral): unforgeable delivery
  receipt (blind-withdrawn token + PoR-bound ack); **cost-to-wash** re-pricing — fee-burn
  (P3a) + **bonded-fetcher credential** (P3b: demand counts distinct bonded fetchers, so
  wash costs one on-chain storage bond per faked unit); **P2** optimistic fair-exchange
  **abort-safety floor** (token-not-consumed-on-abort; a pre-release commitment can't
  redeem as demand).
- **#184 real-wire adversarial harness** (`core/node/adversary.go`, `partition.go`; e2e):
  all four consensus-safety cases proven over **real TCP** — equivocation→slash,
  partition→heal, forged-block→reject, low-bond→reject. Red-team flags (`-equivocate`,
  `-block-peers`, `-forge-block`, `-lowbond-propose`) are in the shipped binary so an
  external team can drive the same attacks against a live deployment.
- **D3 issuance-mixing** (`client/`): private token withdrawal over a **fresh ephemeral
  identity** paying with a **prepaid blind credit** (hides the fetcher's identity), and —
  given a relay-form issuer address — **routed through a content-blind relay** (hides the
  fetcher's IP). Fixed a latent bug where `tcpnet` dropped the `Credit` field over the
  wire (the F4/D3 fee-decoupling had only ever worked in the sim).
- **Registry economics**: `-registry-only` (serve a registry with **no storage node**),
  `-freeload` (routing node that refuses to host content), + **read-cost bounding**
  (per-IP rate limit + server timeouts).
- (Earlier in the arc: **H7** durability escrow + proof-of-repair; **C2** metric wiring;
  **H9** CT-style transparency log; all merged.)

## 3. What to red-team

The unit of test is the **systemic claim** (C1 + C2 + the seven `m0.md` §7 composition
seams), not "is this primitive Sybil-proof". Suggested targets:

1. A **C1 economic discount** — any way to get consensus standing for less than
   `q · C_honest` (a real held-storage bond per unit of standing).
2. A **C2 capture-past-threshold** — quiet accumulation past the measured concentration
   bound, or tripping/evading the maturity shed.
3. The **new mechanisms**: forge or double-count a demand receipt; make wash free again;
   link a D3 private withdrawal back to the fetcher; defeat the adversarial-harness
   defenses (equivocation slash, partition heal, proposal rejection) over the wire.
4. The **γ→1/N firewall**: any path by which served-demand (or any neutral observable)
   moves consensus standing.

## 4. Held-in-tension residuals — KNOWN; do not re-report as new

Please challenge the **honesty of the framing**, not the existence of these:

- **F-1 (anchor liveness dual)** — the maturity "escape hatch" is a permanent-anchor
  liveness dependency in objective mode; **deliberately DEFERRED** (owner's call: don't
  re-plan on one seam; added network tension may moot it). A PoC fix exists.
- **γ→1/N shared-content sealing boundary** (#182) — the one surviving economy of scale;
  silt is not exposed today (standing = a dedicated identity-keyed bond plot), tracked.
- **Tag-forgery** on public per-object PoR keys (H7-documented, inert under neutrality).
- **Demand authenticity** — a Douceur limit; not provable by any receipt, only re-priced
  (cost-to-wash). This is by design, not a gap.
- **C2 operator-clustering heuristic + `M_est`** — a heuristic-by-theorem (Kwon), not a
  proof; owned.
- **P2 dispute-resolution** — gated on verifiable-escrow + threshold-decryption crypto
  (no adoptable pure-Go lib); floor shipped, resolution deferred; low-stakes under
  demand-neutrality.
- **D3 timing-correlation** (epoch-batching) — deferred to the post-M0 H8 mixnet.
- **Registry liveness-pruning + federation** — post-launch.
- **Post-M0 tracks**: H8 mixnet/PIR, H9 ZK non-globality predicate, blind proof-of-repair.

## 5. What to accept-test (the user seam still holds)

- Publish → commit → fetch round-trips bit-perfect; validator onboarding (earned bonded
  standing) commits on the safe defaults; safe defaults refuse a lone/unearned commit.
- Registry roles: `-serve-registry`, `-freeload`, `-registry-only` come up and serve.
- The demand-receipt flow and the D3 private withdrawal work end to end.
- Reference: `../user-seam.md`, `../test-topologies.md`, `m0-acceptance-brief.md`.

## 6. The verify gate (declares M0 RC)

1. **#52** — multi-machine field test on real hardware (owner's rig).
2. A **fresh, no-memory red-team** pass against this brief.
3. The **acceptance loop**.

Passing all three declares **M0 RC**.
