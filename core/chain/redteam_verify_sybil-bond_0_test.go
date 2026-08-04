package chain

import (
	"crypto/ed25519"
	"testing"

	"github.com/nerolabs/silt/ports"
)

// Blind red-team F1 (Sybil, Critical) INVERTED as a regression: one plot (one
// bond ROOT) must NOT back N standings. The objective chain now enforces the same
// per-root dedup the credit ledger has (c.bondRootOwner) — a bond Root credits AT
// MOST ONE identity — so a colluding operator pointing N identities at one shared
// plot earns one bond's standing, not N. This is the attacker's own construction
// with the assertions flipped to DENIED.

// N identities sharing ONE plot root: only the first is bonded; the rest earn
// nothing, and a Sybil-only quorum off the shared root cannot commit.
func TestSharedRootDeniedNStandings(t *testing.T) {
	const n = 8
	const minBond = int64(4) << 20 // 4 MiB
	sharedRoot := ports.HashBytes([]byte("one-disk-one-plot"))

	sybils := make([]ed25519.PrivateKey, n)
	for i := range sybils {
		sybils[i] = key(int64(100 + i))
	}

	c := New(Config{Quorum: 3, MinBond: minBond}, func(ports.NodeID) int64 { return 0 })
	c.SetBondVerifier(objectiveVerify)

	g := &Block{Version: BlockVersion, Height: 0, Entries: []ports.Entry{entry(0)}}
	for _, s := range sybils {
		pub := s.Public().(ed25519.PublicKey)
		r := BondReg{Validator: append([]byte(nil), pub...), Root: sharedRoot, Size: minBond, Answer: []byte("valid")}
		r.Sig = ed25519.Sign(s, r.signingBytes(BondRegNonce(ports.Hash{})))
		g.BondRegs = append(g.BondRegs, r)
	}
	Sign(g, sybils[0])
	if err := c.AppendGenesis(*g); err != nil {
		t.Fatalf("genesis: %v", err)
	}

	// DENIED: only ONE identity is credited off the shared root.
	qualified, bondedTotal := 0, int64(0)
	for _, s := range sybils {
		id := idOf(s)
		if c.attesterQualified(id) {
			qualified++
		}
		bondedTotal += c.BondedSize(id)
	}
	if qualified != 1 {
		t.Fatalf("F1 regression: %d/%d identities qualified off one shared root, want exactly 1", qualified, n)
	}
	if bondedTotal != minBond {
		t.Fatalf("F1 regression: total bonded from one plot = %d, want exactly one bond (%d)", bondedTotal, minBond)
	}

	// DENIED: a follow-up block attested only by the shared-root Sybils cannot
	// reach quorum (all but the first carry zero weight).
	b := &Block{Version: BlockVersion, Height: 1, Prev: g.Hash(), Entries: []ports.Entry{entry(1)}}
	Sign(b, sybils[0])
	for _, a := range sybils[1:] {
		b.Atts = append(b.Atts, Attest(b, a))
	}
	if err := c.Append(*b); err == nil {
		t.Fatal("F1 regression: a shared-root Sybil quorum must NOT commit — one plot cannot buy a write quorum")
	}
}

// The non-genesis validated path: only ONE of the N shared-root registrations
// earns standing even though all pass validateBondRegs (sig + size + space-time).
func TestSharedRootDeniedViaValidatedBlock(t *testing.T) {
	const n = 4
	const minBond = int64(4) << 20
	sharedRoot := ports.HashBytes([]byte("one-plot-validated"))

	a1, a2 := key(1), key(2)
	id1, id2 := idOf(a1), idOf(a2)
	cfg := Config{Quorum: 1, MinBond: minBond,
		Anchors: map[ports.NodeID]bool{id1: true, id2: true}, MatureValidators: 100}
	c := New(cfg, func(ports.NodeID) int64 { return 0 })
	c.SetBondVerifier(objectiveVerify)

	g := &Block{Version: BlockVersion, Height: 0, Entries: []ports.Entry{entry(0)}}
	Sign(g, a1)
	if err := c.AppendGenesis(*g); err != nil {
		t.Fatalf("genesis: %v", err)
	}

	sybils := make([]ed25519.PrivateKey, n)
	for i := range sybils {
		sybils[i] = key(int64(300 + i))
	}
	nonce := BondRegNonce(g.Hash())
	b := &Block{Version: BlockVersion, Height: 1, Prev: g.Hash()}
	for _, s := range sybils {
		pub := s.Public().(ed25519.PublicKey)
		r := BondReg{Validator: append([]byte(nil), pub...), Root: sharedRoot, Size: minBond, Answer: []byte("valid")}
		r.Sig = ed25519.Sign(s, r.signingBytes(nonce))
		b.BondRegs = append(b.BondRegs, r)
	}
	Sign(b, a1)                            // proposer = anchor a1 (launch-eligible while immature)
	b.Atts = append(b.Atts, Attest(b, a2)) // attester = anchor a2
	if err := c.Append(*b); err != nil {
		t.Fatalf("the block (anchor quorum) should still commit: %v", err)
	}

	bonded := 0
	for _, s := range sybils {
		if c.BondedSize(idOf(s)) == minBond {
			bonded++
		}
	}
	if bonded != 1 {
		t.Fatalf("F1 regression: %d/%d shared-root Sybils bonded, want exactly 1 (per-root dedup)", bonded, n)
	}
}
