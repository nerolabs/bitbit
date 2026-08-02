package bond

import (
	"bytes"
	"testing"

	"github.com/nerolabs/silt/core/vdf"
	"github.com/nerolabs/silt/ports"
)

// stDelay is a small VDF delay: enough to exercise the space-time path, fast
// enough for a unit test (the security floor is a deployment tuning knob).
const stDelay = 300

// A held bond answers its own space-TIME challenge: the VDF binds elapsed
// sequential work, the derived blocks are held, and the cheap verify passes.
func TestSpaceTimeHeldBondAnswers(t *testing.T) {
	c := Seal(nodeID(1), 1<<20)
	p := vdf.Default()
	for _, nonce := range []uint64{1, 42, 9999} {
		ans, ok := c.AnswerSpaceTime(nonce, p, stDelay)
		if !ok {
			t.Fatalf("holder could not answer space-time nonce %d", nonce)
		}
		if ans.VDFT != stDelay || len(ans.VDFY) == 0 {
			t.Fatalf("answer carries no VDF proof for nonce %d", nonce)
		}
		if !VerifySpaceTime(c.Root, c.Size, nonce, ans, p, stDelay) {
			t.Fatalf("valid space-time answer for nonce %d rejected", nonce)
		}
	}
}

// The time half has teeth: an answer that skips the VDF, claims the wrong
// amount of work, or forges the VDF output all fail — even if the block
// (space) proofs are perfectly valid.
func TestSpaceTimeRejectsMissingOrForgedWork(t *testing.T) {
	c := Seal(nodeID(1), 1<<20)
	p := vdf.Default()
	const nonce = 42

	// A space-ONLY answer (no VDF) must not satisfy a space-time challenge.
	spaceOnly, ok := c.Answer(nonce)
	if !ok {
		t.Fatal("setup: space-only answer")
	}
	if VerifySpaceTime(c.Root, c.Size, nonce, spaceOnly, p, stDelay) {
		t.Fatal("an answer with no VDF proof passed a space-time challenge")
	}

	honest, ok := c.AnswerSpaceTime(nonce, p, stDelay)
	if !ok {
		t.Fatal("setup: space-time answer")
	}

	// Claiming a different amount of work than required fails.
	wrongT := honest
	wrongT.VDFT = stDelay + 1
	if VerifySpaceTime(c.Root, c.Size, nonce, wrongT, p, stDelay) {
		t.Fatal("an answer claiming the wrong VDF delay passed")
	}

	// Forging the VDF output fails (the proof no longer attests the work, and
	// the derived block indices would shift under it anyway).
	forged := honest
	y := append([]byte(nil), honest.VDFY...)
	y[len(y)-1] ^= 0x01
	forged.VDFY = y
	if VerifySpaceTime(c.Root, c.Size, nonce, forged, p, stDelay) {
		t.Fatal("a forged VDF output passed")
	}
}

// The probed blocks are chosen by the VDF OUTPUT, not the raw nonce — so a
// prover cannot know which blocks to keep ready until it has done the work.
func TestSpaceTimeBlocksDerivedFromWork(t *testing.T) {
	c := Seal(nodeID(1), 1<<20)
	const nonce = 7
	spaceOnly, _ := c.Answer(nonce)
	spaceTime, _ := c.AnswerSpaceTime(nonce, vdf.Default(), stDelay)
	if bytes.Equal(intsToBytes(spaceOnly.Indices), intsToBytes(spaceTime.Indices)) {
		t.Fatal("space-time indices equal the raw-nonce indices — the work does not steer block selection")
	}
}

func intsToBytes(xs []int) []byte {
	b := make([]byte, 0, len(xs)*8)
	for _, x := range xs {
		b = append(b, byte(x), byte(x>>8), byte(x>>16), byte(x>>24))
	}
	return b
}

func nodeID(b byte) ports.NodeID { return ports.HashBytes([]byte{b}) }

// The plot must be a deterministic function of identity: the same node
// regenerates the same bond (so it can re-plot on setup), and two identities
// get genuinely distinct plots (so N Sybils are N distinct blobs on disk).
func TestPlotDeterministicAndIdentityBound(t *testing.T) {
	a := Seal(nodeID(1), 1<<20)
	b := Seal(nodeID(1), 1<<20)
	if a.Root != b.Root {
		t.Fatal("same identity produced a different plot — cannot regenerate")
	}
	other := Seal(nodeID(2), 1<<20)
	if a.Root == other.Root {
		t.Fatal("two identities produced the same plot — bonds not identity-bound")
	}
}

// The space-hardness lever: a block depends on EARLIER blocks, so it cannot
// be recomputed in isolation — perturbing a predecessor's leaf changes the
// block. If blocks were independent (the old placeholder), this would not
// hold and a prover could recompute any single block on demand instead of
// storing the plot.
func TestPlotBlockDependsOnEarlierBlocks(t *testing.T) {
	id := nodeID(7)
	// Build honest leaves for a small plot.
	const n = 64
	leaves := make([]ports.Hash, n)
	for i := 0; i < n; i++ {
		leaves[i] = ports.HashBytes(plotBlock(id, i, leaves))
	}
	const target = n - 1 // a late block: depends on its predecessor + parents

	honest := plotBlock(id, target, leaves)

	// Flip the immediate predecessor's leaf; the block must change.
	perturbed := append([]ports.Hash(nil), leaves...)
	perturbed[target-1][0] ^= 0xff
	if bytes.Equal(honest, plotBlock(id, target, perturbed)) {
		t.Fatal("block does not depend on its predecessor — plot is not chained")
	}

	// Flip one of its long-range parents' leaves; the block must change too.
	parents := parentIndices(id, target)
	if len(parents) == 0 {
		t.Fatal("expected long-range parents for a late block")
	}
	perturbed2 := append([]ports.Hash(nil), leaves...)
	perturbed2[parents[0]][0] ^= 0xff
	if bytes.Equal(honest, plotBlock(id, target, perturbed2)) {
		t.Fatal("block does not depend on its long-range parent — no depth to the graph")
	}
}

// Dependency indices are always earlier blocks (a DAG, never a cycle) and
// deterministic from the public (id, i).
func TestParentIndicesAreEarlierAndDeterministic(t *testing.T) {
	id := nodeID(3)
	if p := parentIndices(id, 0); p != nil {
		t.Fatalf("block 0 should have no parents, got %v", p)
	}
	for i := 1; i < 500; i++ {
		p1 := parentIndices(id, i)
		p2 := parentIndices(id, i)
		if len(p1) != plotParents {
			t.Fatalf("block %d: got %d parents, want %d", i, len(p1), plotParents)
		}
		for k, p := range p1 {
			if p < 0 || p >= i {
				t.Fatalf("block %d parent %d = %d is not an earlier block", i, k, p)
			}
			if p != p2[k] {
				t.Fatalf("block %d parent %d not deterministic", i, k)
			}
		}
	}
}

// A node that HOLDS its bond can answer any challenge, and the verifier
// confirms it against only the committed root.
func TestHeldBondAnswersItsOwnChallenge(t *testing.T) {
	c := Seal(nodeID(1), 1<<20) // 1 MiB
	for _, nonce := range []uint64{1, 42, 9999} {
		ans, ok := c.Answer(nonce)
		if !ok {
			t.Fatalf("holder could not answer nonce %d", nonce)
		}
		if !Verify(c.Root, c.Size, nonce, ans) {
			t.Fatalf("valid held answer for nonce %d rejected", nonce)
		}
	}
}

// V3, test-the-adversary: a prover that does NOT hold the bytes cannot
// satisfy the root. Corrupting a probed block, or answering with another
// identity's bond, both fail — so passing genuinely proves held storage.
func TestForgedOrUnheldBondFails(t *testing.T) {
	c := Seal(nodeID(1), 1<<20)
	ans, ok := c.Answer(42)
	if !ok {
		t.Fatal("setup: holder should answer")
	}

	// Corrupt one probed block: the inclusion proof no longer binds to the
	// committed root, i.e. the prover no longer has the real bytes.
	bad := Answer{Indices: ans.Indices, Proofs: ans.Proofs, Blocks: append([][]byte(nil), ans.Blocks...)}
	bad.Blocks[0] = append([]byte(nil), ans.Blocks[0]...)
	bad.Blocks[0][0] ^= 0xff
	if Verify(c.Root, c.Size, 42, bad) {
		t.Fatal("forged block accepted — storage not actually proven")
	}

	// Another identity's bond does not answer for this root (identity-bound).
	other, ok := Seal(nodeID(2), 1<<20).Answer(42)
	if !ok {
		t.Fatal("setup: other holder should answer its own bond")
	}
	if Verify(c.Root, c.Size, 42, other) {
		t.Fatal("another identity's bond satisfied this root — bonds are not identity-bound")
	}
}
