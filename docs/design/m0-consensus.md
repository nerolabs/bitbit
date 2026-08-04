# M0 fix — objective fork-choice + sound cross-fork slashing (red-team F6 / F7)

> **Status: F6 objective fork-choice SHIPPED (2026-08-04, mechanism, opt-in) in
> `core/chain`; F7 surround-slashing still pending.** The external M0 red-team
> broke the consensus sub-suite (D2): fork-choice weight is **subjective local
> reputation**, so two honest replicas diverge permanently (F6); and cross-height
> double-backing evades the same-height-only equivocation slash (F7).
>
> The **F6 objective-weight mechanism is in code**: an on-chain PoST-bond
> registration (`Block.BondRegs`) makes proposer/attester eligibility, quorum, and
> fork-choice weight a function of the chain, not the local audit view — gated by
> `Config.MinBond` so it is additive and opt-in (default stays legacy; no
> `BlockVersion` bump). Regressions in `core/chain/redteam_consensus_test.go` show
> divergent replicas now AGREE and forks HEAL. The **cold-start is solved via the
> training-wheels anchors**: in objective mode a declared anchor is eligible to
> propose/attest WHILE the network is immature (breaking the "must be bonded to
> record bonds" chicken-and-egg), validators register their real bonds LIVE as
> they propose (`Node.RegisterBondReg`, attached in `proposeBlock`), and the anchor
> eligibility SHEDS at maturity — eligibility only, never fork-choice weight (that
> is always summed real bond). Covered unit (`core/chain/objective_coldstart_test.go`
> — anchor bootstraps then sheds) + integration (`sim/objective_coldstart_test.go`
> — an anchor-only network with empty ledgers bootstraps and validators become
> really bonded on chain). The daemon now exposes it: **`silt daemon -objective`**
> wires the verifier + live self-registration, and the **e2e tier**
> (`e2e/TestObjectiveConsensusCommitsOverTCP`) proves two objective daemons commit a
> real quorum over real TCP. **Residual:** flipping the shipped DEFAULT to objective
> (it is opt-in at the daemon today), and **F7** (Casper-FFG surround-vote
> slashing). Report: `docs/reviews/M0-REDTEAM-REPORT.md` §6/§7.
> **Depends on the bond fix** (`m0-sybil-bond.md`, shipped) — objective weight is
> only meaningful if the bond it weighs is real. **F7 folds in here** (not a
> standalone patch): a naive cross-fork slash wrongly punishes honest
> reorg-followers, which regresses the forged-slash-griefing corner that currently
> *holds* — so the sound version needs the ordering machinery F6 introduces.

## 1. What broke, precisely

### F6 — subjective fork-choice weight → two honest histories both stand

`blockWeight` (`core/chain/chain.go:613`) qualifies each attestation by the
**local, subjective reputation view**:

```go
// chain.go:628
if c.rep(id) < c.cfg.MinAttesterRep { continue }   // c.rep is a per-node closure
```

`c.rep` (set at `New`, `chain.go:256`) reads *this node's* ledger
(`credit.Reputation`, `credit.go:220`), which is built from *whichever peers this
node happened to audit*. Two honest replicas with different-but-honest ledgers
compute **different weights for the same fork**:

- R1 audited {X, Y, Z}; R2 (briefly partitioned) audited {X, Y, W}.
- A fork attested by [X, Y, W]: R1 counts weight 2 (W unqualified locally), R2
  counts 3.
- `Weight()` (`chain.go:602`) sums these; `heavier()` (`chain.go:677`) compares
  them; `Reconcile` (`chain.go:646`) adopts iff heavier. So R1 keeps fork A, R2
  keeps fork B, each `Reconcile`s the other's fork to `false`, and neither ever
  adopts the other. After "healing," `LookupRoot` (`chain.go:707`) returns
  different registries depending on which honest node you ask — reproduced
  end-to-end through `node.SyncChain` (`chainrole.go:218`).

The root cause is structural: **bond weight lives only in each replica's local
ledger** (`credit.go` `bondedBytes`), never on-chain, so "which chain is heavier"
is not a globally-recomputable quantity. `adopt`/`heavier`/`Reconcile` are all
correct *given* an objective weight — they just aren't fed one.

### F7 — cross-height double-backing evades the equivocation slash

`VerifyEquivocation` (`core/chain/equivocation.go:39`) returns false unless
`A.Height == B.Height`, and `FindEquivocations` (`equivocation.go:68`) only pairs
equal heights. An attacker who backs two mutually-exclusive forks but never signs
the **same height** on both is never implicated — e.g. propose A1 at height 1 on
fork A, sit out height 1 on fork B, sign B2 at height 2 on fork B. Combined with
F6's non-healing forks, the attacker sustains two histories and never pays.

The same-height rule is *intentionally* narrow ("sequential signing is not
equivocation"), and that narrowness is load-bearing: the forged-slash-griefing
corner **holds** precisely because slashing requires the culprit's own signatures
over genuinely-conflicting blocks. A naive "slash anyone who signed on two forks"
rule would punish an **honest validator who followed a legitimate reorg** — which
would regress a corner that currently passes. So F7 cannot be fixed in isolation;
it needs a way to tell malicious double-backing from honest reorg-following.

## 2. The fix

### 2a. F6 — fork-choice weight = objective, on-chain-recomputable PoST bond

Move fork-choice off subjective reputation and onto a weight **any node
recomputes identically from on-chain data.**

1. **Commit bond evidence on-chain.** Each attestation carries (or references) a
   verifiable PoST-bond proof — the attester's `(root, size, epoch proof)` that
   any node checks with `bond.VerifySpaceTime` (now real, per `m0-sybil-bond.md`).
   This is a **block-schema addition** → rides `Block.Version` (#98). The bond
   proof is the same one the audit loop already produces; here it is *recorded*,
   not just locally observed.
2. **Weight becomes objective.** `blockWeight` sums, over qualifying attesters,
   the **proven bond size** verified from the on-chain proof — replacing the
   `c.rep(id) >= MinAttesterRep` subjective gate. The qualification bar itself is
   now objective: an attestation counts iff its committed PoST proof verifies. Two
   honest nodes, seeing the same blocks, compute the **same** weight. Ties break
   by lower block hash (already deterministic, `chain.go:682`).
3. **Reputation keeps its *other* jobs.** Local reputation still gates spam/relay
   admission and records audit history and slashing — but it no longer decides
   *canonical fork-choice weight*. That decoupling is the whole fix: eligibility
   can stay subjective; **weight must be objective.**

`Reconcile`/`heavier`/`adopt` need no structural change — they already swap whole
derived state as a pure function of adopted blocks (`adopt`, `chain.go:699`); they
simply now compare an objective weight, so all honest nodes converge on the same
heaviest chain and the partition heals.

**This depends on the bond being real.** If the bond is still the F1/F2 leaves-only
sham, an attacker with 1/128 storage forges arbitrary on-chain weight — objective
but worthless. Hence the coupling: land `m0-sybil-bond.md` first (or together).

### 2b. F7 — adopt Casper-FFG slashing conditions (double-vote + surround)

The sound way to catch cross-height double-backing *without* punishing honest
reorg-followers is the **standard finality-gadget slashing conditions** (Casper
FFG / Tendermint) — adopt, don't invent (B8). Votes gain a `(source, target)`
structure over checkpoints, and two conditions are slashable:

1. **Double vote** — two distinct votes with the **same target height**. This is
   today's same-height rule (`equivocation.go:39`), kept as-is.
2. **Surround vote** — a vote whose `(source, target)` span **surrounds** (or is
   surrounded by) another of the culprit's votes. This is *exactly*
   cross-height double-backing: backing fork A across a span, then fork B across a
   span that surrounds or nests inside it, provably means abandoning a fork you
   were still committing to.

An **honest reorg-follower is never slashed**: following a legitimately heavier
fork produces sequential, *non-surrounding* votes (each new vote's source is the
prior justified checkpoint). Only a validator committing to two live, competing
spans surrounds — and the surround relation is a compact, self-verifying proof
built from the culprit's own two signatures, so forged-slash griefing still can't
fabricate it. That is why F7 rides on F6: the `(source, target)` checkpoint
structure and the notion of a "justified" ancestor come from the objective
finality machinery 2a introduces. `slashEquivocators` (`chainrole.go:197`) already
derives slashing from two chains locally during `Reconcile`, so evidence can use
ancestry — the change is *which* relation counts (add surround), not where it runs.

Ancestry: F7 detection needs to walk `Prev` (`Block.Prev`, `chain.go:92`) to
establish the `(source, target)` spans and the surround relation. No such
walk-back API exists today (validation only checks `b.Prev == Head()`); add a
minimal ancestor-path helper used only by the slash evidence path.

## 3. The composition, after the fix

```
each block's attestations carry committed PoST-bond proofs   (rides Block.Version)
                    │
fork-choice weight  = Σ  verify(attester's on-chain PoST proof) · provenBondSize
                       (objective — every honest node computes the SAME number)
                    │
heaviest verifiable-bond chain wins; ties by lower block hash → partition HEALS
                    │
votes carry (source, target) over checkpoints:
    double-vote  (same target)        → slash   (today's rule, kept)
    surround-vote (nested/overlapping spans) → slash   (F7 fix; spares honest reorg)
    sequential votes on one chain      → NOT slashed   (forged-griefing still denied)
```

## 4. Schema + persistence touch

- **On-chain bond commitment per attestation** and **`(source, target)` vote
  fields / surround-evidence record** — block-schema additions, all ride
  `Block.Version` (#98). Old and new eras validate under their own rules via the
  version guard.
- **Reversible reorg** already lands via `adopt` swapping whole derived state; the
  `spent`/`revoked`/`validatorsSeen` sets rebuild from adopted blocks
  (`adopt`, `chain.go:699`) — the red-team confirmed no stale-state leak on
  reorg. Objective weight makes reorgs actually *happen*, which is what the
  existing machinery was built for.
- **Ancestor-path helper** — pure function over in-memory blocks; no persistence.

## 5. Falsifiable denial + regression (invert the PoCs)

**Denial (V3):** an equivocating or off-head proposer cannot get two competing
histories to both stand; the partition heals to the heavier-**bond** fork on
*every* honest replica; provable double-signing (same-target or surround) costs
the actor standing; an honest reorg-follower is never slashed.

Invert the red-team PoCs as regression (assert DENIED):

| Red-team PoC (asserts BROKEN) | Regression (assert DENIED) |
|---|---|
| `core/chain/redteam_consensus_test.go::TestRedteamConsensusSubjectiveWeightForksBothStand` | two honest ledgers now compute the **same** objective weight; one fork wins |
| `sim/redteam_consensus_test.go::TestRedteamConsensusPartitionNeverHeals` | partition **heals** — all replicas agree on `LookupRoot` after sync |
| `core/chain/redteam_consensus_test.go::TestRedteamEquivocateAcrossHeightsUndetected` | cross-height double-backing is caught by the **surround** condition |

Plus a **must-still-hold** regression guarding the corner F7 could regress: an
honest validator that follows a legitimate reorg (sequential, non-surrounding
votes) is **not** slashed — protect the forged-slash-griefing denial.

## 6. Open risks to hand the red-team

- Does objective weight create a **grinding** vector — can a proposer choose which
  attesters' proofs to include to bias weight? (Weight must sum *all* valid
  committed proofs, not a proposer-chosen subset.)
- On a **small/young** network, can a validator capture the quorum before the
  anchor training-wheels shed (risk #15)? Bound it against the real bond cost.
- Surround-condition completeness: are there malicious double-backing patterns
  that produce **non-surrounding** vote pairs? (Casper FFG proves not, for the
  finality gadget — verify silt's vote structure matches the assumptions.)
- Does committing bond proofs per attestation bloat blocks unacceptably? (Consider
  referencing an on-chain bond registration rather than inlining full proofs.)

## 7. Build sequencing (code is the next turn — after / with the bond fix)

1. On-chain bond commitment per attestation (rides `Block.Version`); objective
   `blockWeight` from verified proofs; remove the subjective `c.rep` gate from
   *weight* (keep it for admission).
2. `(source, target)` vote structure + checkpoint notion; ancestor-path helper.
3. Surround-vote slashing condition in `equivocation.go` / `slashEquivocators`;
   keep the double-vote rule.
4. Invert the three red-team PoCs; add the honest-reorg-not-slashed guard.
5. Multi-machine field test (#52): N validators, partition + heal, kill/restart —
   converge on every replica. Whole-suite `-race` on `core/chain` + sim consensus.
