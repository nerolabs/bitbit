package node

import (
	"testing"

	"github.com/nerolabs/silt/adapters/memproofs"
	"github.com/nerolabs/silt/adapters/memstore"
	"github.com/nerolabs/silt/adapters/simclock"
	"github.com/nerolabs/silt/ports"
)

func has(ids []ports.NodeID, want ports.NodeID) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

// TestReprovideAfterRestartUsesColumnKey is the #69 regression. A restarting
// daemon re-announces everything on its disk (AnnounceHeld), but a coded shard
// must be announced under its COLUMN key hash(root‖column) — where readers
// look — which it can only derive from that shard's proof. Proofs used to live
// only in memory, so after a restart AnnounceHeld fell back to the bare chunk
// id and the content was undiscoverable. With the proof persisted and reloaded
// (LoadProofs), the re-announce lands on the right key again.
func TestReprovideAfterRestartUsesColumnKey(t *testing.T) {
	// A hosted coded shard and the proof that arrived with it, as they would
	// be on disk after the original placement.
	chunk := ports.NewChunk([]byte("a coded shard"))
	var root ports.Hash
	root[0], root[1] = 0xAB, 0xCD
	const col = 3
	proof := ports.StorageProof{Root: root, Index: 0, Total: 1, Column: col}

	// The persistent store + proof store survive the "restart" (same objects).
	store := memstore.New()
	proofs := memproofs.New()
	store.Put(bg(), chunk)
	proofs.Put(chunk.ID, proof)

	newNode := func(withProofs bool) *Node {
		sched := simclock.New()
		var id ports.NodeID
		id[0] = 9
		ln := &linkNet{sched: sched, ends: map[ports.NodeID]*linkEnd{}}
		end := &linkEnd{net: ln, id: id}
		ln.ends[id] = end
		n := New(id, DefaultConfig(), sched, end, store)
		if withProofs {
			n.SetProofStore(proofs)
			n.LoadProofs()
		}
		done := false
		n.AnnounceHeld(func(int) { done = true })
		sched.Run()
		if !done {
			t.Fatal("AnnounceHeld did not finish")
		}
		return n
	}

	colendkey := colKey(root, col)

	// With the reloaded proof: announced under the column key (correct).
	n := newNode(true)
	if !has(n.provs.IDs(colendkey), n.id) {
		t.Fatalf("after restart, expected self as provider under the column key")
	}
	if has(n.provs.IDs(ports.Hash(chunk.ID)), n.id) {
		t.Fatalf("must NOT announce a coded shard under its bare id (the #69 bug)")
	}

	// Without the persisted proof (the old behavior): it lands on the bare id
	// — undiscoverable to a reader walking to the column key. This is what the
	// persistence fixes.
	bug := newNode(false)
	if has(bug.provs.IDs(colendkey), bug.id) {
		t.Fatalf("without the proof it should not find the column key")
	}
	if !has(bug.provs.IDs(ports.Hash(chunk.ID)), bug.id) {
		t.Fatalf("without the proof the old code announces under the bare id")
	}
}
