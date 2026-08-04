package bond

import (
	"bytes"
	"testing"

	"github.com/nerolabs/silt/core/manifest"
	"github.com/nerolabs/silt/core/vdf"
	"github.com/nerolabs/silt/ports"
)

// These are the M0 red-team Sybil PoCs (F1/F2) INVERTED as regressions: each
// asserts the attack that used to PASS is now DENIED. See
// docs/design/m0-sybil-bond.md §5 and docs/reviews/M0-REDTEAM-REPORT.md §1–§2.
// The G2 prefix-plot regressions live in redteam_g2_test.go.

// F1 (leaves-only prover): the red-team stored only the 32-byte leaves (1/128 of
// the plot) and recomputed any probed block on demand, because the old plotBlock
// derived block i from its parents' LEAVES. The fix binds block i to its parents'
// full BYTES over a depth-robust graph, so a prover that kept only the leaves
// cannot reconstruct a block: without the true parent bytes, the recompute yields
// a block whose hash no longer matches the committed leaf.
func TestRedteamF1_ByteBindingDefeatsLeavesOnly(t *testing.T) {
	const n = 128
	seed := plotSeedN(pk(9), n)
	blocks := make([][]byte, n)
	for i := 0; i < n; i++ {
		blocks[i] = plotBlock(seed, i, n, blocks)
	}
	leaves := make([]ports.Hash, n)
	for i := range blocks {
		leaves[i] = ports.HashBytes(blocks[i])
	}

	// A leaves-only adversary discarded the block bytes and keeps only `leaves`.
	// Standing in anything but the true parent bytes (here: zeroed blocks, the
	// information a leaves-only prover actually holds about the bytes — none)
	// must NOT reproduce the committed block.
	const target = n - 1
	starved := make([][]byte, n)
	for i := range starved {
		starved[i] = make([]byte, BlockSize)
	}
	forged := plotBlock(seed, target, n, starved)
	if ports.HashBytes(forged) == leaves[target] {
		t.Fatal("F1 regression: block recomputed without parent bytes — a leaves-only prover would still pass")
	}
	// Sanity: with the true parent bytes it DOES reproduce (the plot is a
	// deterministic function of the held bytes, so an honest holder answers).
	if !bytes.Equal(plotBlock(seed, target, n, blocks), blocks[target]) {
		t.Fatal("honest recompute from held bytes must reproduce the block")
	}
}

// F2 (VDF gates nothing): the red-team seeded the VDF from the PUBLIC
// challengeSeed(root, nonce), ran it with zero resident bytes, then re-derived
// the sampled blocks — releasing the space forfeited nothing. The fix seeds the
// VDF from a plot block read BEFORE the VDF (seedIndex), carried with an
// inclusion proof, so an answer that did not read the real plot block fails.
func TestRedteamF2_VDFBoundToPlotRead(t *testing.T) {
	pkA := pk(4)
	c := Seal(pkA, 1<<20)
	p := vdf.Default()
	const nonce = 123

	honest, ok := c.AnswerSpaceTime(nonce, p, stDelay, testK)
	if !ok || !VerifySpaceTime(pkA, c.Root, c.Size, nonce, honest, p, stDelay, testK) {
		t.Fatal("setup: honest space-time answer must verify")
	}
	// The honest answer must actually carry the pre-VDF seed read.
	if len(honest.SeedBlock) != BlockSize || honest.SeedProof.Total != NumBlocks(c.Size) {
		t.Fatal("honest space-time answer must carry the plot-read seed block + proof")
	}
	if honest.SeedProof.Index != seedIndex(c.Root, NumBlocks(c.Size), nonce) {
		t.Fatal("seed proof must be for the challenge-selected seed index")
	}

	// (a) Tamper the seed block: the inclusion proof no longer binds it to the
	// committed root, so a prover that fabricated the seed instead of reading the
	// real plot block is rejected.
	tampered := honest
	tampered.SeedBlock = append([]byte(nil), honest.SeedBlock...)
	tampered.SeedBlock[0] ^= 0xff
	if VerifySpaceTime(pkA, c.Root, c.Size, nonce, tampered, p, stDelay, testK) {
		t.Fatal("F2 regression: a tampered VDF seed block passed")
	}

	// (b) A zero-resident prover that seeded the VDF from public data alone
	// carries no plot-read proof — rejected.
	noSeed := honest
	noSeed.SeedBlock = nil
	noSeed.SeedProof = manifest.Proof{}
	if VerifySpaceTime(pkA, c.Root, c.Size, nonce, noSeed, p, stDelay, testK) {
		t.Fatal("F2 regression: an answer with no plot-read seed passed")
	}

	// (c) Seeding from ANOTHER plot's block (a prover that holds a different, or
	// smaller, plot) fails: the seed proof is against the wrong root/index.
	other := Seal(pk(5), 1<<20)
	otherST, ok := other.AnswerSpaceTime(nonce, p, stDelay, testK)
	if !ok {
		t.Fatal("setup: other plot answer")
	}
	crossed := honest
	crossed.SeedBlock = otherST.SeedBlock
	crossed.SeedProof = otherST.SeedProof
	if VerifySpaceTime(pkA, c.Root, c.Size, nonce, crossed, p, stDelay, testK) {
		t.Fatal("F2 regression: a VDF seed block from another plot passed")
	}
}

// F3 (dedup carries no cost) is structural, not a runtime bypass: distinct
// identities produce distinct plots, so cost lives in the (now byte-bound) proof
// and the root-owner dedup is only a same-root tiebreak. The distinct-plot
// property is asserted by TestPlotDeterministicAndIdentityBound; this records the
// coupling so a future change that re-collapses plots is caught here too.
func TestRedteamF3_DistinctIdentitiesDistinctPlots(t *testing.T) {
	a := Seal(pk(1), 1<<20)
	b := Seal(pk(2), 1<<20)
	if a.Root == b.Root {
		t.Fatal("F3 regression: two identities share a plot root — cost would not scale with identities")
	}
}
