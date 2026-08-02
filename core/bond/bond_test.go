package bond

import (
	"bytes"
	"testing"

	"github.com/nerolabs/silt/ports"
)

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
