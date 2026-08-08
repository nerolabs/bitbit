# M0 claims ledger — every "delivered" claim, and the test that backs it

**Status: the truth-in-labelling ledger.** M0's credibility rests on honest
held-vs-closed accounting, and the recurring failure the red team finds is
*docs-ahead-of-code* — the spec asserting a property the code does not deliver. This
ledger is the standing guard: **every load-bearing claim that M0 states as *delivered*
must appear here with the test (or code path) that delivers it.** `scripts/check_claims.py`
runs in CI and **fails the build if any test named below no longer exists** (renamed,
deleted) — so a claim can never quietly go unbacked. (Whether the test *passes* is the
normal test suite's job; this checks the *linkage*.)

Residuals that are **held-in-tension, not delivered**, live in the counterpart doc
[`owned-residuals.md`](owned-residuals.md) — do not add them here. A property is either
in this ledger with a passing test, or in that one as an owned residual. Nothing
load-bearing should be in neither.

> **How to maintain:** when you ship a mechanism that closes a claim, add a row here with
> its test. When you rename/remove a test, update its row (CI will otherwise fail). When
> you *soften* a claim to a residual, move it to `owned-residuals.md`.

---

## The composition claim (C1 + C2)

| Claim (asserted in the canon) | Delivered by (test) |
|---|---|
| **C1 — no discount (D-axis):** a forged or unheld storage bond earns no standing | `TestForgedOrUnheldBondFails` |
| **C1 — no discount (PoR):** a forged proof-of-retrieval is rejected | `TestForgedProofFails` |
| **C2 — no quiet capture:** the maturity shed gates on cost-to-corrupt, not head-count (a whale-dominated set stays immature) | `TestC2Metric_OperatorMarginRaisesShedBar` |
| **C2 — split-defense is safe-by-default:** an untrusted objective validator auto-arms operator-margin `M > 1` | `TestOperatorMarginDefaultsAboveOneForUntrustedValidator` |
| **C2 — A axis:** the shed counts *address-diverse* participants; same-domain key-splitting does not shed, distinct domains do | `TestC2Metric_AddressDiversityGate` |
| **C2 — concentration is surfaced** (HHI / Gini / top-share; ⅓ alarm) as an out-of-band veto | `TestC2Metric_ConcentrationSignals` |
| **The demand→standing firewall:** no non-mint ledger press raises consensus standing | `TestInvariantA_NoNonMintPressRaisesStanding` |
| **The firewall (audited):** the ONLY standing-minting press is bond-gated, and every ledger method is classified | `TestInvariantA_TheOnlyMintingPressIsBondGated`, `TestInvariantA_EveryLedgerMethodClassified` |

## Consensus safety & bootstrap (F-1, #184)

| Claim | Delivered by (test) |
|---|---|
| **F-1 — one-way ratchet:** the one-way maturity latch means concentration never re-arms the anchors (both halt and permanent-center horns dead) | `TestMaturityLatchDoesNotRearmAnchorsOnDemature` |
| **F-1 — the latch is a consensus fact:** it re-derives on reload and across a reorg | `TestMaturityLatchSurvivesReloadAndReconcile` |
| **F-1 — de-maturation super-quorum:** a de-matured network commits on ≥⅔ real bonded weight, no anchors | `TestDeMatureSuperQuorumReplacesTheAnchorNet` |
| **F-1 — weak-subjectivity checkpoint:** a fork rewriting history at/before the checkpoint is refused regardless of weight | `TestWeakSubjectivityCheckpointRefusesLongRangeReorg` |
| **#184 — equivocation → slash** over the real wire | `TestEquivocatorSlashedOverTCP` |
| **#184 — partition → heal** onto the heavier fork | `TestPartitionHealsToHeavierForkOverTCP` |
| **#184 — forged-block → reject** by an honest validator | `TestForgedBlockRejectedOverTCP` |
| **Objective fork-choice heals** a partition to the heavier-standing chain | `TestObjectiveConsensusCommitsOverTCP`, `TestReconcileAdoptsHeavierFork` |
| **Earned standing commits** on the safe path (not a `-quorum 0` rubber-stamp) | `TestBondEarnedStandingCommitsOverTCP` |
| **T axis — retention:** unrenewed objective standing decays (no release-and-coast) | `TestObjectiveBondStandingDecaysWithoutRenewal` |

## User-seam claims (tenets + site)

| Claim | Delivered by (test) |
|---|---|
| **Content-addressed, verified on every read** (bit-perfect or an explicit failure): corruption is caught + repaired, not served | `TestCorruptionIsRepairedFromParity` |
| **Erasure-coded durability:** a file survives node churn with repair; degrades without it | `TestChurnRepairKeepsFileAlive`, `TestChurnWithoutRepairDegrades` |
| **Unlinkable publish:** a token-published entry carries no durable Publisher identity | `TestUnlinkablePublishOverTCP` |
| **Per-hash, pluralistic takedown:** a revocation is per-operator, opt-in, existence-checked — never a global switch | `TestChainRevocationCommitsOverTCP`, `TestF5_RevocationIsPerOperator_Denied`, `TestF5_ChainRevocationHonoringIsOptIn` |
| **Survives restart:** a validator's bond standing reloads instead of re-plotting | `TestEnableBondReloadsInsteadOfReplotting` |
| **Prepaid credit survives the wire** (F4/D3 fee-decoupling is not sim-only) | `TestCreditSurvivesWire` |

## Registry read-cost bounding (#48 / F-3)

| Claim | Delivered by (test) |
|---|---|
| **Read-cost bound:** a per-IP token bucket absorbs bursts and throttles sustained floods | `TestRateLimiterBurstThenThrottleThenRefill`, `TestRateLimiterIsPerIP` |
| **F-3 — the whole-registry `/all` dump is off the public mux**, and the rate-limiter bucket map is hard-bounded under an IP-cycling flood | `TestBucketMapIsBounded` |
