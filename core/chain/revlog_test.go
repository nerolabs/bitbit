package chain

import (
	"testing"

	"github.com/nerolabs/silt/core/translog"
	"github.com/nerolabs/silt/ports"
)

// commitRevBlock commits a revocation/un-revocation block at the head and returns
// the height it landed at.
func (w *world) commitRevBlock(t *testing.T, revs, unrevs []ports.Hash) uint64 {
	t.Helper()
	prev, height := w.c.Head()
	b := &Block{Version: BlockVersion, Height: height, Prev: prev, Revocations: revs, Unrevocations: unrevs}
	Sign(b, w.prop)
	w.attestAll(b)
	if err := w.c.Append(*b); err != nil {
		t.Fatalf("append rev block at %d: %v", height, err)
	}
	return height
}

// TestRevocationTransparencyLog: every honored revocation and un-revocation is
// appended to the chain's CT-style log in commit order, each provably included, and
// the log is provably append-only across commits — so a takedown can be audited and
// history cannot be silently rewritten.
func TestRevocationTransparencyLog(t *testing.T) {
	w := newWorld(DefaultConfig())
	// Publish two roots so there is something to take down.
	b := w.block(entry(1), entry(2))
	w.attestAll(b)
	if err := w.c.Append(*b); err != nil {
		t.Fatal(err)
	}
	r1, r2 := entry(1).Root, entry(2).Root

	if w.c.RevocationLogSize() != 0 {
		t.Fatal("log should be empty before any takedown")
	}

	// Three takedown events, in order: revoke r1, revoke r2, then un-revoke r1.
	type ev struct {
		op     byte
		root   ports.Hash
		height uint64
	}
	var events []ev
	h1 := w.commitRevBlock(t, []ports.Hash{r1}, nil)
	events = append(events, ev{RevOp, r1, h1})
	h2 := w.commitRevBlock(t, []ports.Hash{r2}, nil)
	events = append(events, ev{RevOp, r2, h2})
	h3 := w.commitRevBlock(t, nil, []ports.Hash{r1})
	events = append(events, ev{UnrevOp, r1, h3})

	size := w.c.RevocationLogSize()
	if size != len(events) {
		t.Fatalf("log size = %d, want %d", size, len(events))
	}
	root := w.c.RevocationLogRoot()

	// Every event is provably included, and an auditor reconstructs the leaf from
	// public block data (op, root, height) via the exported RevocationLeaf.
	for i, e := range events {
		proof, err := w.c.RevocationInclusionProof(i, size)
		if err != nil {
			t.Fatalf("inclusion proof %d: %v", i, err)
		}
		leaf := RevocationLeaf(e.op, e.root, e.height)
		if !translog.VerifyInclusion(leaf, i, size, root, proof) {
			t.Fatalf("takedown event %d (%c %x@%d) did not verify inclusion", i, e.op, e.root[:4], e.height)
		}
		// A different event must NOT verify at this position.
		if translog.VerifyInclusion(RevocationLeaf(UnrevOp, e.root, e.height+1), i, size, root, proof) {
			t.Fatalf("a forged leaf verified at position %d", i)
		}
	}

	// Append-only: every historical root is provably a prefix of the current one.
	for m := 0; m <= size; m++ {
		oldRoot, err := w.c.RevocationLogRootAt(m)
		if err != nil {
			t.Fatalf("RootAt(%d): %v", m, err)
		}
		proof, err := w.c.RevocationConsistencyProof(m, size)
		if err != nil {
			t.Fatalf("consistency %d→%d: %v", m, size, err)
		}
		if !translog.VerifyConsistency(oldRoot, m, root, size, proof) {
			t.Fatalf("consistency %d→%d did not verify — the log must be provably append-only", m, size)
		}
	}
}

// TestRevocationLogDeterministicAcrossReplay: the log is a pure function of the
// committed blocks, so a replica that re-applies the same history (SyncChain/adopt)
// arrives at the identical log root — otherwise auditors on different replicas would
// disagree on what was taken down.
func TestRevocationLogDeterministicAcrossReplay(t *testing.T) {
	build := func() ports.Hash {
		w := newWorld(DefaultConfig())
		b := w.block(entry(1), entry(2))
		w.attestAll(b)
		if err := w.c.Append(*b); err != nil {
			t.Fatal(err)
		}
		w.commitRevBlock(t, []ports.Hash{entry(1).Root}, nil)
		w.commitRevBlock(t, []ports.Hash{entry(2).Root}, nil)
		w.commitRevBlock(t, nil, []ports.Hash{entry(1).Root})
		return w.c.RevocationLogRoot()
	}
	if build() != build() {
		t.Fatal("the revocation log root is not deterministic across identical histories")
	}
}
