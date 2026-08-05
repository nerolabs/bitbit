package chain

import (
	"crypto/ed25519"
	"testing"

	"github.com/nerolabs/silt/ports"
)

// M0 hardening H4 (Memo 05): consensus safety must come from quorum ARITHMETIC at
// the Byzantine threshold, not from a fixed quorum or a head-count of validators.
// These tests prove the two exit criteria: (1) two quorums always intersect above
// the fault bound; (2) one operator with many keys cannot trip the training wheels
// off.

// TestBFTQuorumIntersectionAboveFaultBound is the safety proof: for every validator
// set size, the Byzantine-sized support set (proposer + bftThreshold attesters)
// is a supermajority whose any two instances share at least f+1 members — so, with
// at most f Byzantine, at least one HONEST validator is in both, and two conflicting
// blocks can't each gather a quorum. This is the arithmetic a fixed Quorum lacks.
func TestBFTQuorumIntersectionAboveFaultBound(t *testing.T) {
	for n := 1; n <= 200; n++ {
		f := (n - 1) / 3
		support := bftThreshold(n) + 1 // + the proposer, always a qualified signer
		if support > n {
			t.Fatalf("n=%d: required support %d exceeds the set — unsatisfiable (liveness)", n, support)
		}
		overlap := 2*support - n // two support sets of size `support` over n share ≥ this many
		if overlap < f+1 {
			t.Fatalf("n=%d f=%d support=%d: two quorums overlap in %d < f+1=%d — an attacker could double-commit", n, f, support, overlap, f+1)
		}
		if honest := overlap - f; honest < 1 {
			t.Fatalf("n=%d: with ≤ f=%d Byzantine among the overlap, honest overlap is %d < 1", n, f, honest)
		}
	}
}

// objectiveSet builds an objective chain seeded with N genesis-bonded validators
// (2 MiB each), a Quorum floor, and the Byzantine-quorum flag. No anchors and
// MatureValidators 0 → always mature, so the pure quorum arithmetic is what's tested.
func objectiveSet(t *testing.T, n, quorumFloor int, byz bool) (*Chain, []ed25519.PrivateKey, *Block) {
	t.Helper()
	vals := make([]ed25519.PrivateKey, n)
	regs := make([]BondReg, n)
	for i := range vals {
		vals[i] = key(int64(1000 + i))
		regs[i] = bondReg(vals[i], twoMiB, ports.Hash{})
	}
	c := New(Config{Quorum: quorumFloor, MinBond: 1 << 20, ByzantineQuorum: byz}, func(ports.NodeID) int64 { return 0 })
	c.SetBondVerifier(objectiveVerify)
	g := &Block{Version: BlockVersion, Height: 0, Entries: []ports.Entry{entry(0)}, BondRegs: regs}
	Sign(g, vals[0])
	if err := c.AppendGenesis(*g); err != nil {
		t.Fatalf("genesis: %v", err)
	}
	return c, vals, g
}

// TestByzantineQuorumScalesWithValidatorSet: with the flag on, a commit needs the
// Byzantine attestation count over the qualified set — one short is rejected, exactly
// enough is accepted — and the requirement RISES with the set (a fixed 3 would not).
func TestByzantineQuorumScalesWithValidatorSet(t *testing.T) {
	const n = 10
	c, vals, g := objectiveSet(t, n, 1 /*floor*/, true /*byzantine*/)

	want := bftThreshold(n) // 10 → f=3 → 10-3-1 = 6 non-proposer attestations
	if got := c.RequiredQuorum(); got != want {
		t.Fatalf("RequiredQuorum with %d validators = %d, want the Byzantine threshold %d", n, got, want)
	}

	commit := func(nAtt int) *Block {
		b := &Block{Version: BlockVersion, Height: 1, Prev: g.Hash(), Entries: []ports.Entry{entry(1)}}
		Sign(b, vals[0]) // vals[0] proposes
		for i := 1; i <= nAtt; i++ {
			b.Atts = append(b.Atts, Attest(b, vals[i]))
		}
		return b
	}
	if err := c.ValidateCommit(commit(want - 1)); err == nil {
		t.Fatalf("a commit one attestation short of the Byzantine quorum (%d) must be REJECTED", want)
	}
	if err := c.ValidateCommit(commit(want)); err != nil {
		t.Fatalf("a commit at the Byzantine quorum (%d attestations) must be accepted: %v", want, err)
	}
}

// TestFixedQuorumUnsafeWithoutByzantineSizing is the inverted control: with the flag
// OFF, the SAME 10-validator set accepts a commit backed by just the fixed floor of
// 3 — support set 4 of 10, which two conflicting blocks could BOTH reach among 10
// (4+4 ≤ 10) without sharing an honest node. This is the hole H4 closes.
func TestFixedQuorumUnsafeWithoutByzantineSizing(t *testing.T) {
	const n = 10
	c, vals, g := objectiveSet(t, n, 3 /*fixed floor*/, false /*no byzantine sizing*/)
	if got := c.RequiredQuorum(); got != 3 {
		t.Fatalf("without the flag the requirement is the fixed floor 3, got %d", got)
	}
	b := &Block{Version: BlockVersion, Height: 1, Prev: g.Hash(), Entries: []ports.Entry{entry(1)}}
	Sign(b, vals[0])
	for i := 1; i <= 3; i++ {
		b.Atts = append(b.Atts, Attest(b, vals[i]))
	}
	if err := c.ValidateCommit(b); err != nil {
		t.Fatalf("setup: a fixed-quorum chain accepts a 3-attestation commit among 10 (the unsafe state): %v", err)
	}
	// Two disjoint 4-signer support sets fit in 10 with no overlap — quorum
	// intersection is NOT guaranteed, which is exactly why H4 sizes at the threshold.
	if 3+1+3+1 <= n {
		t.Logf("two disjoint support sets of size 4 fit in %d validators — no guaranteed honest overlap", n)
	}
}

// appendMatured appends a height-h block proposed by prop and attested by the given
// keys, satisfying the training wheels (an anchor among the attesters while immature).
func appendCommit(t *testing.T, c *Chain, prop ed25519.PrivateKey, prev *Block, attesters ...ed25519.PrivateKey) *Block {
	t.Helper()
	b := &Block{Version: BlockVersion, Height: prev.Height + 1, Prev: prev.Hash(), Entries: []ports.Entry{entry(byte(prev.Height + 1))}}
	Sign(b, prop)
	for _, a := range attesters {
		b.Atts = append(b.Atts, Attest(b, a))
	}
	if err := c.Append(*b); err != nil {
		t.Fatalf("append height %d: %v", b.Height, err)
	}
	return b
}

// TestMaturityNakamotoResistsOneOperator is the shed-metric exit criterion: one
// operator that spins up many keys cannot trip the training wheels off. Under the old
// head-count, 4 attesting validators would mature a MatureValidators=2 network; under
// the Nakamoto coefficient, an operator whose weight is dominated by ONE big bond has
// coefficient 1 and stays immature no matter how many satellite keys it adds — while a
// genuinely spread set matures.
func TestMaturityNakamotoResistsOneOperator(t *testing.T) {
	const minBond = int64(1) << 20
	// Two anchors bootstrap the young network: a1 proposes, a2 gives the anchor
	// sign-off the training wheels require while immature.
	a1, a2 := key(1), key(2)

	type bondedKey struct {
		priv ed25519.PrivateKey
		size int64
	}
	build := func(bonds []bondedKey) (*Chain, *Block) {
		cfg := Config{
			Quorum: 2, MinBond: minBond, MinProposerRep: 0, MinAttesterRep: 0,
			Anchors:          map[ports.NodeID]bool{idOf(a1): true, idOf(a2): true},
			AnchorQuorum:     1,
			MatureValidators: 2, // require Nakamoto coefficient ≥ 2 to shed
		}
		c := New(cfg, func(ports.NodeID) int64 { return 0 })
		c.SetBondVerifier(objectiveVerify)
		regs := []BondReg{bondReg(a1, minBond, ports.Hash{}), bondReg(a2, minBond, ports.Hash{})}
		for _, b := range bonds {
			regs = append(regs, bondReg(b.priv, b.size, ports.Hash{}))
		}
		g := &Block{Version: BlockVersion, Height: 0, Entries: []ports.Entry{entry(0)}, BondRegs: regs}
		Sign(g, a1)
		if err := c.AppendGenesis(*g); err != nil {
			t.Fatalf("genesis: %v", err)
		}
		return c, g
	}

	// One operator: 1 dominant 10-MiB bond + 3 satellite min bonds. Each takes a turn
	// attesting (with the anchor a2) so all four enter the participating set.
	dom, s1, s2, s3 := key(10), key(11), key(12), key(13)
	c, prev := build([]bondedKey{{dom, 10 << 20}, {s1, minBond}, {s2, minBond}, {s3, minBond}})
	for _, v := range []ed25519.PrivateKey{dom, s1, s2, s3} {
		prev = appendCommit(t, c, a1, prev, a2, v) // anchor a2 + one operator key
	}
	if c.Mature() {
		t.Fatal("H4 shed metric: an operator whose weight is dominated by one bond must NOT mature the network — Nakamoto coefficient is 1, four satellite keys or not")
	}

	// A genuinely decentralized set: three independent, equal 6-MiB validators. Once
	// all three have participated the coefficient reaches 2 and the wheels shed.
	v1, v2, v3 := key(20), key(21), key(22)
	c2, prev2 := build([]bondedKey{{v1, 6 << 20}, {v2, 6 << 20}, {v3, 6 << 20}})
	for _, v := range []ed25519.PrivateKey{v1, v2, v3} {
		prev2 = appendCommit(t, c2, a1, prev2, a2, v)
	}
	if !c2.Mature() {
		t.Fatal("H4 shed metric: three independent equal-weight validators SHOULD mature the network (Nakamoto coefficient 2)")
	}
}
