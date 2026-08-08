# M0 — what is NOT done yet (review brief for the research + red teams)

> **⚠ SUPERSEDED (2026-08-08): this "NOT done" list is now CLEARED — the M0 build
> backlog is complete.** See **`m0-build-complete.md`** for the current status overlay
> (built surface, what to red-team/accept, the known held-in-tension residuals, and the
> verify gate). This file is retained as the historical snapshot of the build backlog.

> **Purpose.** This is the honest, specific list of what remains for M0, written for
> the **research team** and the **adversarial red team** to weigh in on before that
> work is committed to or the M0-RC verify gate is run. It is deliberately a
> *not-done* list: what is built and green is in [`../../CHANGELOG.md`](../../CHANGELOG.md),
> the composition spec is [`../design/m0.md`](../design/m0.md), and the decisions are
> [`../decisions.md`](../decisions.md).
>
> **Status as of 2026-08-09.** The C1×C2 composition is built and internally
> regression-locked — bond (D) + durability/S7 (H7) + witnessed-demand (B, D-DEMAND)
> + concentration metric (C2) + provably-non-silent takedown (H9) — with the one
> load-bearing invariant held throughout (every new economy is classified `neutral`;
> the γ→1/N firewall is intact). **What is below is the remainder — none of it is
> "done," and some of it is deliberately deferred past M0.**

---

## A. M0-residual BUILD items — in scope, not yet built

These are M0 build-track items (not deferred) that remain. Prioritised.

1. **#184 — the four consensus-SAFETY cases over the REAL WIRE** *(the M0-RC
   accountability gate).* Each passes in unit/sim today; **none is yet proven over
   real multi-process/multi-host TCP**:
   - equivocation-slash (double-sign caught by on-chain `Block.Slashes`, evicted);
   - partition → heal to the heavier-bonded fork;
   - low-bond validator earns no standing / can't attest;
   - forged / tampered block rejected.
   **Blocker:** the `e2e/` harness spawns honest daemons only; proving these needs
   either **adversarial daemon variants** (a double-sign flag, network-partition
   control) or a **test that speaks the raw consensus wire**. That harness does not
   exist yet. **Open question for review:** is real-wire e2e required for M0-RC, or is
   sim-passed + the #52 field test sufficient (tag `sim-passed / real-wire-open`)?

2. **D3 issuance-mixing** *(the one M0 residual of H8; the rest of H8 is post-M0 — §B).*
   Route token issuance over the content-blind relay from an ephemeral identity,
   **epoch-batched**, to close the publisher/fetcher IP+timing link. Ephemeral
   identities + blind credits + blind demand-token withdrawal already exist; what
   remains is the **relay-routing + epoch-batching of issuance** (transport-layer,
   needs the real-wire harness). **Until this ships, fetcher-unlinkability is
   NOMINAL** — the blind signature hides the token serial, not the withdrawer's
   network identity (see §C).

3. **D-DEMAND remaining phases** *(the receipt primitive + wire + cost-to-wash are
   built; these are the tail):*
   - **P2** — optimistic fair-exchange dispute (both abort paths), validator quorum
     as the threshold TTP (Asokan–Shoup–Waidner). Not built.
   - **P3b** — the **bonded-fetcher credential** (count a receipt toward demand only
     if the fetcher shows a scarce bond-distinct credential, pricing wash onto the
     fetcher-identity supply). The fee-burn half of P3 IS built + regression-locked;
     this second lever is not.

4. **Registry economics tail:**
   - **`-registry-only`** daemon mode (construct no storage node at all — the
     `-freeload` quick win shipped; this leaner mode is a daemon-startup refactor).
   - **#48** — keep registries cheap: liveness-pruning of dead entries, read-cost
     bounding/caching, federation/sharding. Not built.

---

## B. Explicitly POST-M0 (deferred by decision — NOT M0 scope)

Called out so the review does not mistake these for M0 gaps. Each is deferred by a
committed decision, not an oversight.

- **H8 metadata-privacy stack — mixnet transport + private-DHT PIR** (D-PRIV:
  *"a post-M0 build track, not required to make the tenet honest now"*). The D-PRIV
  tenet is made honest by the *decision* (immutable #4 → a layered, stated tradeoff +
  refusal-to-surveil), not by shipping a mixnet. Only D3 (§A2) is M0.
- **H9 ZK non-globality PREDICATE + PIR-routed probes** — the survivor-Nakamoto
  threshold predicate that *quantifies* how non-global a takedown is. The CT-style
  transparency log it sits on top of IS built (H9). The predicate is post-M0.
- **Plaintext/bandwidth-blind proof-of-repair** — the F_p re-encode (Semi-AVID-PR) or
  a characteristic-2-native commitment (FRI-Binius). M0 ships the **Merkle-recompute
  floor** (sound, content-blind, *not* bandwidth-blind — an explicit non-goal); the
  blind upgrade is a fast-follow (theorem-level GF(2⁸)→F_r gap; see design doc §3/§13).
- **MSR / regenerating-code proof-of-repair** — no published construction; silt ships
  plain-RS. Off the critical path.

---

## C. Known RESIDUALS — held-in-tension, documented, NOT closed

**This is the section most worth the red team's and research team's eyes.** Each is a
*documented* limit silt does not claim to have closed. The question for review: are
the bounds and the "not exposed today" arguments actually sound?

1. **⭐ The shared-content sealing boundary — γ→1/N (#182, the core open problem).**
   Plain PoR over *shared* erasure-coded shards lets one physical copy answer for N
   pledges, collapsing the disk axis to γ→1/N — closed only by identity-keyed PoRep
   *sealing* of arbitrary useful shared data (not yet publicly-verifiable + timing-free
   + trusted-setup-free). **silt is not exposed today** because standing comes from a
   *dedicated* identity-keyed bond plot, *separate* from served content. **The C1
   claim is gated on this separation holding.** *Is the separation argument airtight?*

2. **Tag-forgery in proof-of-repair AND in the demand receipt.** Both reuse
   Shacham–Waters PoR with a key derived from public/layout material, so a party with
   the key can forge tags for wrong bytes → the retrievability leg certifies "holds
   bytes matching *some* tags," not "holds the *correct* bytes." In H7 the
   **recompute leg closes it** for correctness; in demand it is a documented residual,
   **inert because demand is neutral** (a forged receipt buys zero standing). *Is
   "neutral ⇒ inert" a sufficient answer for demand?*

3. **Demand authenticity is a Douceur limit, not an engineering gap.** No receipt
   proves the fetcher was economically independent (a self-fetch is a real paid
   delivery). silt re-prices wash (fee-burn built; bonded-fetcher credential = P3b,
   §A3), never proves authenticity. *Is cost-to-wash priced high enough to matter, and
   what is the right fee/standing-reward ratio (a parameter, unset)?*

4. **C2 operator-clustering is heuristic BY THEOREM (Kwon).** On-chain data carries no
   operator label, so the Nakamoto coefficient is over bond-distinct *keys*; the
   operator margin `M` (⌊k̂/M⌋, shed at k̂ ≥ k·M) is a conservative stand-in. **`M_est`
   under adversarial NodeID placement is unquantified** (a flagged research gap). The
   **honest-whale / real cartel** is outside C2 entirely (bounded only by HHI veto +
   cost-to-corrupt + anchor wheels). *What is a defensible `M` for a real deployment?*

5. **Byzantine size-estimation under adversarial NodeID placement.** The C2 sampling
   tolerance is proven only for *random* Byzantine placement; a stake-splitter chooses
   its NodeIDs, degrading it by an amount the literature does not characterise.

6. **Durability is finite-but-renewable, not perpetual.** Perpetual cold-data solvency
   needs the credit-cost decline `g > 0` (instrumented, not assumed); if `g ≤ 0` the
   contract stays solvent only by re-endowment before the funded horizon expires. *Is
   the `g` instrument measuring the right thing?*

---

## D. The VERIFY GATE — not yet run (M0 is "held" only when this passes)

Per the build-then-verify discipline, M0 is *built* but not *held* until an external
pass at declared parameters. **None of this has run** (and, per the current directive,
the external passes come only AFTER the build backlog above is settled):

- **#52 — multi-machine field test (R1):** bonds, tokens, consensus across real
  machines and real NAT. Needs real hardware.
- **#184 — real-wire consensus safety** (also §A1) — the hard child of #52.
- **#183 — external red-team vs C1/C2** and the seven `m0.md` §7 composition **seams**
  (not isolated primitives — a primitive failing a standalone "Sybil-proof" test is
  Douceur, expected). *Blocked-by C2 (#185), which is now built, so #183 is unblocked.*

---

## E. Specific asks for the two teams

**Research team:**
- Is the **γ→1/N separation argument** (§C1) sound as the gate on C1, or is there a
  leak between the dedicated bond plot and served content?
- A defensible **`M` (operator margin)** and any way to bound **`M_est`** under
  adversarial placement (§C4, §C5).
- Whether **"neutral ⇒ forged-receipt-inert"** (§C2) is a complete answer, or whether
  demand must be authenticated further before it can ever feed standing.

**Adversarial red team:**
- Attack the **seven composition seams** (`m0.md` §7), not the primitives.
- The **self-dealing / wash** economics (§C3): is cost-to-wash actually loss-making at
  realistic parameters?
- The **consensus-safety cases** (§A1/§D) — especially whether sim-passed is a
  credible stand-in for real-wire, or a place a break hides.
- The **takedown transparency** guarantees (H9): can a takedown be hidden from the
  CT-style log, or history rewritten without breaking a consistency proof?
