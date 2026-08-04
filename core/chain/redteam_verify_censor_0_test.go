package chain

import (
	"errors"
	"testing"

	"github.com/nerolabs/silt/ports"
)

// Blind red-team F3 (Accountability) INVERTED as a regression: a genesis block
// must NOT be able to pre-emptively revoke a never-published root. AppendGenesis
// now rejects any genesis carrying takedowns (ErrGenesisTakedown), closing the
// path that bypassed the ErrRevokeUnknownRoot existence guard — so a takedown can
// only name an already-committed root, on the governed normal path (immutable #5,
// "never pre-emptive").
func TestGenesisPreemptiveRevocationDenied(t *testing.T) {
	neverPublished := ports.HashBytes([]byte("competitor-future-root"))

	// DENIED: a genesis carrying a revocation is rejected outright.
	g := &Block{Version: BlockVersion, Height: 0, Prev: ports.Hash{},
		Entries: []ports.Entry{entry(1)}, Revocations: []ports.Hash{neverPublished}}
	Sign(g, key(1))
	c := New(DefaultConfig(), func(ports.NodeID) int64 { return 1000 })
	if err := c.AppendGenesis(*g); !errors.Is(err, ErrGenesisTakedown) {
		t.Fatalf("F3 regression: genesis with a revocation must be rejected (ErrGenesisTakedown), got: %v", err)
	}
	if c.Revoked(neverPublished) {
		t.Fatal("F3 regression: a never-published root must not be revoked (the bogus genesis was rejected)")
	}

	// An un-revocation in genesis is likewise rejected.
	g2 := &Block{Version: BlockVersion, Height: 0, Prev: ports.Hash{},
		Entries: []ports.Entry{entry(1)}, Unrevocations: []ports.Hash{neverPublished}}
	Sign(g2, key(1))
	if err := New(DefaultConfig(), func(ports.NodeID) int64 { return 0 }).AppendGenesis(*g2); !errors.Is(err, ErrGenesisTakedown) {
		t.Fatalf("F3 regression: genesis with an un-revocation must be rejected, got: %v", err)
	}

	// A clean genesis (entries only) still works.
	clean := &Block{Version: BlockVersion, Height: 0, Prev: ports.Hash{}, Entries: []ports.Entry{entry(2)}}
	Sign(clean, key(1))
	if err := New(DefaultConfig(), func(ports.NodeID) int64 { return 0 }).AppendGenesis(*clean); err != nil {
		t.Fatalf("a clean genesis must still be accepted: %v", err)
	}

	// CONTRAST: the existence guard still fires on the normal (non-genesis) path.
	w := newWorld(DefaultConfig())
	gc := &Block{Version: BlockVersion, Height: 0, Prev: ports.Hash{}, Entries: []ports.Entry{entry(3)}}
	Sign(gc, w.prop)
	if err := w.c.AppendGenesis(*gc); err != nil {
		t.Fatalf("setup: clean genesis rejected: %v", err)
	}
	prev, h := w.c.Head()
	nb := &Block{Version: BlockVersion, Height: h, Prev: prev, Revocations: []ports.Hash{neverPublished}}
	Sign(nb, w.prop)
	if err := w.c.ValidateProposal(nb); !errors.Is(err, ErrRevokeUnknownRoot) {
		t.Fatalf("normal-path revocation of an unknown root must be ErrRevokeUnknownRoot, got: %v", err)
	}
}
