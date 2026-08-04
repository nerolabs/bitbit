# silt M0 red-team — external adversary report

> Independent adversary, per `redteam/m0-redteam-brief.md`. Target: the public
> surface only (repo `github.com/nerolabs/silt` @ `c1397e0`, built `go build
> ./cmd/silt`, Go 1.26). Every finding below is a runnable PoC — a new
> `*_test.go` added to the clone, **no source file modified**, repo builds clean.
> All PoCs were re-run and re-verified by the lead, not taken on a subagent's word.

## Bottom line

**M0 is held _iff_ the external red-team denies all three failure modes
(TENETS Part 0). All three are BROKEN by shipped code.** The primitives are real
and mostly well-chosen (the VDF is textbook Wesolowski; the PoR is a faithful
Shacham–Waters scheme that I could **not** break). The breaks are in the **novel
composition** — precisely the part B8 says is the research and must be proven.
The design doc `gate4-m0-mechanism.md` claims each corner is "Resolved"; the
_code_ resolves none of the three.

## Verdict table

| Corner (§6 outcome that must fail) | Verdict | One-line |
|---|---|---|
| **Privacy** — link a published root → its standing key | 🔴 **BROKEN** | D3 issuance-mixing never shipped; a colluding validator minority de-anonymizes the publisher at token-acquisition. |
| **Accountability** — find an identity-level/global takedown switch | 🔴 **BROKEN** | On-chain revocation is a network-wide, opt-out-less, ownership-unchecked, irreversible switch. |
| **Sybil** — reach consensus/denylist influence for < N bonds | 🔴 **BROKEN** | The bond stores 1/128 of what it charges (→ 0 for small bonds); the VDF "time" half is decorative. |

Sub-suites and adjacent corners:

| Sub-claim | Verdict | One-line |
|---|---|---|
| Consensus D2 — "partition heals to the heavier fork; two histories can't both stand" | 🔴 **BROKEN** | Fork-choice weight is subjective local reputation, not objective on-chain bond; two honest replicas diverge permanently. |
| Consensus — provable double-signing costs standing | 🟠 **PARTIAL** | Same-height equivocation is slashed; cross-height double-backing of competing forks is not. |
| Liar-prover / retrieval audit (PoR) | 🟢 **DENIED** | Faithful private-verification SW scheme; no data-less pass, no partial-storage shortcut, verify-without-fetch confirmed. |
| Forged-slash griefing of an honest validator | 🟢 **DENIED** | Equivocation evidence requires the culprit's own signatures over both blocks. |
| Reorg double-spend / un-revoke | 🟢 **DENIED** | `adopt()` swaps whole derived state as a pure function of adopted blocks. |
| Operator-local denylist (voluntary, per-hash) | 🟢 **HELD** | No built-in list, no authority, per-hash only — genuinely pluralistic. |
| Storage-plane availability/integrity across real NAT | 🟢 **PASS (live)** | Docker kernel-NAT field test: bit-perfect 5 MB publish→fetch via relay. |

## Reproduce everything

```sh
cd redteam/silt
go build ./...                                   # clean
go test ./core/bond/ ./core/credit/ -run 'Sybil|Redteam' -v   # Sybil corner
go test ./sim/ -run TestRedteamIssuanceLayerLinksPublishToStanding -v   # Privacy
go test ./core/chain/ -run TestRedteamCensor -v                # Accountability
go test ./core/chain/ ./sim/ -run 'RedteamConsensus|RedteamEquivocate' -v  # Consensus
go test ./core/por/ -run RedteamPoR -v            # PoR denial
./integration/nat/run.sh                          # live cross-NAT (Docker)
```

---

## Findings

### 1. The PoST bond charges 128× the storage it actually binds (→ 0 for small bonds)

- **corner:** sybil
- **adversary:** the Sybil farm
- **severity:** critical
- **breaks denial:** §6 Sybil ("N independent identity-bound plots held across time; a farm splitting one plot fails identity-binding")
- **confidence:** high
- **attack:** `plotBlock` (`core/bond/bond.go:315`) derives each 4 KiB block from only the **32-byte leaves** of its predecessor + 3 parents, and `leaves[i] = HashBytes(block_i)` (`bond.go:127`). The block data is read by nobody but its own leaf hash. So a prover that stores **only the `leaves` array** (32 B/block) recomputes any probed block in a single `plotBlock` call — no dependency-subgraph recursion — and builds its Merkle proof from the same leaves. It passes both `Verify` and the live-path `VerifySpaceTime` (`core/node/bondaudit.go:144`) while holding `BlockSize/32 = 128×` less than the bond it advertises. For bonds **≤ ~1.1 MiB**, `Seal()` throughput (~420 MiB/s measured) exceeds the VDF window (~3 ms), so the attacker re-plots on demand and holds **0 resident bytes** between challenges.
- **outcome:** N Sybil identities cost `N·(S/128)` disk — or ~0 for small bonds — not `N·S`. Consensus/denylist standing (`credit.Reputation`) is bought for 1/128 the storage the whole Sybil-cost model assumes. The `bondstanding` sim's "honest bonded validators" premise is exactly what this defeats.
- **suggested fix:** bind the *block bytes*, not just leaves — e.g. make each block depend on the full 4 KiB predecessor/parent blocks (memory-hard label), or adopt a proven depth-robust graph (Ateniese/DRSample) and a memory-hard label function. Charging must price the resource that's actually held.
- **already-known:** no. `core/bond/bond.go:32-42` asserts the opposite ("recomputing a probed block forces recomputing its whole dependency subgraph … store the S bytes"); CHANGELOG "N distinct blobs of real storage" is likewise false against this attack.
- **PoC:** `core/bond/sybil_amortize_test.go::TestSybil_LeavesOnlyProver_PassesWithFractionalStorage`; `core/bond/redteam_sybil2_angle_a_test.go` (zero-resident crossover).

### 2. The VDF "time" half is decorative — its input is public, so it gates nothing

- **corner:** sybil
- **adversary:** the Sybil farm
- **severity:** high
- **breaks denial:** §6 Sybil / §3b ("cannot retroactively fake having held the space across an epoch"; "cannot release the space and re-plot just in time")
- **confidence:** high
- **attack:** `AnswerSpaceTime` runs the VDF over `challengeSeed(root, nonce)` (`bond.go`), which is **entirely public** (the root is gossiped on every message; the nonce is in the challenge). No held block feeds the VDF. A zero-resident prover computes the VDF, learns the VDF-derived indices, *then* re-derives exactly those blocks and passes `VerifySpaceTime`. Separately, the VDF does not scale with farm size: 10 identities' VDFs run concurrently on 10 cores (~3× speedup measured), so a farm pays ~one VDF window for its whole fleet. Raising `BondVDFDelay` is counter-productive — it *widens* the re-plot budget (at T=10000 the window fits an 11 MiB re-plot).
- **outcome:** the "space-time" reduces to "space" (already broken by Finding 1) plus a per-box constant that a farm amortizes. The sequential-work floor buys nothing because it is decoupled from possession.
- **suggested fix:** the challenge that seeds the sampling must require reading the plot *before* the VDF (Spacemesh binds the VDF to a commitment that requires the space), so releasing the space forfeits the ability to answer; and/or make re-plotting cost ≫ one epoch. The VDF is sound in isolation (I found no cheap-fake in `normalizeInput`/`hashToPrime`/range checks) — the flaw is where it sits in the composition.
- **already-known:** no. Contradicts CHANGELOG lines 284-287 and the `AnswerSpaceTime` docstring.
- **PoC:** `core/bond/redteam_sybil2_angle_c_test.go` (`VDFGatesNothing`, `FarmVDFParallel`).

### 3. Root-owner dedup adds no Sybil cost

- **corner:** sybil
- **adversary:** the Sybil farm
- **severity:** medium (amplifier)
- **breaks denial:** §6 Sybil ("strict binding, N plots for N identities")
- **confidence:** high
- **attack:** the dedup (`core/credit`) keys on **exact root-hash equality** and is **per-ledger** (each validator has its own). It fires only on byte-identical replays; N distinct-secret identities each earn full standing, and two validators independently credit the same root to different first-claimers. So identity-binding forces "N distinct roots" but never "N × S disk," and there is no global one-identity-per-root invariant.
- **outcome:** the binding the design leans on to restore `N·S` cost is a local same-root tiebreak, not a cost mechanism. Combined with Findings 1–2, per-identity cost is `S/128 → 0`.
- **suggested fix:** the cost has to live in the storage/time proof itself (Findings 1–2); dedup cannot carry it.
- **already-known:** no.
- **PoC:** `core/credit/redteam_sybil2_angle_b_test.go` (3 tests).

### 4. Publisher de-anonymized at token issuance (D3 never shipped)

- **corner:** privacy
- **adversary:** colluding validator minority / network observer
- **severity:** high
- **breaks denial:** §6 Privacy ("no adversary ties a published root to the durable standing key … issuance … layer"); §3c D3
- **confidence:** high
- **attack:** `AcquireToken` (`core/node/tokenrole.go:92`) requests blind signatures **directly from the bonded identity's own transport**, in real time, with no mix/relay, no ephemeral rotation, no epoch batching, and no enforced canonical validator set. The issuer handler receives the requester NodeID (`from`) and calls `ChargePublish(from)` (`tokenrole.go:75`), debiting the durable standing account. A colluding issuer minority logs `(requester, timing)` and correlates the near-simultaneous ephemeral publish → the standing key by IP+timing; the fee debit is a second, independent ledger-level link. The blind-signature crypto (`core/blindtoken`, RSA-FDH) is sound — it hides the *serial*, not the *requester* — so unlinkability fails because sound crypto is invoked over a non-anonymous transport. Layers 1 (Publisher-less ledger) and 2 (ephemeral publish announce) **do** hold.
- **outcome:** a colluding minority ties a published root to the standing key that paid for it — the exact linkage §3c/D3 promised to sever "at every layer an observer can watch."
- **suggested fix:** ship D3 — route issuance over the content-blind relay, batch into epochs, enforce a canonical validator set; decouple the fee (aggregate/prepaid, not per-request-timed).
- **already-known:** partially, and **understated**. CHANGELOG calls D3 "deferred" and the residual a "narrowed anonymity set"; in shipped code the anonymity set is a **singleton** (the one identity that asked), i.e. direct de-anonymization. §4 constraint 2 claims unlinkability is "Resolved by D3" — false against code.
- **PoC:** `sim/redteam_privacy_issuance_test.go::TestRedteamIssuanceLayerLinksPublishToStanding`.

### 5. On-chain revocation is a global takedown switch the design says can't exist

- **corner:** accountability
- **adversary:** the censor (quorum-capturing or quorum-coercing)
- **severity:** high
- **breaks denial:** §6 Accountability ("no identity-level or global switch exists to find"); TENETS immutable #5, Don't #2, S4, persona §9
- **confidence:** high
- **attack:** `ValidateProposal` (`core/chain/chain.go:320`) accepts a revocation block if it is merely non-empty and quorum-signed — **no ownership check, no existence check** on the revoked roots. A quorum can revoke a root it never published, a competitor's root, or a hash never on the chain. On commit, `apply` sets `revoked[r]=true` (`chain.go:529`), and **every** chain-following node honors it unconditionally: `node.isDenied` unions `chain.Revoked(root)` with **no per-operator opt-out** (`core/node/denylist.go`; the "compliant nodes" framing has no runtime flag — grep finds none). It is set-only/irreversible; the only "undo" is out-weighing the censor's quorum in a full reorg. Precondition: quorum capture — cheap given Findings 1–3; even an honest-but-coerced quorum (a jurisdiction pressuring top validators) achieves it.
- **outcome:** a takedown whose effect is universal for all chain followers, not "proportional to who trusts you" — a single kill switch (Don't #2) / one-operator-decides-globally (S4). The operator-local denylist half is genuinely voluntary and per-hash and **does** hold; the break is that a second, mandatory, global source is unioned into the same check.
- **suggested fix:** make chain-revocation honoring a per-operator subscription (restore pluralism), require the revoker to prove root ownership or the root's on-chain existence, and add a revoke/un-revoke record type; or drop the "pluralistic / never a global switch" promise for the chain path and document it honestly as quorum-governed global.
- **already-known:** no (the global/opt-out-less/ownership-unchecked nature is not flagged as a residual).
- **PoC:** `core/chain/redteam_censor_test.go` (`GlobalRevocationSwitch`, `ContrastVoluntaryVsChain`).

### 6. Subjective fork-choice weight → two honest histories both stand permanently

- **corner:** sybil (consensus sub-suite / D2)
- **adversary:** equivocating / off-head proposer (no malice even required)
- **severity:** high
- **breaks denial:** §3e / §6 consensus ("an equivocating or off-head proposer cannot get two competing histories to both stand; the partition heals to the heavier fork")
- **confidence:** high
- **attack:** fork-choice `blockWeight` (`chain.go:561`) qualifies each attestation by `c.rep(id) >= MinAttesterRep` — the **local, subjective reputation view** — not the objective on-chain PoST-bond weight §3e/Constraint 3 require. Two honest replicas with different-but-honest ledgers (the daemon reality: each validator audits a different subset of peers during a partition) compute different weights: R1 sees fork A as heavier, R2 sees fork B as heavier. Each `Reconcile`s the other's fork to `false` and keeps its own. After "healing," `LookupRoot` returns different registries depending on which honest replica you ask — reproduced end-to-end through `node.SyncChain`.
- **outcome:** two competing histories both stand; the partition never heals; the registry is non-deterministic across honest nodes. An off-head proposer only has to seed two forks and let subjective drift persist.
- **suggested fix:** implement D2 as specified — fork-choice weight = objective, on-chain-recomputable PoST bond of the committing attesters, so "which chain wins" is globally agreed. (Note this depends on the bond being real — Findings 1–2 — so the two fixes are coupled.)
- **already-known:** partially. CHANGELOG (≈lines 93-97, 246-248) admits fork-choice is "still the locally-qualified reputation view [that] can diverge under an adversarial partition" and labels objective bond weight "recorded D2 hardening" — but §3e/§6 still state the denial as **held**, and `sim/reorg_test.go` masks the gap by using a single shared ledger. This PoC converts the acknowledged gap into a concrete break of the stated denial.
- **PoC:** `core/chain/redteam_consensus_test.go::TestRedteamConsensusSubjectiveWeightForksBothStand`; `sim/redteam_consensus_test.go::TestRedteamConsensusPartitionNeverHeals`.

### 7. Cross-height double-backing evades equivocation detection

- **corner:** sybil (consensus sub-suite)
- **adversary:** colluding proposer with standing on two forks
- **severity:** medium
- **breaks denial:** §6 consensus ("provable double-signing slashes")
- **confidence:** high
- **attack:** `VerifyEquivocation` returns false unless `A.Height == B.Height` (`core/chain/equivocation.go:39`) and `FindEquivocations` only pairs equal heights. An attacker who backs two mutually-exclusive forks but never signs the *same* height on both is never implicated — e.g. propose A1 at height 1 on fork A, sit out height 1 on fork B, propose B2 at height 2 on fork B.
- **outcome:** combined with Finding 6's non-healing forks, an attacker sustains two competing histories and never pays the equivocation slash.
- **suggested fix:** treat signing across incompatible forks (conflicting `Prev` ancestry), not only same-height collisions, as slashable.
- **already-known:** the same-height rule is intentional ("sequential signing is not equivocation"); that cross-height double-*backing* is a distinct unslashed misbehavior is not acknowledged.
- **PoC:** `core/chain/redteam_consensus_test.go::TestRedteamEquivocateAcrossHeightsUndetected`.

## Denials worth recording (independently re-derived — report per RoE)

- **Retrieval audit (PoR) is sound.** `core/por` is a faithful Shacham–Waters private-verification scheme: each authenticator covers **every sector** of its block, so the leaf-vs-block shortcut that kills the bond has no analogue here. A data-less prover fails; 2000 key-less `μ`-forgeries all rejected; the `n.liar` path fails every challenge; verify-without-fetch confirmed (the only `store.Get` in the audit path is the prover reading its own copy). The self-disclosed tail-shard "option B" residue is not a data-less bypass. This is the corner the composition got *right* — and the direct contrast that proves the bond (Findings 1–2) got it wrong. PoC: `core/por/redteam_por_forge_test.go` (5 tests).
- **Forged-slash griefing:** denied — evidence requires the culprit's signatures over both conflicting blocks.
- **Reorg double-spend / un-revoke:** denied — `adopt()` swaps `byRoot`/`spent`/`revoked`/`validatorsSeen` as a pure function of adopted blocks (so a reorg leaves no stale spent-serial) — *when* a reorg happens, which Finding 6 shows it often won't.
- **Live cross-NAT availability:** PASS — `integration/nat/run.sh` moved a 5 MB file bit-perfect between two kernel-NATed daemons via the relay.

---

## Notes to the builder (efficiency + doc truth)

You asked what would have made this pass more efficient, and to flag doc staleness/falsities. Both below.

### What cost me time / would have helped up front

1. **The design doc is stale in the dangerous direction.** `gate4-m0-mechanism.md` is labelled "**design doc, ahead of code**," but the code has since *overtaken* it — the VDF, the real PoR, `Reconcile`, and equivocation all exist now. So the doc reads as "not built yet" for things that _are_ built, and simultaneously claims "**Resolved by D2/D3**" (§4) for things that are **not** resolved in code (issuance mixing, objective fork-choice weight). I burned real time reconciling "is this the plan or the reality?" A one-line status per §3 subsection ("shipped / partial / planned, as of commit X") would have saved a full pass. Right now the fastest way to know what's real is to read the code, which defeats the doc.
2. **I didn't know Docker was available until you told me.** The brief points at `integration/nat` and `test-topologies.md`, but nothing said "Docker is installed and the harness runs green on this box." Knowing that on minute one, I'd have launched the cross-NAT field test first (it's the highest-value live artifact and takes ~1 min). Suggest the brief's "don't burn budget on setup" section state the host's capabilities explicitly: Docker yes/no, expected `run.sh` runtime, and that it self-cleans.
3. **`examples/` is excellent — lead with it harder.** `flow4-earned-standing.sh` and the others booted a real swarm, printed clear PASS/FAIL, and killed only their own PIDs — exactly what a red-team wants. The `silt id` / `silt chain-status` helpers are the right primitives. These deserve top billing in the brief as *the* way to get a live network in one command; I found them only because I went looking.
4. **The pre-wired sims are the ideal starting shape and underspecified in the brief.** `sim run bondstanding|consensus|audit|takedown` each encode a denial — extending them was the fastest route to a live attack. But two of them quietly self-report residuals that read like passes: `sim run audit` prints "**only 4 of 6 liars caught**," and `bondstanding` asserts Sybil denial *while assuming the bonded validators honestly hold their storage* — the exact premise Finding 1 destroys. Worth a sentence in the brief: "these sims prove denial *given honest primitives*; your job is the primitives."
5. **White-box test seams were already there** (`n.liar`, exported `manifest.Prove`/`MerkleRoot`, `vdf.Eval`, per-package `*_test.go` access). That made PoCs cheap to write. No change needed — just noting it worked.

### Documentation staleness / falsities (independent of the code bugs)

These are places the docs assert something the code contradicts — each is a doc-truth defect even where you choose not to fix the code:

- **`core/bond/bond.go:32-42`** — "recomputing a single probed block on demand forces recomputing its whole dependency subgraph … the rational strategy is to STORE the S bytes." **False:** the dependencies are the 32-byte leaves; storing leaves (1/128) defeats it. This is the load-bearing claim of the whole Sybil corner and it's wrong.
- **CHANGELOG "cannot release the space and re-plot" / `AnswerSpaceTime` docstring** — **false**; the VDF input is public, so a zero-resident prover re-plots after the delay (Finding 2).
- **CHANGELOG "N distinct blobs of real storage"** — misleading; the real cost is `S/128 → 0`.
- **`gate4-m0-mechanism.md` §4 "Resolved by D3" (unlinkability) and "Resolved by D2" (partition/fork-choice)** — **false against code**: D3 issuance-mixing is unshipped; fork-choice weight is still subjective reputation. §6's Privacy and Consensus denials are presented as met; neither is.
- **`gate4-m0-mechanism.md` §6 verdict framing** — the table presents all three denials as the target; a reader could infer they hold. None do in shipped code. Recommend the doc carry the live verdict, not just the target.
- **"Compliant nodes" (chain-revocation docs, CHANGELOG ≈line 1036, `chainrole.go` ProposeRevocation doc)** — implies opt-in; there is **no** runtime opt-out flag. Either add one or drop the "pluralistic / never a global switch" language for the chain path.
- **CHANGELOG privacy "residual … narrows the anonymity set"** — understates: with no epoch batching and no canonical set enforced, the set is a singleton (direct de-anonymization).
- **Internal inconsistency on consensus** — CHANGELOG honestly admits subjective fork-choice "can diverge under an adversarial partition," while §3e/§6 state "partition heals to the heavier fork" as held. The two docs disagree; the code sides with the CHANGELOG's admission.

### Net recommendation

M0's mission-immutable ("held iff the external suite denies all three") is currently **not met**: privacy, accountability, and Sybil are each broken at the mechanism level, and the consensus sub-suite with them. The good news is that none of the breaks are in the adopted primitives (VDF and PoR are solid) — they are all in the **composition**, which is fixable without swapping crypto: bind the bond to bytes-held and to a pre-VDF plot read (Findings 1–2), ship D3 (Finding 4), gate revocation on ownership + per-operator subscription (Finding 5), and make fork-choice weight objective (Finding 6). Until then, the honest status line is: **primitives real, composition unproven, M0 not yet held.**

## Appendix — PoC files added (no source modified)

```
core/bond/sybil_amortize_test.go              # Finding 1 (leaves-only 128×)
core/bond/redteam_sybil2_angle_a_test.go      # Finding 1 (zero-resident re-plot)
core/bond/redteam_sybil2_angle_c_test.go      # Finding 2 (VDF decorative)
core/credit/redteam_sybil2_angle_b_test.go    # Finding 3 (dedup no-cost)
sim/redteam_privacy_issuance_test.go          # Finding 4 (issuance de-anon)
core/chain/redteam_censor_test.go             # Finding 5 (global revocation)
core/chain/redteam_consensus_test.go          # Findings 6, 7 (fork-choice, cross-height)
sim/redteam_consensus_test.go                 # Finding 6 (end-to-end non-healing)
core/por/redteam_por_forge_test.go            # PoR denial (5 tests)
```
