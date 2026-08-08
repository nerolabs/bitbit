# M0 owned residuals — the held-in-tension ledger

**Status: canonical residuals reference + research-collaboration surface.** M0's honesty
rule (`m0.md` §8): a seam *held in tension with a documented residual* is a **pass**; a
seam *silently assumed closed* is the failure. This doc is the single place every owned
residual is named, classified, bounded, and given an open question — so the set is legible
at a glance and the research team has one surface to push on.

Each residual carries:
- **Class** — why it is not closed: **theorem** (an impossibility result forbids closing it),
  **crypto-gap** (the construction exists but has no adoptable pure-Go impl — see
  [`primitive-availability-gaps.md`](primitive-availability-gaps.md)), **scope** (deliberately deferred past
  M0), or **research-frontier** (open problem, routed out).
- **What it is** · **Why not closed** · **How it's bounded today** · **What would close it** ·
  **Open question for research**.

Nothing here is load-bearing for the *shipped* M0 safety claims on the D-gated path (C1 no
discount, C2 no quiet capture, the demand→standing firewall) — those are held. These bound
*reach and tightness*, and each is labelled at the site it appears (`m0.md`, `TENETS.md`,
`decisions.md`, CHANGELOG).

---

## A. Sybil / concentration (C1 + C2)

### A1. The honest whale (C2 — held, not closed **by theorem**)
- **Class:** theorem (Kwon et al., AFT 2019 — no on-chain rule bounds a determined operator
  without a trusted identity authority).
- **What it is:** a single *real* operator who genuinely provisions distinct disk across
  distinct network positions, pays the full `D × A × M` cost per key, and *then chooses to
  collude*. C1 (no discount) still holds — they paid honest cost — but C2 (no quiet capture)
  cannot count them as one operator, because aggregate operator identity is unobservable
  on-chain.
- **Why not closed:** closing it needs proof-of-personhood / a trusted identity authority,
  which silt refuses (permissionless, content-blind, low-friction).
- **Bounded today by:** (1) the real dollar cost of disk × distinct AS positions per key;
  (2) the operator margin **M** (auto-armed > 1 for the untrusted posture); (3) the **A-axis**
  address-diversity gate — the shed counts distinct declared domains, so same-domain key
  splitting doesn't inflate the count; (4) the **HHI / Gini / top-share concentration alarm**
  (out-of-band veto, measurement not enforcement).
- **What would close it:** nothing on-chain (theorem). Only an external proof-of-uniqueness
  layer, which is out of scope.
- **Open question:** is there a *cheaper-than-personhood* uniqueness signal (beyond
  rentable /24 diversity) that raises the honest-whale cost further without a trust anchor?

### A2. `M` is unverifiable on-chain (#182)
- **Class:** theorem (same Kwon root as A1).
- **What it is:** the operator-margin `M` (keys-per-operator inflation) that discounts the
  Nakamoto coefficient to `⌊k̂/M⌋` is a *declared constant*, not a measured quantity.
- **Bounded today by:** conservative default (> 1, auto-armed); the A-axis makes part of it
  *earned* (distinct declared domains) rather than assumed.
- **Open question:** can telemetry (address/AS distribution, timing) give a *defensible
  lower bound* on `M` without an authority — or is a declared floor the honest ceiling?

### A3. Byzantine size-estimation under **adversarial** NodeID placement (#182)
- **Class:** research-frontier (least-supported load-bearing claim under C2).
- **What it is:** the CPR estimator behind C2's Byzantine-robust sampling tolerates
  `O(n^{1−δ})` Byzantine nodes *when randomly placed*; a stake-splitter *chooses* its
  NodeIDs, degrading the bound by an amount **the literature does not quantify**.
- **Bounded today by:** the DHT's `-dht-domain-cap` diversity + the A-axis; but the exact
  degradation is unquantified.
- **Open question:** quantify the CPR / size-estimation bound under *chosen* NodeID
  placement — or find a placement-robust estimator.

### A4. The γ→1/N shared-content sealing boundary (#182 — the single surviving economy of scale)
- **Class:** research-frontier (the core open research task, routed to an academic collaborator).
- **What it is:** C1's direct-product cost bound assumes cross-identity independence (H3). H3
  fails on the disk axis *if* standing were ever granted for "I can prove I hold shard *s*":
  erasure coding means many honest nodes legitimately hold the same shards, so one physical
  copy could answer for N pledges and the disk axis collapses **γ→1/N**.
- **Why silt is NOT exposed today:** standing comes from a *dedicated identity-keyed bond
  plot* (`seed = H("silt/bond/plot/v3" ‖ pk ‖ n)`), so N bonds need N× distinct sealed disk —
  the disk axis and the served-content axis are deliberately **separate**. The bonded disk is
  "wasted" throwaway labels rather than useful served content. C1 is gated on this staying
  separate.
- **What would close it (and unlock fusing served useful content into standing):**
  identity-keyed **PoRep sealing of arbitrary useful shared data** that is publicly-verifiable
  + timing-free + trusted-setup-free — which does not yet exist.
- **Open question:** the highest-leverage one — a sealing construction that lets one node's
  served useful bytes count toward its identity-keyed standing *without* letting one physical
  copy answer for N identities.

---

## B. Time and demand (T + B axes)

### B1. T-axis acquisition (relabelled — retention only ships)
- **Class:** scope (deferred M1+) + design (an age gate is unsound).
- **What it is:** `C_honest ∝ D×A×T×B` wants time to cost. **Retention** ships (decay/TTL forces
  continuous re-proof). **Acquisition-time accrual does not** — standing is granted in full on
  the first passing bond challenge, priced by D alone.
- **Why not built:** a bare `firstSeenTick` age gate is *pre-farmable* (the coin-age
  anti-pattern — Peercoin's CAA attack; NeuCoin removed coin-age). The only sound form is a
  **continuous VDF chained to the bond identity** — an M1+ construction (see
  [`primitive-availability-gaps.md`](primitive-availability-gaps.md) §5).
- **Open question:** is a bond-anchored continuous VDF worth its always-on cost over an
  already-non-substitutable D axis, or is D-only acquisition the right permanent answer?

### B2. Demand authenticity — a Douceur limit (D-DEMAND)
- **Class:** theorem (Douceur) — re-priced, not proven.
- **What it is:** a demand receipt proves *service happened*, not that the requester was a
  distinct honest party. Wash-demand (self-dealing) can't be *detected*.
- **Bounded today by:** **cost-to-wash** re-pricing — fee-burn per receipt + a bonded-fetcher
  credential (demand counts distinct bonded fetchers, so washing costs one on-chain storage
  bond per faked unit). Demand is a **neutral** observable (the firewall): forged demand buys
  **zero** consensus standing, so authenticity is not load-bearing for M0 safety.
- **Open question:** the anti-wash inequality `c_wash > c_real` is a monitored economic
  condition, not a proof — is there a tighter, still-permissionless requester-distinctness
  signal?

### B3. Demand-receipt residual leaks (seam-4)
- **Class:** scope (neutralized by the firewall today; must close before any demand→standing fusion).
- **What it is:** a receipt is forgeable with **zero** object bytes (the per-object PoR seed is
  public), and a bonded-mode receipt links fetch→standing key to one validator.
- **Bounded today by:** the firewall — demand has no consensus consumer, so both are inert.
- **What would close it:** needed only *if* B is ever fused into standing (γ→1/N territory, A4).

---

## C. Privacy (D-PRIV)

### C1. Fetcher metadata (IP + timing) unlinkability — D3/H8
- **Class:** scope (post-M0 H8) + theorem (anonymity trilemma).
- **What it is:** D3 issuance-mixing severs the *identity* + *payment* link (ephemeral key +
  blind credit + relay), verified over real TCP. A residual **transport IP + timing** link
  remains until epoch-batching / a mixnet ships.
- **Bounded today by:** the relay hides the fetcher IP from the issuer; timing-correlation is
  the residual. Deferred to the **H8 mixnet + PIR-DHT** track.
- **Why bounded, not closed:** the anonymity trilemma (Das et al.) — strong anonymity + low
  bandwidth + low latency can't all hold against a global adversary on a paid substrate.
- **Open question:** the metadata-layer anonymity-set achievable on a *small* paid network
  without a latency/bandwidth tax users won't pay.

---

## D. Durability & takedown

### D1. Bandwidth-blind proof-of-repair (H7)
- **Class:** crypto-gap (see [`primitive-availability-gaps.md`](primitive-availability-gaps.md) §1).
- **What it is:** M0 ships the Merkle-recompute floor (fetch k survivors, recompute, compare) —
  sound and content-blind but **not bandwidth-blind**. The blind form needs a char-2-native
  polynomial commitment (FRI-Binius) with no pure-Go impl.
- **Open question:** an F_p storage re-encode vs. waiting for a char-2-native commitment lib.

### D2. Tag-forgery on public per-object PoR keys (H7 — inert)
- **Class:** scope (documented H7 non-goal, inert under neutrality).
- **What it is:** a caretaker holds the layout key and can forge valid SW tags for *wrong*
  bytes; the recompute leg (re-derives correct bytes from survivors) closes it for repair, but
  the tag alone is forgeable.
- **Bounded today by:** the recompute correctness leg; and PoR standing is not fused into
  consensus.

### D3. MSR / regenerating-code proof-of-repair (off the critical path)
- **Class:** research-frontier (off critical path).
- **What it is:** the A1 proof-of-repair composition is airtight for *plain-RS* (what silt
  ships); no published construction specializes it to MSR/Clay regenerating codes.
- **Needed only if:** silt later adopts regenerating codes.

### D4. Cold-data solvency — finite-but-renewable (g > 0)
- **Class:** scope (instrument-first).
- **What it is:** perpetual durability solvency exists only when the cost-trend `g > 0`
  (declining cost per repair). M0 ships **finite-but-renewable** durability and instruments `g`.
- **Open question:** the parameter region where the internal credit reserve stays solvent
  across realistic cost trajectories.

### D5. Provable non-globality of takedown — ZK threshold predicate (D-TAKEDOWN)
- **Class:** crypto-gap (see [`primitive-availability-gaps.md`](primitive-availability-gaps.md) §4).
- **What it is:** M0 ships the CT-style transparency log (provable *recording* of every
  takedown, inclusion + consistency proofs). The stronger *survivor-Nakamoto* metric — a ZK
  predicate "≥ t distinct-domain PoR-fresh replicas are gone" — needs a ZK stack with no
  pure-Go impl.

---

## E. Consensus & bootstrap (F-1 fallout)

### E1. Bounded re-centralization after the maturity latch (F-1)
- **Class:** theorem (weak-subjectivity irreducibility) — the deliberate trade.
- **What it is:** once matured, silt never re-arms the launch anchors; a genuinely
  re-concentrated *real* bonded set keeps committing under the real-bond super-quorum, caught
  only by C2 + the A-axis + the alarm, never by anchors.
- **Why accepted:** it trades the (eliminated) permanent-center and undefined-halt risks for a
  **bounded, socially-recoverable** re-centralization risk — the same trade Ethereum, Cosmos,
  and Bitcoin all made retiring their training wheels. Irreducible for any weakly-subjective
  system.

### E2. Weak-subjectivity dependency (F-1 — newly explicit)
- **Class:** theorem (structural to all proof-of-stake-class systems).
- **What it is:** a node syncing from genesis or long-offline cannot distinguish the real
  matured chain from a forged long-range one on chain data alone, so it **must** be pinned to a
  recent trusted **weak-subjectivity checkpoint** (`-ws-checkpoint`) within the WS period
  (~`BondTTLBlocks` + slashing depth).
- **Open question:** the exact WS period derivation for silt's turnover/eviction dynamics, and
  checkpoint *distribution* tooling (explorer endpoints, client-bundled checkpoints) — post-M0.

### E3. Reachability of maturity (held in tension)
- **Class:** scope (a parameterized bet, not a theorem).
- **What it is:** M0's Sybil soundness is conditional on the mature regime being reached before
  the young, anchor-scaffolded regime is captured. No proof — a safe-parameterization (plural
  threshold anchors + the one-way latch), with levels that must track live telemetry.

---

## F. Cryptographic dependency gaps (pure-Go)

Five constructions silt *would* adopt but for which no mature pure-Go impl exists in 2026 —
enumerated in [`primitive-availability-gaps.md`](primitive-availability-gaps.md): (1) char-2-native polynomial
commitment (blind PoR); (2) threshold decryption + DKG (fair-exchange dispute / accountable
disclosure); (3) verifiable encryption; (4) ZK threshold predicate (D5); (5) continuous
identity-chained VDF (B1). Each ships a sound floor and defers the full form.

---

## The through-line

Every residual above is either **impossible to close** (a named theorem), **gated on a
primitive that has no adoptable pure-Go impl**, or **deliberately scoped past M0** — and each
is *labelled at the site it appears*, not silently assumed closed. That labelling is the M0
deliverable. The two highest-leverage research questions are **A4** (γ→1/N sealing — the one
surviving economy of scale) and **A1/A2** (cheaper-than-personhood uniqueness to tighten the
honest-whale bound). Research team: push hardest there.
