# M0 red-team — builder response + verification guide

> **For the next reviewer.** The independent M0 red-team
> (`docs/reviews/M0-REDTEAM-REPORT.md`) broke all three corners in the
> *composition* (the primitives held). This document is the builder's response:
> for every finding **F1–F7**, the fix, the PR, and **exactly how to verify it** —
> the inverted PoC regression to run, and the live daemon flag where one exists.
>
> **Honest status line:** every composition finding now has a shipped fix with
> unit + integration coverage (and real-TCP e2e where a daemon surface exists).
> **This is not a self-certification that M0 is held.** M0 is held only when a
> fresh external red-team denies all three failure modes (TENETS Part 0). This
> guide exists to make that re-verification fast.
>
> **Fix-verification pass (2026-08-04):** the original red-team re-ran its seven
> PoCs against the fixed `main` and confirmed 1/2/3/5 solidly fixed and PoR still
> denied, but flagged that **#6/#7 (consensus) and #4 (privacy) were fixed but not
> the DEFAULT path** — a stock validator ran legacy subjective fork-choice, and
> the default publish still used the legacy fee-charge. **Both are now closed:**
> objective fork-choice is the default for an untrusted validator (`-objective`
> defaults true; legacy is an explicit `-objective=false` opt-out), and the
> default `-token-quorum` publish uses the prepaid-credit path. See the per-finding
> "Now the default" notes below.

## Reproduce everything (the inverted regressions)

Each was the red-team's own PoC, inverted to assert the attack is now DENIED.

```sh
go build ./...                                          # clean

# Sybil (F1/F2/F3)
go test ./core/bond/  -run 'Redteam|Sybil|Plot|SpaceTime'   -v   # F1/F2 crypto
go test ./sim/        -run 'ReleasedBond|BondFloor'         -v   # F1/F2 over the wire + floor
go test ./core/node/  -run 'BondAntiReleaseFloor'          -v   # floor unit

# Privacy (F4)
go test ./core/node/  -run 'RedteamF4'                     -v   # fee decoupling + domains + double-spend
go test ./sim/        -run 'PrepaidCredit'                 -v   # fee decoupling over the wire
go test ./core/chain/ -run 'CanonicalIssuers'              -v   # canonical issuer set

# Accountability (F5)
go test ./core/chain/ -run 'F5'                            -v   # ownership/existence/reversibility
go test ./core/node/  -run 'F5'                            -v   # per-operator honoring (isDenied)
go test ./sim/        -run 'F5Revocation'                  -v   # per-operator over the node loop

# Consensus (F6/F7)
go test ./core/chain/ -run 'RedteamF6|CanonicalIssuers|ObjectiveLaunchAnchor|F7'  -v
go test ./sim/        -run 'Objective'                     -v   # heal + cold-start over the loop
go test ./core/node/  -run 'RegisterBondReg'              -v   # live registration

# E2E over real TCP (skipped under -short; CI runs the full form)
go test ./e2e/ -run 'ObjectiveConsensusCommitsOverTCP|ChainRevocationCommitsOverTCP|BondEarnedStandingCommitsOverTCP' -v

# The whole suite + the primitives the red-team could NOT break
go test ./...                                             # all green
go test ./core/por/ -run 'Forgery|Tampered|Deleted|WrongKey' -v   # PoR still sound
```

CI gates all of this on every PR: `go test` (unit + sim), the multi-process e2e
job, and the Docker cross-NAT job.

---

## F1 / F2 / F3 — Sybil bond

**Break (report §1–§3):** the plot bound only the 32-byte block *leaves*, so a
prover held 1/128 of the charged bytes (→0 for small bonds, re-plotted inside the
VDF window); the VDF input was public, so "time" gated nothing; root-owner dedup
added no cost.

**Fix:**
- **F1** — `plotBlock` (`core/bond/bond.go`) now derives each block from the
  **full bytes** of its predecessor and its **DRSample depth-robust-graph**
  parents (Alwen–Blocki–Harsha, CCS'17). Recomputing a block needs the parents'
  bytes recursively → pebbling cost Ω(n) → storing the S bytes is rational; charged
  size == resident footprint. `Verify` never recomputes a block (stays O(log n)).
- **F2** — `AnswerSpaceTime` seeds the VDF from a plot block **read before the
  VDF** (`seedIndex` → `challengeSeedST`); the answer carries that block + its
  inclusion proof, so a released-space prover cannot produce the seed without the
  Ω(n) recompute.
- **F3** — root-owner dedup is documented as a same-root tiebreak only; cost lives
  in the byte-bound proof; distinct identities produce distinct plots.
- **Anti-release floor** — `Node.MinBondBytes` / `silt daemon -min-bond-floor`
  denies standing to a bond small enough to release + re-plot inside the challenge
  window (floor ≥ window × plot-throughput; ~135 MiB at a 500 ms window / 270 MB/s).

**PRs:** #139 (F1/F2 + inverted PoCs), #144 (integration), #149 (floor).
**Verify:** `core/bond/redteam_sybil_test.go` (leaves-only fails, released-plot
fails, domains bound); `sim/bond_release_test.go` (a released bond earns zero
standing over the live audit wire); `core/node/bondfloor_test.go` +
`sim/bond_floor_test.go` (sub-floor bond earns nothing). Live: two `-bond` daemons
prove bonds to each other over real TCP (`e2e/TestBondEarnedStandingCommitsOverTCP`).
**Residual:** the floor VALUE is a deployment knob (default off; every fast
test/demo/NAT config uses tiny bonds).

## F4 — Publisher de-anonymized at token issuance

**Break (report §4):** `AcquireToken` requested blind signatures from the bonded
identity's transport and `ChargePublish(from)` debited the durable standing
account — de-anon by IP+timing and by the fee.

**Fix (the strong links are severed):**
- **Durable NodeID** never leaks: the CLI `swarm add` publish client runs from a
  fresh **ephemeral identity** (`identity.Generate` + `SetEphemeral`), so the
  issuer never sees the durable NodeID (the report's durable link was the sim-API
  path, not the CLI).
- **Fee** link severed by **prepaid publish credits** (online Chaumian e-cash,
  `core/blindtoken` domain-separated so a credit ≠ a token): the fee is charged in
  **bulk at mint**; a publish spends a credit with **no per-publish debit**. **Now
  wired into the default publish** — `cmd/silt/swarm.go`'s `acquirePublishToken`
  mints a credit per validator then spends them, so a default `-token-quorum`
  publish no longer hits `ChargePublish(from)` (re-verification #4 closed;
  `e2e/TestUnlinkablePublishOverTCP` exercises it).
- **Subset-choice** channel severed by a **canonical on-chain issuer set**
  (`Chain.CanonicalIssuers`, from the objective bond) — every publisher asks the
  same validators.

**PRs:** #141 (credits), #142 (canonical), #145 (integration).
**Verify:** `core/node/redteam_privacy_test.go` (mint charges once, publish
charges nothing more, double-spend + forgery refused, credit/token domains
distinct); `sim/credit_fee_test.go` (fee decoupling over the wire — the durable
balance is unchanged by a credit-backed publish); `core/node` /
`core/chain/redteam_consensus_test.go` canonical-set determinism.
**Residual (deliberately deferred — option B):** the **IP + timing** link for
*public-IP* clients (NATed clients already route issuance through the relay, so
their IP is already hidden from the issuer). Closing it fully = relay-forced
issuance + epoch batching; a refinement, honestly recorded in
`docs/design/m0-privacy-issuance.md` §2a–2b. **A fresh red-team should probe
whether this residual is material.**

## F5 — On-chain revocation was a global takedown switch

**Break (report §5):** a quorum could revoke any root (no ownership/existence
check), every chain-follower honored it (no opt-out), and it was irreversible.

**Fix:**
- **Existence-checked** — `ValidateProposal`/commit reject a revocation of a root
  not committed on this chain (`ErrRevokeUnknownRoot`).
- **Per-operator opt-in** — honoring on-chain revocations is a subscription
  (`SetHonorChainRevocations` / `silt daemon -honor-chain-revocations`, default
  **off**); the operator-local denylist is always honored.
- **Reversible** — `Block.Unrevocations` (quorum-gated) clears a takedown.

**PRs:** #136 (fix), #146 (integration + `Node.WouldDeny`), #151 (daemon `-revoke`
/ `-honor-chain-revocations` + e2e).
**Verify:** `core/chain/redteam_f5_accountability_test.go` (ownership/existence);
`core/node/redteam_f5_subscription_test.go` (per-operator `isDenied`);
`sim/revocation_test.go` (a subscribing node denies a quorum-revoked root, a node
on the identical chain that did not subscribe does not; unknown-root revoke
refused). Live: `e2e/TestChainRevocationCommitsOverTCP` (a validator drives a
quorum takedown over real TCP).

## F6 — Subjective fork-choice → two honest histories both stand

**Break (report §6):** fork-choice weight used the local reputation view, so two
honest replicas with different audited sets diverged permanently.

**Fix:** fork-choice weight, quorum, and eligibility are now a function of
**on-chain PoST-bond registrations** (`Block.BondRegs`, verified against the real
space-time primitive) — identical on every replica, so divergent local views
can't disagree and a lighter fork reorgs onto the heavier one everywhere. The
**cold-start** is solved by the training-wheels anchors (eligible while immature,
shed at maturity; eligibility never weight), with validators registering their
real bonds live as they propose. Enabled by `silt daemon -objective`.

**PRs:** #140 (mechanism), #143 (node wiring + integration), #147 (cold-start),
#148 (daemon flag + e2e).
**Verify:** `core/chain/redteam_consensus_test.go` (maximally-divergent replicas
compute the same weight; a partition heals; forged registration denied);
`sim/objective_consensus_test.go` (heal with a **separate empty ledger per node**);
`sim/objective_coldstart_test.go` + `core/chain/objective_coldstart_test.go`
(anchor bootstrap then shed). Live: `e2e/TestObjectiveConsensusCommitsOverTCP`
(two `-objective` daemons commit a real quorum over real TCP).
**Now the default** (re-verification #6/#7 closed): `silt daemon -objective`
defaults to `true` for any untrusted validator (`-min-rep > 0`); a trusted swarm
(`-min-rep 0`) auto-disables it, and legacy is an explicit `-objective=false`
opt-out. A multi-validator quorum bootstraps from the declared launch `-anchors`
(the honest trustless-cold-start boundary — declaring the launch validator set is
the remaining operational requirement, not a code gap). So "two histories both
stand" is no longer reachable with stock validator flags.

## F7 — Cross-height double-backing evades the equivocation slash

**Break (report §7):** a validator signing two incompatible forks but never the
same height on both evaded the same-height-only slash.

**Resolution (analysis, not a new slashing rule):** worked through and locked in
`core/chain/redteam_f7_test.go`:
1. Same-height double-signing **is** slashed (`FindEquivocations`).
2. Cross-height double-backing is **provably indistinguishable from an honest
   reorg-follow** from the blocks alone (attest A@1, then follow a heavier fork to
   attest B@2 → identical evidence), so any rule slashing it would slash honest
   validators — a regression. Detection correctly does not flag it (**the guard**).
3. **F6 neutralizes it** — the double-backer cannot make both histories stand.

Casper-FFG surround-slashing was the pre-F6 plan; the analysis shows it is
unnecessary (F6 neutralizes) and, for this exact pattern, ineffective (the spans
do not surround), so no finality gadget is added for M0. **A reviewer who
disagrees should try to construct a cross-height double-back that (a) F6 does not
neutralize and (b) is distinguishable from an honest reorg — that is the open
question.** See `docs/design/m0-consensus.md` §2b.

---

## Denials the red-team recorded (still hold)

Independently re-verified and unchanged: PoR is sound
(`go test ./core/por/ -run 'Forgery|Tampered|Deleted'`); forged-slash griefing denied; reorg
double-spend / un-revoke denied; live cross-NAT availability
(`./integration/nat/run.sh`).

## What a fresh reviewer should focus on

1. **F4 IP+timing residual** — is de-anonymizing a *public-IP* publisher at
   issuance material, given the ephemeral identity + prepaid credit + canonical
   set already sever NodeID, fee, and subset-choice, and NATed clients already
   relay?
2. **F7 open question** — a distinguishable, F6-surviving cross-height double-back
   (above), if one exists.
3. **Objective-mode default** — the mechanism + cold-start + e2e are shipped, but
   the *default* is still legacy; the public anchor set is the launch decision.
4. **Parameter floors** — the anti-release `-min-bond-floor` and `BondVDFDelay`
   are knobs; are the recommended values right for a real deployment's window?
