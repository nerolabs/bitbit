# M0 fix — the Sybil bond made real (red-team F1 / F2 / F3)

> **Superseded in part (2026-08-05):** a later red-team pass broke the Sybil corner
> again via **prefix plots** (G2), which per-root dedup cannot catch. The construction
> that closes it is in [m0-sybil-rebind.md](../../docs/design/m0-sybil-rebind.md); read that first.

> **Status: F1/F2/F3 structural fix SHIPPED (2026-08-04) in `core/bond` +
> `adapters/diskplot`; this doc is the spec it was built to.** The external M0
> red-team broke the Sybil corner *in the composition* — the Wesolowski VDF and
> the Shacham–Waters PoR were attacked and held; the **bond** did not. The
> byte-binding (DRSample depth-robust graph) and the pre-VDF plot-read seed are in
> code with inverted-PoC regressions. The **anti-release floor** is now enforced
> too: `Node.MinBondBytes` denies standing to a bond small enough to release and
> re-plot inside the challenge window — floor ≥ (challenge-window × plot-throughput);
> at ~270 MB/s (`bond.BenchmarkSeal`) and a 2 s window that is ~540 MiB, so an open
> deployment sets `-min-bond-floor` (e.g. `1G`). It is a **config knob** (default
> off, since every fast test/demo/NAT config uses tiny bonds), enforced with unit +
> integration coverage. `BondVDFDelay` remains the complementary time-floor knob.
>
> Report: `docs/reviews/M0-REDTEAM-REPORT.md` §1–§3. Findings F1/F2/F3.
> Supersedes the "Resolved by D1" language in `gate4-m0-mechanism.md` §3b.
> **This is the keystone:** the consensus fix (`m0-consensus.md`, F6) depends on
> the bond being *real*, and F3 dissolves once F1/F2 land.

## 1. What broke, precisely

Three findings, one root cause: **the bond charges for 4 KiB blocks but binds
only their 32-byte leaves, and the "time" half never touches the plot.**

### F1 — the plot binds leaves, not bytes (→ 1/128 storage, → 0 for small bonds)

`plotBlock` (`core/bond/bond.go:341`) derives each 4 KiB block from only the
**32-byte leaves** of its predecessor + 3 pseudo-random parents:

```go
// bond.go:348-356 (paraphrased)
if i > 0 { h.Write(leaves[i-1][:]) }           // 32 bytes
for _, p := range parentIndices(secret, i) {
    h.Write(leaves[p][:])                       // 32 bytes each
}
label := h.Sum(nil)                             // then expanded to 4 KiB (lines 360-367)
```

and `leaves[i] = ports.HashBytes(b)` (`bond.go:147`). The 4 KiB expansion is a
pure deterministic function of the 32-byte label — **nobody reads a block's
bytes; only its leaf hash feeds any other block.** So a prover that stores *only
the `leaves` array* (32 B/block) recomputes any probed block in a single
`plotBlock` call — no dependency-subgraph recursion — and builds its Merkle proof
from the same leaves. It passes `Verify` (`bond.go:250`) and the live-path
`VerifySpaceTime` (`bond.go:259`, driven from `bondaudit.go:144`) while holding
`BlockSize/32 = 128×` less than the bond it advertises. For bonds ≤ ~1.1 MiB,
`Seal` throughput exceeds the VDF window, so the attacker re-plots on demand and
holds **0 resident bytes** between challenges.

The load-bearing docstring at `bond.go:36-54` asserts the opposite ("recomputing
a probed block forces recomputing its whole dependency subgraph … store the S
bytes"). It is false: the dependencies *are* the leaves.

### F2 — the VDF "time" half is decorative (public input, gates nothing)

`AnswerSpaceTime` (`bond.go:230`) runs the VDF over
`challengeSeed(root, nonce)` (`bond.go:295`), which is **entirely public**
(`root || nonce`, no secret, no plot read). A zero-resident prover computes the
VDF from public data, learns the VDF-derived sample indices, *then* re-derives
exactly those blocks and passes. The sequential-work floor buys nothing because
it is **decoupled from possession**. Worse, raising `BondVDFDelay` *widens* the
re-plot budget; and a farm runs its per-identity VDFs concurrently across cores,
so it pays ~one VDF window for the whole fleet.

### F3 — root-owner dedup adds no Sybil cost (amplifier)

`RecordBondChallenge` (`core/credit/credit.go:142`) keys the `rootOwner` dedup on
**exact root-hash equality**, per-ledger. It fires only on byte-identical
replays; N distinct-secret identities each earn full standing. So identity
binding forces "N distinct roots" but never "N × S disk." The dedup is a
same-root tiebreak, **not a cost mechanism** — it cannot carry the Sybil cost the
design leans on.

## 2. The fix — adopt a real Proof-of-Space graph, bind bytes, bind the read

Per B8 (adopt, don't invent the primitive). Three coupled changes; the security
argument is standard **pebbling / depth-robustness**, not novel crypto.

### 2a. F1 — depth-robust graph over full-byte labels

**Replace the parent graph and the label dependency.**

1. **Depth-robust graph (DRG).** Replace the 3-pseudo-random-parent selection
   (`parentIndices`, `bond.go:373`) with a **proven depth-robust graph** —
   DRSample (Alwen–Blocki–Harsha) or the Ateniese construction the red-team
   named. Depth-robustness is what makes the pebbling complexity (and therefore
   the space a cheating prover must hold) provably `Ω(N)`. The current 3-parent
   graph has no such proof.
2. **Bind the bytes, not the leaves.** `plotBlock` must hash the **full parent
   *blocks*** (`blocks[p]`, 4 KiB), not `leaves[p]` (32 B):
   ```go
   // proposed: label depends on full predecessor/parent block BYTES
   h.Write(blocks[i-1])                 // full 4 KiB, not leaves[i-1][:]
   for _, p := range drSample(secret, i) { h.Write(blocks[p]) }
   ```
   Now reconstructing block *i* requires the *bytes* of its parents, which
   require *their* parents — and over a depth-robust graph the pebbling argument
   forces holding `Θ(N·BlockSize)` resident bytes. A leaves-only prover holds
   `H(block_j)` but cannot invert it to `block_j`; the 128× gap closes to **1×.**
   The charged size again equals the resident footprint.
3. **(Free variable, optional) memory-hard label function** for ASIC-resistance —
   the *shape* (depth-robust graph + full-byte dependency) is the load-bearing
   part; the label function is tunable (Evolving tenet).

**What does *not* change:** `Verify`/`verifyAt` (`bond.go:250/274`) still only
check Merkle inclusion of `leaf = H(block_i)` against the published root — it
never recomputes `plotBlock`, so **verify stays O(log n) on the core loop.** The
change is confined to plot generation (`Seal`, `bond.go:140`) and the security
argument; the verifier seam `Verify(root, size, nonce, Answer)` is untouched.
`Reconstruct` (`bond.go:162`) already re-derives leaves from the stored blocks,
so disk persistence (`adapters/diskplot`) needs no schema change — only the plot
contents differ.

### 2b. F2 — bind the sampling challenge to a plot read *before* the VDF

Adopt the Spacemesh discipline: **you must read the plot to start the VDF.**
Derive the VDF seed from a challenge-selected plot location instead of pure public
data:

```
j*   = plotIndex(root, nonce)                 // public: which block to read
seed = H("silt/bond/st/v2" ‖ root ‖ nonce ‖ block[j*])   // requires block[j*]
vdf.Eval(p, seed, delay)
```

Now the VDF input depends on a block the prover must **possess at VDF-start**.
Composed with 2a, recomputing `block[j*]` on demand is memory-hard (`Ω(N)`
pebbling work) — so a zero-resident prover cannot cheaply produce the seed, and
releasing the space forfeits the ability to answer within the window.

**Re-tune so re-plot ≫ epoch.** The anti-release guarantee is quantitative:
plotting/re-plotting cost (now memory-hard, `Θ(N·BlockSize)` work) must exceed
`BondVDFDelay` + the answer window, so a farm cannot release+re-plot between
challenges. State and measure the constant (§5). This *inverts* the old
pathology where raising the delay helped the attacker: with the read-bound seed,
a longer delay no longer widens a cheap re-plot budget, because the re-plot
itself is the expensive memory-hard step.

The VDF primitive is sound in isolation (the red-team found no cheap-fake in
`normalizeInput`/`hashToPrime`/range checks); the fix is **where it sits in the
composition**, not the VDF.

### 2c. F3 — let cost live in the proof; keep dedup as a tiebreak only

Once 2a makes per-identity cost `N·S` real, `rootOwner` dedup returns to its
correct, narrow job: a same-root tiebreak that stops a byte-identical replay.
**Stop leaning on it for Sybil cost.** Identity-binding already holds structurally
— `bondSecret` (`bondaudit.go:22`) derives the plot secret from the node's
*signing key*, so a farm needs one independent, expensive plot per identity;
there is no one-plot-covers-N path once recompute is memory-hard. No code change
required for F3 beyond the docstring correction and not over-claiming dedup.

## 3. The composition, after the fix

```
plot (off-loop, memory-hard):  block[i] = MHF( secret(NodeID), DRG-parents' BYTES )
                               root = Merkle(leaves),  leaf[i] = H(block[i])
                               resident footprint = charged size  (1×, was 1/128×)

challenge (per epoch):  j*   = plotIndex(root, nonce)            ── must READ plot
                        seed = H(root ‖ nonce ‖ block[j*])
                        VDF(seed, delay)  ── sequential time, seeded by possession
                        indices = f(VDF output);  Merkle proofs at indices

verify (on-loop, O(log n), UNCHANGED SEAM):  VerifySpaceTime(root,size,nonce,Answer,…)
```

Standing still flows through the existing number seam: `RecordBondChallenge` →
`bondedBytes` → `Reputation` (`credit.go:220`), decayed by `DecayStale` so the
integral must be *sustained*. The bond fix changes only what `bondedBytes`
*means* — now genuinely-held space, not 1/128 of it.

## 4. Schema + persistence touch

- **Plot contents change** (byte-bound labels over a DRG); the on-disk format
  (`adapters/diskplot`, magic/version header) bumps its internal `version` so a
  stale v1 plot is re-plotted, not trusted (B7). This is *not* a `Block.Version`
  change — plots are off-chain.
- **No block-schema change** for the bond itself: the chain still sees standing
  only as a number. (The *consensus* fix, `m0-consensus.md`, adds on-chain bond
  commitments and *does* ride `Block.Version` — that is where the bond becomes
  chain-visible.)
- **Re-plot on upgrade** is a one-time operator cost; document it in the upgrade
  note and gate it behind the plot-format version.

## 5. Falsifiable denial + regression (invert the PoCs)

**Denial (V3):** a prover holding fewer than the charged bytes, or having released
the plot, cannot answer a live `VerifySpaceTime` challenge except with negligible
probability; N Sybil identities cost `N·S` disk held across time.

Adopt the red-team's own PoCs **inverted** as regression (assert DENIED):

| Red-team PoC (asserts BROKEN) | Regression (assert DENIED) |
|---|---|
| `core/bond/sybil_amortize_test.go` (leaves-only 128×) | leaves-only prover now **fails** `Verify`/`VerifySpaceTime` |
| `core/bond/redteam_sybil2_angle_a_test.go` (zero-resident re-plot) | zero-resident re-plot cannot beat the challenge window; measure re-plot ≫ epoch |
| `core/bond/redteam_sybil2_angle_c_test.go` (VDF decorative) | releasing the plot before the VDF forfeits the answer (read-bound seed) |
| `core/credit/redteam_sybil2_angle_b_test.go` (dedup no-cost) | N identities now cost N plots via memory-hard recompute, not dedup |

Plus a **cost-constant measurement** test: honest single-node plot time + resident
bytes vs. Sybil-farm cost at the influence threshold — state the number the design
promises (§6 of the mechanism doc).

## 6. Open risks to hand the red-team

- Is the chosen DRG's depth-robustness constant strong enough at silt's `N`, or
  does a shallow-pebbling shortcut recover sub-linear storage? (Pick a *proven*
  construction and cite the bound.)
- Does the read-bound VDF seed fully close the release-and-recompute gap, or does
  a partial-plot prover (holds a cut of the DRG) still answer a fraction of
  challenges? Tune `Samples` so partial storage fails w.h.p.
- Farm VDF parallelism: confirm the per-identity *space* (not the VDF) is the
  binding cost, and that concurrent VDFs across a fleet don't reopen an
  amortization path.
- Plot/re-plot time vs. epoch: measure and prove `re-plot > BondVDFDelay + window`
  on commodity hardware at the target honest size.

## 7. Build sequencing (code is the next turn)

1. DRG parent selection + full-byte label in `plotBlock`/`Seal`; bump plot format
   version; unit tests for pebbling-hard recompute.
2. Read-bound VDF seed in `AnswerSpaceTime`/`VerifySpaceTime`; re-tune delay vs.
   measured re-plot cost.
3. Docstring truth-fix at `bond.go:36-54`; drop dedup over-claim.
4. Invert the four red-team PoCs as regression; add the cost-constant test.
5. Whole-suite pass (`-race` on `core/bond` + `core/credit` + sim `bondstanding`)
   per testing discipline; then hand F6 (consensus) the now-real bond.
