package bond

import (
	"testing"

	"github.com/nerolabs/silt/core/manifest"
	"github.com/nerolabs/silt/ports"
)

// These are the M0 red-team G2 PoCs INVERTED as regressions: the prefix-plot
// Sybil and its enablers, which per-root dedup structurally could not catch,
// are now DENIED by the verified labeling check. See
// docs/design/m0-sybil-rebind.md and the researcher memo (research/G2-SYBIL-BOND-MEMO.md).

// G2 (the break): one physical N-block plot backed N standings, because
// plotBlock/parentIndices keyed only on (secret,i) — so blocks 0..m-1 of an
// N-plot were byte-identical to a standalone m-plot with its OWN distinct root,
// and the verifier only checked Merkle INCLUSION, never recomputed a LABEL.
//
// The fix folds n into the public seed H(pk, n), so a PREFIX of an n-plot is no
// longer byte-identical to a valid m-plot — and even if an attacker commits the
// prefix bytes under an m-leaf root, the labeling check recomputes labels for
// size m and they DON'T match. This asserts both halves.
func TestRedteamG2_PrefixPlotIsNotAValidSmallerPlot(t *testing.T) {
	pkA := pk(11)
	const bigN = 512
	big := sealN(pkA, bigN) // the one physical plot the attacker holds

	// (a) Data layer: the prefix bytes of the N-plot are NOT the bytes an
	// honest m-plot would have, so the prefix root differs from a real m-plot's
	// root. The prefix attack relied on these being byte-identical.
	const m = 256
	honestM := sealN(pkA, m)
	prefixRoot := manifest.MerkleRoot(big.leaves[:m])
	if prefixRoot == honestM.Root {
		t.Fatal("G2 regression: an N-plot prefix is byte-identical to a real m-plot — size not folded into the seed")
	}

	// (b) Verification layer — the decisive one. The attacker commits the N-plot's
	// first m blocks under their own (prefix) root and answers a size-m challenge.
	// Possession passes (it holds the bytes), but the labeling check recomputes
	// labels from H(pk, m) and the N-plot's blocks are labeled for H(pk, N):
	// the recompute fails, so the forged size-m standing is DENIED.
	prefix := &Commitment{
		Size:   int64(m) * BlockSize,
		Root:   prefixRoot,
		pk:     pkA,
		seed:   plotSeedN(pkA, m), // what the attacker CLAIMS the size is
		blocks: big.blocks[:m],
		leaves: big.leaves[:m],
	}
	const nonce = 7
	ans, ok := prefix.Answer(nonce, testK)
	if !ok {
		t.Fatal("setup: attacker holds the prefix bytes, so it can build possession opens")
	}
	// Sanity: possession alone (the pre-G2 check) would have accepted this — the
	// bytes really are in the prefix tree. It is the labeling check that denies it.
	if !possessionOnly(prefixRoot, prefix.Size, nonce, ans) {
		t.Fatal("setup: the prefix genuinely satisfies Merkle possession (the pre-G2 check)")
	}
	if Verify(pkA, prefixRoot, prefix.Size, nonce, ans, testK) {
		t.Fatal("G2 regression: a prefix of a larger plot passed a smaller-size challenge — one disk backs N standings")
	}
}

// G2 identity binding: a plot sealed for pk_A must fail when claimed by pk_B.
// Before G2 the root was a free variable bound only by a signature over an
// attacker-chosen root; now the verifier recomputes labels from the CLAIMED key,
// so victim-sealed bytes don't match under the attacker's key.
func TestRedteamG2_PlotBoundToClaimedIdentity(t *testing.T) {
	pkA, pkB := pk(21), pk(22)
	c := Seal(pkA, 1<<20)
	const nonce = 42
	ans, ok := c.Answer(nonce, testK)
	if !ok {
		t.Fatal("setup: holder answers its own plot")
	}
	if !Verify(pkA, c.Root, c.Size, nonce, ans, testK) {
		t.Fatal("setup: the plot verifies under its own key")
	}
	// The SAME bytes, the SAME root, claimed by a different identity: the labeling
	// recompute uses H(pkB, n) and fails. (bondaudit additionally binds
	// sha256(pk)==peerID, but the labeling check alone already denies it.)
	if Verify(pkB, c.Root, c.Size, nonce, ans, testK) {
		t.Fatal("G2 regression: a plot sealed for one identity verified under another's key")
	}
}

// G2 arbitrary bytes: committing random bytes of the right size and answering
// possession is not enough — the labeling check recomputes each opened node's
// label and requires a match, which random bytes cannot satisfy. This is the
// proof-of-STORAGE → proof-of-SPACE upgrade: bytes you cannot regenerate are
// rejected.
func TestRedteamG2_ArbitraryBytesFailLabeling(t *testing.T) {
	pkA := pk(31)
	const n = 256
	// A junk "plot": every block is arbitrary (here derived from the index so the
	// tree is well-formed) but NOT the DRSample label for (pkA, n).
	blocks := make([][]byte, n)
	leaves := make([]ports.Hash, n)
	for i := 0; i < n; i++ {
		b := make([]byte, BlockSize)
		h := ports.HashBytes([]byte{byte(i), byte(i >> 8), 0xab})
		copy(b, h[:])
		blocks[i], leaves[i] = b, ports.HashBytes(b)
	}
	junk := &Commitment{Size: int64(n) * BlockSize, Root: manifest.MerkleRoot(leaves), pk: pkA, seed: plotSeedN(pkA, n), blocks: blocks, leaves: leaves}
	const nonce = 99
	ans, ok := junk.Answer(nonce, testK)
	if !ok {
		t.Fatal("setup: the junk plot holds its bytes, so possession opens build")
	}
	if !possessionOnly(junk.Root, junk.Size, nonce, ans) {
		t.Fatal("setup: junk bytes satisfy Merkle possession — the pre-G2 check")
	}
	if Verify(pkA, junk.Root, junk.Size, nonce, ans, testK) {
		t.Fatal("G2 regression: arbitrary committed bytes passed — this is proof-of-storage, not proof-of-space")
	}
}

// G2 quantified: the amplification factor collapses from (N-M+1) to 1. Across a
// family of prefix roots of the ONE physical plot, exactly ZERO forge a standing
// at their claimed (smaller) size — so N identities require N plots, not one.
func TestRedteamG2_PrefixFamilyYieldsNoExtraStandings(t *testing.T) {
	pkA := pk(41)
	const bigN = 512
	big := sealN(pkA, bigN)
	const nonce = 3
	forged := 0
	for _, m := range []int{64, 128, 200, 256, 400} {
		prefix := &Commitment{
			Size:   int64(m) * BlockSize,
			Root:   manifest.MerkleRoot(big.leaves[:m]),
			pk:     pkA,
			seed:   plotSeedN(pkA, m),
			blocks: big.blocks[:m],
			leaves: big.leaves[:m],
		}
		ans, ok := prefix.Answer(nonce, testK)
		if ok && Verify(pkA, prefix.Root, prefix.Size, nonce, ans, testK) {
			forged++
		}
	}
	if forged != 0 {
		t.Fatalf("G2 regression: %d prefix standings forged from one plot — amplification not collapsed to 1", forged)
	}
}

// G2 default-safety: leaving k unset (0) must NOT disable the labeling check —
// it resolves to DefaultLabelSamples. An unset config is the exact "off by
// default" trap that has bitten this codebase; assert the prefix Sybil is denied
// at k=0 too.
func TestRedteamG2_UnsetKStillDeniesPrefixSybil(t *testing.T) {
	pkA := pk(51)
	const bigN = 512
	big := sealN(pkA, bigN)
	const m = 256
	prefix := &Commitment{
		Size:   int64(m) * BlockSize,
		Root:   manifest.MerkleRoot(big.leaves[:m]),
		pk:     pkA,
		seed:   plotSeedN(pkA, m),
		blocks: big.blocks[:m],
		leaves: big.leaves[:m],
	}
	const nonce = 5
	ans, ok := prefix.Answer(nonce, 0) // k=0 ⇒ DefaultLabelSamples, on both sides
	if !ok {
		t.Fatal("setup: prefix answer builds")
	}
	if Verify(pkA, prefix.Root, prefix.Size, nonce, ans, 0) {
		t.Fatal("G2 regression: prefix Sybil passed with k unset — labeling check silently disabled")
	}
}

// sealN plots for pk with exactly n blocks (n = size/BlockSize), so tests can
// reason in block counts directly.
func sealN(pk []byte, n int) *Commitment { return Seal(pk, int64(n)*BlockSize) }

// possessionOnly re-runs ONLY the pre-G2 Merkle-possession check (no labeling),
// so a test can show an attack satisfied the old scheme and is caught solely by
// the new labeling check.
func possessionOnly(root ports.Hash, size int64, nonce uint64, a Answer) bool {
	n := NumBlocks(size)
	want := challengeIndices(root, n, nonce)
	if len(a.Indices) != len(want) || len(a.Blocks) != len(want) || len(a.Proofs) != len(want) {
		return false
	}
	for j := range want {
		if a.Indices[j] != want[j] {
			return false
		}
		p := a.Proofs[j]
		if p.Index != want[j] || p.Total != n {
			return false
		}
		if !manifest.VerifyProof(root, ports.HashBytes(a.Blocks[j]), p) {
			return false
		}
	}
	return true
}
