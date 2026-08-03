package chain

import (
	"testing"

	"github.com/nerolabs/silt/ports"
)

// genesisBlock builds and appends a declared genesis, returning it so forks
// can branch from it.
func (w *world) genesis() *Block {
	g := &Block{Version: BlockVersion, Height: 0, Entries: []ports.Entry{entry(0)}}
	Sign(g, w.prop)
	if err := w.c.AppendGenesis(*g); err != nil {
		panic(err)
	}
	return g
}

// forkBlock builds a height-1 block on top of prev with the given entry,
// attested by the first `nAtt` validators — so its fork-choice weight is nAtt.
func (w *world) forkBlock(prev ports.Hash, e ports.Entry, nAtt int) *Block {
	b := &Block{Version: BlockVersion, Height: 1, Prev: prev, Entries: []ports.Entry{e}}
	Sign(b, w.prop)
	for _, v := range w.vals[:nAtt] {
		b.Atts = append(b.Atts, Attest(b, v))
	}
	return b
}

// The heal: a replica committed to a lighter fork adopts a heavier valid one
// that shares its genesis, and rolls its state off the abandoned fork.
func TestReconcileAdoptsHeavierFork(t *testing.T) {
	w := newWorld(DefaultConfig())
	g := w.genesis()

	light := w.forkBlock(g.Hash(), entry(1), 3) // weight 3
	heavy := w.forkBlock(g.Hash(), entry(2), 4) // weight 4 — heavier

	if err := w.c.Append(*light); err != nil {
		t.Fatalf("append light fork: %v", err)
	}
	if _, ok := w.c.LookupRoot(entry(1).Root); !ok {
		t.Fatal("setup: light fork's entry should be present")
	}

	adopted, err := w.c.Reconcile([]Block{*g, *heavy})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if !adopted {
		t.Fatal("a strictly heavier fork must be adopted")
	}
	if _, ok := w.c.LookupRoot(entry(2).Root); !ok {
		t.Fatal("heavier fork's entry should now be present")
	}
	if _, ok := w.c.LookupRoot(entry(1).Root); ok {
		t.Fatal("the abandoned fork's entry must be gone after the reorg")
	}
	if w.c.Weight() != 4 {
		t.Fatalf("weight after reorg = %d, want 4", w.c.Weight())
	}
}

// A lighter (or equal-but-not-lower-hash) fork is NOT adopted — we don't
// thrash off a heavier history.
func TestReconcileRejectsLighterFork(t *testing.T) {
	w := newWorld(DefaultConfig())
	g := w.genesis()

	heavy := w.forkBlock(g.Hash(), entry(1), 4)
	light := w.forkBlock(g.Hash(), entry(2), 3)

	if err := w.c.Append(*heavy); err != nil {
		t.Fatalf("append heavy: %v", err)
	}
	adopted, err := w.c.Reconcile([]Block{*g, *light})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if adopted {
		t.Fatal("a lighter fork must not displace a heavier committed history")
	}
	if _, ok := w.c.LookupRoot(entry(1).Root); !ok {
		t.Fatal("the heavier committed entry must remain")
	}
}

// Fork-choice must be deterministic on a weight tie: every honest replica
// breaks the tie the same way (lower head hash), so the network converges
// rather than splitting on which equal fork to keep.
func TestReconcileTieBreakDeterministic(t *testing.T) {
	// Two equal-weight forks; whichever has the lower head hash must win from
	// BOTH starting points.
	mkForks := func() (g, a, b *Block) {
		w := newWorld(DefaultConfig())
		g = w.genesis()
		a = w.forkBlock(g.Hash(), entry(1), 4)
		b = w.forkBlock(g.Hash(), entry(2), 4)
		return
	}
	_, a, b := mkForks()
	ah, bh := a.Hash(), b.Hash()
	lo, hi := a, b
	if bytesLess(bh[:], ah[:]) {
		lo, hi = b, a
	}

	// A replica on the HIGHER-hash fork must adopt the lower-hash one.
	w1 := newWorld(DefaultConfig())
	g1 := w1.genesis()
	if err := w1.c.Append(*hi); err != nil {
		t.Fatalf("append hi: %v", err)
	}
	if adopted, err := w1.c.Reconcile([]Block{*g1, *lo}); err != nil || !adopted {
		t.Fatalf("equal-weight lower-hash fork should win the tiebreak (adopted=%v err=%v)", adopted, err)
	}

	// A replica already on the LOWER-hash fork must NOT switch.
	w2 := newWorld(DefaultConfig())
	g2 := w2.genesis()
	if err := w2.c.Append(*lo); err != nil {
		t.Fatalf("append lo: %v", err)
	}
	if adopted, _ := w2.c.Reconcile([]Block{*g2, *hi}); adopted {
		t.Fatal("must not switch off the lower-hash fork to an equal higher-hash one")
	}
}

// A fork that does not share our genesis is refused outright — a peer cannot
// swap in a heavier FOREIGN history.
func TestReconcileRefusesForeignGenesis(t *testing.T) {
	w := newWorld(DefaultConfig())
	g := w.genesis()
	if err := w.c.Append(*w.forkBlock(g.Hash(), entry(1), 4)); err != nil {
		t.Fatalf("append: %v", err)
	}

	// A different genesis (different entry) + a heavy block on it.
	other := newWorld(DefaultConfig())
	og := &Block{Version: BlockVersion, Height: 0, Entries: []ports.Entry{entry(9)}}
	Sign(og, other.prop)
	oheavy := other.forkBlock(og.Hash(), entry(8), 4)

	adopted, err := w.c.Reconcile([]Block{*og, *oheavy})
	if err != ErrForeignGenesis {
		t.Fatalf("expected ErrForeignGenesis, got adopted=%v err=%v", adopted, err)
	}
}

// A heavier-LOOKING but INVALID fork (a block without a real quorum) is
// re-validated end to end and rejected — a lying peer cannot feed us garbage.
func TestReconcileRejectsInvalidFork(t *testing.T) {
	w := newWorld(DefaultConfig())
	g := w.genesis()
	if err := w.c.Append(*w.forkBlock(g.Hash(), entry(1), 3)); err != nil {
		t.Fatalf("append: %v", err)
	}

	// A fork block at height 1 with only ONE attestation — below Quorum=3.
	bad := w.forkBlock(g.Hash(), entry(2), 1)

	adopted, err := w.c.Reconcile([]Block{*g, *bad})
	if adopted || err == nil {
		t.Fatalf("an under-quorum fork must be rejected, got adopted=%v err=%v", adopted, err)
	}
	if _, ok := w.c.LookupRoot(entry(1).Root); !ok {
		t.Fatal("our valid chain must survive a rejected invalid fork")
	}
}
