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

// testK is a small labeling-opens count for fast unit tests; production defaults
// to bond.DefaultLabelSamples (64). The construction is identical at any k>0.
const testK = 8

// A plot reloaded from persisted bytes rebuilds the same commitment (root
// re-derived, not trusted — B7) and answers challenges — the restart path.
func TestReconstructRoundTrip(t *testing.T) {
	pkA := pk(5)
	orig := Seal(pkA, 1<<20)
	got, err := Reconstruct(pkA, orig.Size, orig.Blocks())
	if err != nil {
		t.Fatalf("reconstruct: %v", err)
	}
	if got.Root != orig.Root {
		t.Fatal("reconstructed root differs — reload would advertise the wrong bond")
	}
	ans, ok := got.Answer(42, testK)
	if !ok || !Verify(pkA, got.Root, got.Size, 42, ans, testK) {
		t.Fatal("reconstructed bond cannot answer its own challenge")
	}
	// A wrong size (block count mismatch) or a corrupt block length is rejected
	// so the caller re-plots rather than trusting a bad plot.
	if _, err := Reconstruct(pkA, (1<<20)+BlockSize, orig.Blocks()); err == nil {
		t.Fatal("reconstruct accepted a block count that disagrees with size")
	}
	short := append([][]byte(nil), orig.Blocks()...)
	short[0] = short[0][:BlockSize-1]
	if _, err := Reconstruct(pkA, orig.Size, short); err == nil {
		t.Fatal("reconstruct accepted a wrong-length block")
	}
}

// A held bond answers its own space-TIME challenge: the VDF binds elapsed
// sequential work, the derived blocks are held, and the cheap verify passes.
func TestSpaceTimeHeldBondAnswers(t *testing.T) {
	pkA := pk(1)
	c := Seal(pkA, 1<<20)
	p := vdf.Default()
	for _, nonce := range []uint64{1, 42, 9999} {
		ans, ok := c.AnswerSpaceTime(nonce, p, stDelay, testK)
		if !ok {
			t.Fatalf("holder could not answer space-time nonce %d", nonce)
		}
		if ans.VDFT != stDelay || len(ans.VDFY) == 0 {
			t.Fatalf("answer carries no VDF proof for nonce %d", nonce)
		}
		if !VerifySpaceTime(pkA, c.Root, c.Size, nonce, ans, p, stDelay, testK) {
			t.Fatalf("valid space-time answer for nonce %d rejected", nonce)
		}
	}
}

// The time half has teeth: an answer that skips the VDF, claims the wrong
// amount of work, or forges the VDF output all fail — even if the block
// (space) proofs are perfectly valid.
func TestSpaceTimeRejectsMissingOrForgedWork(t *testing.T) {
	pkA := pk(1)
	c := Seal(pkA, 1<<20)
	p := vdf.Default()
	const nonce = 42

	// A space-ONLY answer (no VDF) must not satisfy a space-time challenge.
	spaceOnly, ok := c.Answer(nonce, testK)
	if !ok {
		t.Fatal("setup: space-only answer")
	}
	if VerifySpaceTime(pkA, c.Root, c.Size, nonce, spaceOnly, p, stDelay, testK) {
		t.Fatal("an answer with no VDF proof passed a space-time challenge")
	}

	honest, ok := c.AnswerSpaceTime(nonce, p, stDelay, testK)
	if !ok {
		t.Fatal("setup: space-time answer")
	}

	// Claiming a different amount of work than required fails.
	wrongT := honest
	wrongT.VDFT = stDelay + 1
	if VerifySpaceTime(pkA, c.Root, c.Size, nonce, wrongT, p, stDelay, testK) {
		t.Fatal("an answer claiming the wrong VDF delay passed")
	}

	// Forging the VDF output fails (the proof no longer attests the work, and
	// the derived block indices would shift under it anyway).
	forged := honest
	y := append([]byte(nil), honest.VDFY...)
	y[len(y)-1] ^= 0x01
	forged.VDFY = y
	if VerifySpaceTime(pkA, c.Root, c.Size, nonce, forged, p, stDelay, testK) {
		t.Fatal("a forged VDF output passed")
	}
}

// The probed blocks are chosen by the VDF OUTPUT, not the raw nonce — so a
// prover cannot know which blocks to keep ready until it has done the work.
func TestSpaceTimeBlocksDerivedFromWork(t *testing.T) {
	c := Seal(pk(1), 1<<20)
	const nonce = 7
	spaceOnly, _ := c.Answer(nonce, testK)
	spaceTime, _ := c.AnswerSpaceTime(nonce, vdf.Default(), stDelay, testK)
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

// pk fakes a validator ed25519 public key for tests: the bond seed is H(pk, n),
// and bond treats pk as opaque bytes, so any distinct 32 bytes stand in for a key.
func pk(b byte) []byte { h := ports.HashBytes([]byte{b}); return h[:] }

// BenchmarkSeal reports the plot (and therefore re-plot) cost per bond size —
// the constant behind the F2 tuning claim "re-plot ≫ one epoch." Run with
// `go test ./core/bond -run x -bench Seal -benchmem`. Byte-binding over the
// depth-robust graph makes each block hash its parents' full bytes, so plotting
// is deliberately more expensive than the old leaves-only labeling; that expense
// is the Sybil cost and the anti-release floor.
func BenchmarkSeal(b *testing.B) {
	for _, size := range []int64{1 << 20, 4 << 20} {
		b.Run(sizeLabel(size), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				_ = Seal(pk(byte(i)), size)
			}
		})
	}
}

func sizeLabel(size int64) string {
	switch {
	case size >= 1<<20:
		return itoa(size>>20) + "MiB"
	default:
		return itoa(size>>10) + "KiB"
	}
}

func itoa(x int64) string {
	if x == 0 {
		return "0"
	}
	var b []byte
	for x > 0 {
		b = append([]byte{byte('0' + x%10)}, b...)
		x /= 10
	}
	return string(b)
}

// The plot must be a deterministic function of identity: the same node
// regenerates the same bond (so it can re-plot on setup), and two identities
// get genuinely distinct plots (so N Sybils are N distinct blobs on disk).
func TestPlotDeterministicAndIdentityBound(t *testing.T) {
	a := Seal(pk(1), 1<<20)
	b := Seal(pk(1), 1<<20)
	if a.Root != b.Root {
		t.Fatal("same identity produced a different plot — cannot regenerate")
	}
	other := Seal(pk(2), 1<<20)
	if a.Root == other.Root {
		t.Fatal("two identities produced the same plot — bonds not identity-bound")
	}
}

// The space-hardness lever (M0 F1 fix): a block depends on the BYTES of earlier
// blocks, so it cannot be recomputed in isolation — perturbing a predecessor's
// or a parent's block bytes changes the block. Binding to bytes (not the
// 32-byte leaves) is what stops a prover from storing only leaves and
// recomputing any single block on demand instead of storing the plot.
func TestPlotBlockDependsOnEarlierBlockBytes(t *testing.T) {
	const n = 64
	seed := plotSeedN(pk(7), n)
	// Build an honest small plot (full blocks, as Seal does).
	blocks := make([][]byte, n)
	for i := 0; i < n; i++ {
		blocks[i] = plotBlock(seed, i, n, blocks)
	}
	const target = n - 1 // a late block: depends on its predecessor + parents

	honest := plotBlock(seed, target, n, blocks)

	// Flip a byte of the immediate predecessor's BLOCK; the block must change.
	perturbed := cloneBlocks(blocks)
	perturbed[target-1][0] ^= 0xff
	if bytes.Equal(honest, plotBlock(seed, target, n, perturbed)) {
		t.Fatal("block does not depend on its predecessor's bytes — plot is not byte-chained")
	}

	// Flip a byte of one long-range parent's BLOCK; the block must change too.
	parents := parentIndices(seed, target, n)
	if len(parents) == 0 {
		t.Fatal("expected long-range parents for a late block")
	}
	perturbed2 := cloneBlocks(blocks)
	perturbed2[parents[0]][0] ^= 0xff
	if bytes.Equal(honest, plotBlock(seed, target, n, perturbed2)) {
		t.Fatal("block does not depend on its long-range parent's bytes — no depth to the graph")
	}
}

func cloneBlocks(in [][]byte) [][]byte {
	out := make([][]byte, len(in))
	for i, b := range in {
		out[i] = append([]byte(nil), b...)
	}
	return out
}

// Dependency indices are always earlier blocks (a DAG, never a cycle) and
// deterministic from the public (seed, i, n).
func TestParentIndicesAreEarlierAndDeterministic(t *testing.T) {
	const n = 500
	seed := plotSeedN(pk(3), n)
	if p := parentIndices(seed, 0, n); p != nil {
		t.Fatalf("block 0 should have no parents, got %v", p)
	}
	for i := 1; i < n; i++ {
		p1 := parentIndices(seed, i, n)
		p2 := parentIndices(seed, i, n)
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
	pkA := pk(1)
	c := Seal(pkA, 1<<20) // 1 MiB
	for _, nonce := range []uint64{1, 42, 9999} {
		ans, ok := c.Answer(nonce, testK)
		if !ok {
			t.Fatalf("holder could not answer nonce %d", nonce)
		}
		if !Verify(pkA, c.Root, c.Size, nonce, ans, testK) {
			t.Fatalf("valid held answer for nonce %d rejected", nonce)
		}
	}
}

// V3, test-the-adversary: a prover that does NOT hold the bytes cannot
// satisfy the root. Corrupting a probed block, or answering with another
// identity's bond, both fail — so passing genuinely proves held storage.
func TestForgedOrUnheldBondFails(t *testing.T) {
	pkA := pk(1)
	c := Seal(pkA, 1<<20)
	ans, ok := c.Answer(42, testK)
	if !ok {
		t.Fatal("setup: holder should answer")
	}

	// Corrupt one probed block: the inclusion proof no longer binds to the
	// committed root, i.e. the prover no longer has the real bytes.
	bad := ans
	bad.Blocks = append([][]byte(nil), ans.Blocks...)
	bad.Blocks[0] = append([]byte(nil), ans.Blocks[0]...)
	bad.Blocks[0][0] ^= 0xff
	if Verify(pkA, c.Root, c.Size, 42, bad, testK) {
		t.Fatal("forged block accepted — storage not actually proven")
	}

	// Another identity's bond does not answer for this root (identity-bound).
	other, ok := Seal(pk(2), 1<<20).Answer(42, testK)
	if !ok {
		t.Fatal("setup: other holder should answer its own bond")
	}
	if Verify(pkA, c.Root, c.Size, 42, other, testK) {
		t.Fatal("another identity's bond satisfied this root — bonds are not identity-bound")
	}
}
