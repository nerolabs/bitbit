package diskproofs

import (
	"bytes"
	"testing"

	"github.com/nerolabs/silt/ports"
)

func TestRoundTripLoadDelete(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	mk := func(b byte, col int) (ports.ChunkID, ports.StorageProof) {
		var id, root, p0, p1 ports.Hash
		id[0], root[0], p0[0], p1[0] = b, b+1, b+2, b+3
		// PoR tags must persist too, or a restarted host re-announces a shard
		// it can no longer prove and gets slashed (#69 / Gate 4a).
		tag0, tag1 := make([]byte, 32), make([]byte, 32)
		tag0[0], tag1[0] = b+4, b+5
		return id, ports.StorageProof{Root: root, Index: int(b), Total: 8,
			Path: []ports.Hash{p0, p1}, Column: col, PorTags: [][]byte{tag0, tag1}}
	}

	id1, pr1 := mk(1, 3)
	id2, pr2 := mk(2, noColumnCol) // an uncoded/manifest-style proof (column -1)
	if err := s.Put(id1, pr1); err != nil {
		t.Fatalf("put1: %v", err)
	}
	if err := s.Put(id2, pr2); err != nil {
		t.Fatalf("put2: %v", err)
	}

	// A fresh handle on the same dir must see both — this is the restart path.
	s2, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	got, err := s2.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 proofs, got %d", len(got))
	}
	if g := got[id1]; g.Root != pr1.Root || g.Column != pr1.Column || g.Index != pr1.Index ||
		len(g.Path) != 2 || g.Path[0] != pr1.Path[0] || g.Path[1] != pr1.Path[1] {
		t.Fatalf("id1 proof round-trip mismatch: %+v vs %+v", g, pr1)
	}
	if g := got[id1]; len(g.PorTags) != 2 ||
		!bytes.Equal(g.PorTags[0], pr1.PorTags[0]) || !bytes.Equal(g.PorTags[1], pr1.PorTags[1]) {
		t.Fatalf("id1 PoR tags did not survive restart: got %v want %v", g.PorTags, pr1.PorTags)
	}
	if got[id2].Column != noColumnCol {
		t.Fatalf("id2 column round-trip: got %d want %d", got[id2].Column, noColumnCol)
	}

	if err := s2.Delete(id1); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := s2.Delete(id1); err != nil { // idempotent
		t.Fatalf("second delete should be a no-op, got: %v", err)
	}
	got, _ = s2.Load()
	if _, ok := got[id1]; ok {
		t.Fatalf("id1 should be gone after delete")
	}
	if _, ok := got[id2]; !ok {
		t.Fatalf("id2 should remain")
	}
}

const noColumnCol = -1
