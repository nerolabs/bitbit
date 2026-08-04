package chain

import (
	"crypto/ed25519"
	"errors"
	"testing"

	"github.com/nerolabs/silt/ports"
)

// Retest G1 (Accountability, Critical) INVERTED as a regression: a genesis block
// must NOT be able to carry an equivocation Slash. AppendGenesis skips
// validateSlashes and apply() unconditionally evicts every Slashes culprit, so an
// UNVERIFIED genesis Slash was a proof-free, pre-emptive, identity-level kill
// switch — the F3 door (closed for Revocations) left open for the stronger lever.
// AppendGenesis now rejects any genesis carrying Slashes (ErrGenesisTakedown).
func TestGenesisBogusSlashDenied(t *testing.T) {
	w := newWorld(DefaultConfig())
	victim := idOf(w.vals[0])

	// A FORGED accusation: the victim signed neither block, so the self-verifying
	// proof must reject it — yet even a VALID proof must not ride in via genesis.
	a := &Block{Version: BlockVersion, Height: 1, Entries: []ports.Entry{entry(1)}}
	Sign(a, w.prop)
	b := &Block{Version: BlockVersion, Height: 1, Entries: []ports.Entry{entry(2)}}
	Sign(b, w.prop)
	bogus := Equivocation{Culprit: pubOf(w.vals[0]), A: *a, B: *b}
	if VerifyEquivocation(&bogus) {
		t.Fatal("precondition: the forged slash must not verify")
	}

	// DENIED: a genesis carrying the slash is rejected outright.
	g := &Block{Version: BlockVersion, Height: 0, Entries: []ports.Entry{entry(0)},
		Slashes: []Equivocation{bogus}}
	Sign(g, w.prop)
	if err := w.c.AppendGenesis(*g); !errors.Is(err, ErrGenesisTakedown) {
		t.Fatalf("G1 regression: genesis with a Slash must be rejected (ErrGenesisTakedown), got: %v", err)
	}
	if w.c.IsSlashed(victim) {
		t.Fatal("G1 regression: the victim must not be slashed (the bogus genesis was rejected)")
	}

	// A clean genesis (no slashes) still works, and the victim keeps full standing.
	clean := &Block{Version: BlockVersion, Height: 0, Entries: []ports.Entry{entry(0)}}
	Sign(clean, w.prop)
	if err := w.c.AppendGenesis(*clean); err != nil {
		t.Fatalf("a clean genesis must still be accepted: %v", err)
	}
	if w.c.IsSlashed(victim) || !w.c.attesterQualified(victim) {
		t.Fatal("the victim must retain standing after a clean genesis")
	}

	// CONTRAST: the normal (non-genesis) path still slashes on a REAL proof —
	// the guard closes the genesis door without disarming legitimate slashing.
	real := newWorld(DefaultConfig())
	gc := &Block{Version: BlockVersion, Height: 0, Prev: ports.Hash{}, Entries: []ports.Entry{entry(0)}}
	Sign(gc, real.prop)
	if err := real.c.AppendGenesis(*gc); err != nil {
		t.Fatalf("setup: clean genesis rejected: %v", err)
	}
	ra, rb := real.conflicting(gc, real.prop, real.vals[3],
		[]ed25519.PrivateKey{real.vals[0]}, []ed25519.PrivateKey{real.vals[0]})
	proven := Equivocation{Culprit: pubOf(real.vals[0]), A: *ra, B: *rb}
	if !VerifyEquivocation(&proven) {
		t.Fatal("setup: the real double-sign proof must verify")
	}
	if err := real.c.validateSlashes(&Block{Slashes: []Equivocation{proven}}); err != nil {
		t.Fatalf("a real equivocation proof must pass validateSlashes on the normal path: %v", err)
	}
}
