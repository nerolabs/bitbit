# M0 Sybil — rebinding the storage bond to identity and size (G2)

**Status: BUILT (the S1 storage-bond as-built), awaiting external re-verification.**
This note is the as-built detail for **one axis** of the M0 composition — **D,
distinct sealed disk per identity**. Read [`m0.md`](m0.md) first for the systemic
claim the bond serves; the bond is *not* "the Sybil corner," it is one
non-substitutable factor in `C_honest` (disk × address-diversity × time × served
demand). The construction below shipped in `core/bond` v3 with unit + integration +
e2e coverage and the red-team PoC inverted as a regression
(`core/bond/redteam_g2_test.go`, `sim/bond_sybil_g2_test.go`).

> **How this fits the systemic target.** This primitive was broken twice as a
> *standalone* Sybil defense — a shared-plot Sybil (F1, fixed by per-root dedup) and
> then, over the fix code, **prefix plots** (G2, which dedup structurally cannot
> catch). That is *expected*: no single primitive is Sybil-proof under free identity
> + no permanent center (Douceur), so a primitive failing a standalone Sybil-proof
> test is not an M0 failure. The bond's job is to make axis D real. M0 holds only if
> the *composition* satisfies C1 + C2 and survives the §7 seams under external
> re-verification — not when "the Sybil corner is denied." The carried open risks in
> §8 (tight `ε→k`, on-chain proof size / asymmetric-`k`) are targets for that pass.

**This is the S1 (storage-bond) as-built detail** behind the systemic M0 spec —
read [`m0.md`](m0.md) first for the composition claim this bond serves as one
axis (D — distinct sealed disk per identity). The earlier finding-by-finding
design notes (the F1/F2/F3 structural pass and the original Gate-4 mechanism
spine) are now historical in
[`/archive/design-history/`](../../archive/design-history/).

---

## 1. The break (G2)

One physical plot backs N independent validator standings, so one disk buys a quorum.

It is a conjunction of three facts, each in the code today, each necessary:

**(a) The plot is not size-bound.** `plotBlock(secret, i, blocks)` and
`parentIndices(secret, i)` depend only on `(secret, i)` — **neither reads `n`**, the
total block count. So for any `m ≤ n`, blocks `0..m-1` of an `n`-block plot are
**byte-identical** to a standalone `m`-block plot. Each prefix length has its own
distinct Merkle root, and `verifyAt` checks `Total == NumBlocks(size)`, so the
`m`-leaf root is *honestly* committed to size `m`. Every prefix is a real, distinct,
valid bond living inside the one physical plot.

**(b) There is no labeling check — this is proof-of-storage, not proof-of-space.**
`Verify` / `VerifySpaceTime` / `verifyAt` check only **Merkle inclusion** of sampled
blocks (plus, for space-time, the VDF and the seed-block inclusion). They **never
recompute a label** — never check `block[i] == plotBlock(secret, i, parents)`. So the
verifier cannot distinguish a correctly-plotted DRSample dataset from *arbitrary
committed bytes of the right size*, nor tell which **size** the bytes were plotted
for. All the depth-robust labeling in `plotBlock` is invisible at verification time:
it raises the honest prover's cost and buys **zero** soundness.

**(c) Identity binding is asserted, never verified.** The plot is sealed from a
*private* per-key seed (`bondSecret`), but no verification path checks that a root
came from a given identity's seed. On-chain, `validateBondRegs` verifies an ed25519
signature over `(root, size, nonce)` — binding the identity to **a claim about a root
the attacker chose**, not to the root's provenance. The root is a free variable.

**Quantified.** Seal one `N`-block plot; register fresh keys on distinct prefix roots
`m_1 < m_2 < … ≤ N`. Each passes: its own signature (c), the size floor if
`m_k ≥ MinBond`, `verifyBond` (b — the attacker holds the blocks), and per-root dedup
(a — the roots differ). Standings ≈ `N − MinBond/BlockSize`; **marginal disk cost of
one more Sybil is one 4 KiB block.** With `MinBond = 1M`, a 1 GB plot yields ≈262k
standings. The `MinBondBytes` floor does not help — it targets release-and-replot and
merely sets the smallest admissible prefix.

The package doc's own hedge is exactly right: *"this is NOT a proof of CORRECT
plotting … a verifier still trusts the advertised root."* **That sentence is the
vulnerability.**

### Why the existing defenses can't be stretched to cover it
- **Per-root dedup (`bondRootOwner`, F1)** keys on *equal* roots; the prefix family
  manufactures *distinct* roots. Structurally unable to help.
- **Anti-release floor (`MinBondBytes`, G4)** targets a different corner (time /
  release-and-replot). Orthogonal.
- **Bond TTL (`BondTTLBlocks`, G4)** doesn't bite: the attacker genuinely still holds
  the (one) plot and can renew every prefix.

---

## 2. The construction

Adopted, not invented: graph-labeling proof-of-space (Dziembowski–Faust–Kolmogorov–
Pietrzak, CRYPTO'15) over the depth-robust DRSample graph silt already ships
(Alwen–Blocki–Harsha, CCS'17), with identity and size folded into the label. Novelty
is only in composition — which tenet B8 permits **if externally red-teamed**.

**Two changes that must ship together (see §5).**

**(i) Public, identity- and size-bound seed.**
```
secret = H("silt/bond/plot/v3" ‖ pk ‖ uint64(n))     // was: private bondSecret(signer)
plotBlock(secret, i, …)      // n folded in
parentIndices(secret, i, n)  // n folded in ⇒ a prefix is not a valid smaller plot
```
`pk` is the validator's ed25519 public key; `n = NumBlocks(size)`.

**(ii) A labeling-consistency challenge.** After the VDF completes,
`vdfDerivedNonce` selects `k` interior nodes (domain-separated from
`challengeIndices`). For each, the prover opens the node **and its immediate
predecessor and its `plotParents` DRSample parents**, each with a Merkle proof; the
verifier **recomputes** `plotBlock(H(pk, n), v, parents)` from the opened parent
*bytes* and requires it to equal the opened node.

The verifier can do this **without holding the plot** — precisely because the seed is
now public. That is the whole point: "valid block" becomes a **recomputable public
predicate**, so reused bytes are rejectable.

**Why this kills the break:** a plot sealed for `pk_A` at size `n_A` produces labels
that **fail recomputation** when claimed by `pk_B` or at size `n_B`. Identity and size
become **checked properties of the plot**, not claimed ones. N standings require N
plots — the B1 invariant is restored, and the amplification factor collapses from
`(N − M + 1)` to `1`.

### What is NOT sufficient (rejected alternatives)
| Option | Why it fails |
|---|---|
| Fold `n` in, no labeling check | Attacker commits the size-`N` prefix bytes under an `M`-leaf root; the verifier never recomputes, so never notices. **`n`-in-label and the labeling check are a matched pair — neither works alone.** |
| Identity-salted Merkle leaves / challenge derivation | Changes *which* blocks are opened and *what the root is*, never *what a valid block is*. Reused bytes still open correctly. Fine as extra domain separation; **not binding.** |
| Bind size via Merkle `Total` only | Already present, already bypassed — the attacker isn't lying about size; each prefix genuinely *is* size `m`. |
| Dedup harder | Only dedups *equal* roots (see §1). |

**A plot-format bump (v2→v3) is unavoidable for a verifiable fix.** Every cheaper
option leaves the verifier accepting bytes it cannot regenerate. Both changes alter
the sealed bytes, so a fleet re-plot is required.

---

## 3. Soundness and parameters

DRSample is, whp, `(Ω(N/log N), Ω(N))`-depth-robust at indegree 2; silt uses indegree
`1 + plotParents = 4`, which only strengthens it. A prover storing a `(1−ε)` fraction
leaves `εN` labels it must recompute on demand, and depth-robustness makes recomputing
a random missing label cost `Ω(N)` sequential pebbling — which the read-bound VDF
window forbids (§4).

Each labeling open hits a not-cheaply-answerable node with probability `≥ ε`, so `k`
independent opens catch an `ε`-short prover with probability `≥ 1 − (1−ε)^k`:

> **`k ≥ λ · ln 2 / ε`** for soundness error `2^{-λ}`.

| space cheat `ε` | `k` for `2^{-40}` |
|---|---|
| 1% | ≈ 2,773 |
| 10% | ≈ 277 |
| 30% | ≈ 78 |

**Recommended default `k = 64`** (catches a ~30% cheat at ≈`2^{-33}`, a ~50% cheat at
`2^{-64}`), as a per-network *evolving* knob.

Note the existing `Samples = 20` possession opens catch a prover **missing** blocks,
not one **recomputing** them. Different threat: the labeling opens are additive, not a
substitute.

---

## 4. Anti-release interaction (unchanged, but strengthened)

The floor stays `MinBondBytes ≳ c · B · W` (`c ≥ 2`), where `B` = plot throughput and
`W` = challenge window: a released plot must not be re-plottable inside one window. At
~270 MB/s and a ~2 s window that is ~540 MiB, hence the shipped 1 GiB default.

Two notes: raising `BondVDFDelay` **widens `W` and lowers the required floor** (more
sequential time ⇒ less disk needed to make re-plot lose the race); and the labeling
check **strengthens** the time argument, since re-deriving a labeling-challenged node
now costs full depth-robust pebbling `Ω(n)`, not a single block read.

---

## 5. Ordering constraints (both load-bearing)

**(a) The public seed must NOT land before the labeling check.** Publicising the seed
without the labeling check is a *regression*: it lets an outsider precompute a
victim's root, which the private `bondSecret` prevented. They ship in **one PR**.

**(b) The public seed is safe from griefing *only because* three legs hold**, and the
first is already merged:
1. **Genesis squat** on a now-computable root → displaced by **"proof beats
   declaration"** (`bondRootProven`, the G3 fix, `core/chain/chain.go`). **G3 is
   load-bearing for G2's safety.**
2. **Proven squat under the attacker's own key** → fails `verifyBond`, because the
   verifier recomputes labels from `H(attacker_pk, n)` and victim-sealed bytes don't
   match.
3. **Registering as the victim** → requires the victim's ed25519 private key.

---

## 6. Wire format and cost

**`Answer` gains one group** (`core/bond`):
```go
LabelIndices []int         // derived from vdfDerivedNonce — unknowable until the VDF completes
LabelBundles []LabelOpen

type LabelOpen struct {
    Node    []byte;   NodeProof    manifest.Proof
    Pred    []byte;   PredProof    manifest.Proof        // blocks[v-1]; omitted for v==0
    Parents [][]byte; ParentProofs []manifest.Proof      // blocks[parentIndices(secret,v,n)]
}
```

**`verifyBond` gains the validator key** so it can recompute `H(pk, n)`. No new
`BondReg` field — `chain.validateBondRegs` already holds `r.Validator` and `r.Size`.

**Verifier work:** `k` label recomputations + `5k` Merkle-path checks + the unchanged
VDF verify — `O((Samples + 5k)·log n)` hashing, **no fetch, no VDF re-run**.

**Proof size** ≈ `k · 5 · (BlockSize + |proof|)`. At `k = 64`, `n = 2^18`: **≈1.5 MB**
— fine over the wire, **heavy on-chain**. Mitigation: **asymmetric `k`** — a large `k`
(~64) for the live `bondAuditOnce` (frequent, cheap bandwidth) and a small `k` (~8–12)
for on-chain `validateBondRegs`, with soundness accumulating across epochs. **See the
open risk in §8.2 before relying on this.**

Keep the ed25519 signature, size floors, `bondRootOwner` dedup, and `BondTTLBlocks`
decay exactly as-is. Under verified identity binding the dedup is provably redundant —
**keep it anyway** as cheap defence-in-depth against a labeling-check regression.

---

## 7. Build sequence

Every step ships unit + integration + e2e coverage, with the red-team PoC inverted as
a regression (build-immutable).

1. **`core/bond` v3** — public `H(pk, n)` seed; `n` folded into `plotBlock` and
   `parentIndices`; `Seal`/`AnswerSpaceTime` emit label bundles; `VerifySpaceTime`
   recomputes labels. **Seed + labeling check in ONE PR** (§5a). Plot format v2→v3.
   *Falsifiable denial:* invert `G2_prefix_plot_sybil_test.go` — N prefix roots must
   yield **1** standing, and a plot sealed for `pk_A` must fail when claimed by `pk_B`.
2. **`core/node`** — `bondSecret` becomes the public `H(pk, n)`; plot-store version
   guard forces the re-plot; `RegisterBondReg` carries the bundles.
3. **`core/chain`** — thread `r.Validator` into `verifyBond`; pick the on-chain `k`.
4. **`cmd/silt`** — re-plot migration path + operator messaging.
5. **Docs/site** — reconcile `bond.go`'s "NOT a proof of CORRECT plotting" hedge,
   which this change finally retires.

---

## 8. Open risks (carried, not resolved)

1. **Tight `ε → k`.** The closed-form `k ≥ λ ln2/ε` treats each open as an independent
   `ε`-Bernoulli. The true catch probability through indegree-4 DRSample's `(e,d)`
   parameters and parent-correlation needs the DFKP'15 / Fisch'19 pebbling reduction
   instantiated for silt's exact graph. **External derivation required (B8) before
   fixing production `k`.**
2. **On-chain proof size — and a dependency (RESOLVED, H2).** The asymmetric-`k`
   mitigation leans on on-chain standing also decaying via `BondTTLBlocks` plus continuous
   live re-audit. This was blocked because bond renewal happened only when a validator
   *proposes* (`core/node/chainrole.go`), so an attest-only validator would lapse. **H2
   (RT-2) built the non-proposer renewal path** — `node.SubmitBondRenewal` submits a fresh
   `BondReg` (`MsgSubmitBondReg`) for inclusion by whoever proposes next (mirroring
   `pendingSlashes`), and `BondTTLBlocks` is now safe-by-default on the untrusted objective
   posture (`effectiveBondTTL`). The renewal-path prerequisite for the accumulation
   argument is therefore met; see [`m0.md`](m0.md) §6 (surface S3) and the
   archived hardening strategy `archive/design-history/m0-hardening-strategy.md` §3.
3. **Plot throughput `B`.** The 1 GiB floor scales linearly in `B`; `~270 MB/s` should
   be re-measured on target hardware with indegree-4 DRSample.
4. **Migration.** v2 plots must re-plot to v3. The format guard forces it, but the
   fleet-wide cost and the interim where v2 and v3 standings coexist is a rollout
   question to answer before release.
5. **Verification is external.** Per the immutables, this design is **not** held until
   an external red-team denies the corner against the built code. Self-marked homework
   is not adversarial proof.

---

## 9. Provenance

The construction was derived by an **independent researcher pass** with no build
context, working from the code, the tenets/immutables, and the proof-of-space
literature — deliberately not reading `docs/reviews/` or prior red-team reports, so it
could confirm or refute the builder's hypothesis on the merits. It confirmed the
hypothesis (public identity-bound seed + labeling check) and **corrected** it on three
points now embodied above: `n` must fold into `parentIndices` too; `n`-folding without
the labeling check is useless; and the public seed must never land first.

*Literature:* Dziembowski, Faust, Kolmogorov, Pietrzak, *Proofs of Space*, CRYPTO 2015
· Alwen, Blocki, Harsha, *DRSample*, CCS 2017 · Fisch, *Tight Proofs of Space and
Replication*, EUROCRYPT 2019 · Ren, Devadas, *PoS from Stacked Expanders*, 2016 ·
Ateniese, Bonacina, Faonio, Galesi, *PoS: When Space Is of the Essence*, SCN 2014 ·
Wesolowski, *Efficient VDFs*, EUROCRYPT 2019.
