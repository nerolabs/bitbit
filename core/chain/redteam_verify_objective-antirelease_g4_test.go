package chain

import (
	"crypto/ed25519"
	"errors"
	"testing"

	"github.com/nerolabs/silt/ports"
)

// bondRegAt mints a signed BondReg for key s over root, bound to prev's nonce.
func bondRegAt(s ed25519.PrivateKey, root ports.Hash, size int64, prev ports.Hash) BondReg {
	r := BondReg{Validator: pubOf(s), Root: root, Size: size, Answer: []byte("valid")}
	r.Sig = ed25519.Sign(s, r.signingBytes(BondRegNonce(prev)))
	return r
}

// Retest G4 part (a/b) INVERTED: a sub-floor bond earns NO objective standing.
// The anti-release floor (Config.MinBondBytes) now gates the objective set, so a
// plot too small to survive the challenge window (releasable + re-plottable) buys
// no fork-choice weight — whether it arrives declared at genesis or proven on the
// normal path.
func TestObjectiveSubFloorBondEarnsNoStanding(t *testing.T) {
	const floor = int64(1) << 30 // 1 GiB anti-release floor
	const tiny = int64(1) << 20  // 1 MiB — sub-floor, releasable

	c := New(Config{Quorum: 1, MinBond: 1 << 20, MinBondBytes: floor}, func(ports.NodeID) int64 { return 0 })
	c.SetBondVerifier(objectiveVerify)

	// A genesis DECLARING a tiny bond: it must earn zero standing.
	sybil := key(500)
	g := &Block{Version: BlockVersion, Height: 0, Entries: []ports.Entry{entry(0)},
		BondRegs: []BondReg{bondRegAt(sybil, ports.HashBytes([]byte("tiny")), tiny, ports.Hash{})}}
	Sign(g, sybil)
	if err := c.AppendGenesis(*g); err != nil {
		t.Fatalf("genesis: %v", err)
	}
	if got := c.BondedSize(idOf(sybil)); got != 0 {
		t.Fatalf("G4 regression: sub-floor genesis bond credited %d objective standing, want 0", got)
	}
	if c.attesterQualified(idOf(sybil)) {
		t.Fatal("G4 regression: sub-floor bond is attester-qualified in the objective set")
	}

	// The normal path rejects a sub-floor registration outright (fail-closed).
	anchorP, anchorA := key(10), key(11)
	c2 := New(Config{Quorum: 1, MinBond: 1 << 20, MinBondBytes: floor,
		Anchors: map[ports.NodeID]bool{idOf(anchorP): true, idOf(anchorA): true}, MatureValidators: 100},
		func(ports.NodeID) int64 { return 0 })
	c2.SetBondVerifier(objectiveVerify)
	g2 := &Block{Version: BlockVersion, Height: 0, Entries: []ports.Entry{entry(0)}}
	Sign(g2, anchorP)
	if err := c2.AppendGenesis(*g2); err != nil {
		t.Fatalf("genesis2: %v", err)
	}
	b := &Block{Version: BlockVersion, Height: 1, Prev: g2.Hash(),
		BondRegs: []BondReg{bondRegAt(sybil, ports.HashBytes([]byte("tiny2")), tiny, g2.Hash())}}
	Sign(b, anchorP)
	b.Atts = []Attestation{Attest(b, anchorA)}
	if err := c2.Append(*b); !errors.Is(err, ErrBadBondReg) {
		t.Fatalf("G4 regression: a sub-floor normal-path registration must be rejected (ErrBadBondReg), got: %v", err)
	}
}

// Retest G4 part (c) INVERTED: objective standing DECAYS if not renewed, so a
// validator that proves once and RELEASES its plot cannot keep voting. A validator
// that RENEWS with a fresh proof within the TTL keeps its standing.
func TestObjectiveBondStandingDecaysWithoutRenewal(t *testing.T) {
	const ttl = uint64(2)
	const bond = int64(4) << 20

	anchorP, anchorA := key(10), key(11)
	anchors := map[ports.NodeID]bool{idOf(anchorP): true, idOf(anchorA): true}
	cfg := Config{Quorum: 1, MinBond: bond, BondTTLBlocks: ttl, Anchors: anchors, MatureValidators: 100}

	newChain := func() (*Chain, *Block) {
		c := New(cfg, func(ports.NodeID) int64 { return 0 })
		c.SetBondVerifier(objectiveVerify)
		g := &Block{Version: BlockVersion, Height: 0, Entries: []ports.Entry{entry(0)}}
		Sign(g, anchorP)
		if err := c.AppendGenesis(*g); err != nil {
			t.Fatalf("genesis: %v", err)
		}
		return c, g
	}

	// Anchors advance the chain by one empty block; optionally V renews its bond.
	advance := func(c *Chain, prev *Block, regs []BondReg) *Block {
		nb := &Block{Version: BlockVersion, Height: prev.Height + 1, Prev: prev.Hash(),
			Entries: []ports.Entry{entry(byte(prev.Height + 1))}, BondRegs: regs}
		Sign(nb, anchorP)
		nb.Atts = []Attestation{Attest(nb, anchorA)}
		if err := c.Append(*nb); err != nil {
			t.Fatalf("advance to height %d: %v", prev.Height+1, err)
		}
		return nb
	}

	V := key(700)
	R := ports.HashBytes([]byte("V-real-plot"))

	// --- No renewal: standing lapses after TTL blocks. ---
	c, g := newChain()
	b1 := advance(c, g, []BondReg{bondRegAt(V, R, bond, g.Hash())}) // V registers at height 1
	if got := c.BondedSize(idOf(V)); got != bond {
		t.Fatalf("V should be bonded after registering: got %d", got)
	}
	b2 := advance(c, b1, nil) // height 2: 2-1=1 <= ttl → still bonded
	if c.BondedSize(idOf(V)) != bond {
		t.Fatal("V must keep standing within the TTL window (height 2)")
	}
	b3 := advance(c, b2, nil) // height 3: 3-1=2 <= ttl → still bonded
	if c.BondedSize(idOf(V)) != bond {
		t.Fatal("V must keep standing at the TTL boundary (height 3)")
	}
	advance(c, b3, nil) // height 4: 4-1=3 > ttl → lapses
	if got := c.BondedSize(idOf(V)); got != 0 {
		t.Fatalf("G4 regression: V retained %d standing after releasing (no renewal past TTL), want 0", got)
	}
	if c.attesterQualified(idOf(V)) {
		t.Fatal("G4 regression: a lapsed bond is still attester-qualified")
	}

	// --- Renewal within the window keeps standing alive past the original expiry. ---
	c, g = newChain()
	b1 = advance(c, g, []BondReg{bondRegAt(V, R, bond, g.Hash())}) // register at height 1
	b2 = advance(c, b1, nil)                                       // height 2
	// Renew at height 3 (within window: 3-1=2 <= ttl), resetting the clock.
	b3 = advance(c, b2, []BondReg{bondRegAt(V, R, bond, b2.Hash())})
	if c.BondedSize(idOf(V)) != bond {
		t.Fatal("V must be bonded right after renewal (height 3)")
	}
	b4 := advance(c, b3, nil) // height 4: 4-3=1 <= ttl → alive (would have lapsed without renewal)
	if got := c.BondedSize(idOf(V)); got != bond {
		t.Fatalf("G4 regression: a RENEWED bond lapsed early: got %d at height 4, want %d", got, bond)
	}
	_ = b4
}
