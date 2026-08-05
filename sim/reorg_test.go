package sim

import (
	"testing"

	"github.com/nerolabs/silt/adapters/identity"
	"github.com/nerolabs/silt/adapters/memstore"
	"github.com/nerolabs/silt/adapters/simclock"
	"github.com/nerolabs/silt/adapters/simnet"
	"github.com/nerolabs/silt/core/chain"
	"github.com/nerolabs/silt/core/credit"
	"github.com/nerolabs/silt/core/genesis"
	"github.com/nerolabs/silt/core/node"
	"github.com/nerolabs/silt/ports"
)

// TestPartitionHealsToHeavierFork is the D2 integration test: a network splits,
// each side commits its OWN block at the same height (a fork the old chain
// could never resolve — "first valid block wins, no reorg"), then the partition
// heals and the lighter side reorgs onto the heavier fork over the wire, while
// the heavier side does not budge. Consensus reconverges on one history.
func TestPartitionHealsToHeavierFork(t *testing.T) {
	const (
		seed = int64(42)
		N    = 10
	)
	ledger := credit.New(50_000, 0)
	repFn := func(n ports.NodeID) int64 { return ledger.Reputation(n) }
	cfg := chain.DefaultConfig() // quorum 3, rep 100
	nodeCfg := node.DefaultConfig()

	sched := simclock.New()
	net := simnet.New(sched, seed, simnet.DefaultConfig())

	var nodes []*node.Node
	var ids []ports.NodeID
	for i := 0; i < N; i++ {
		ident := identity.FromSeed(seed*1000 + int64(i))
		id := ident.NodeID()
		st := memstore.New()
		nd := node.New(id, nodeCfg, sched, net.Endpoint(id), st)
		nd.SetLedger(ledger)
		ch := chain.New(cfg, repFn)
		if gb, _, _, gerr := genesis.Build(st); gerr == nil { // identical genesis on every node
			ch.AppendGenesis(gb)
		}
		nd.EnableChain(ch, ident.Signer())
		// Earn standing via the BOND press (a distinct root per node) so each is a
		// qualified validator — PoR audits no longer mint standing (H1).
		ledger.RecordBondChallenge(id, ports.HashBytes([]byte{byte(i), 0xb0}), 64<<20, true, 1)
		nodes = append(nodes, nd)
		ids = append(ids, id)
	}
	for i := 1; i < N; i++ {
		nodes[i].Bootstrap([]ports.NodeID{ids[0]}, func() {})
	}
	sched.Run()

	// Split: group A = 4 nodes, group B = 6 nodes; the two sides cannot talk.
	groupA, groupB := ids[0:4], ids[4:10]
	net.Partition(groupB...)

	// Each block commits with exactly `quorum` attestations, so the heavier
	// history is the one that made MORE progress: group A commits ONE block,
	// group B commits TWO — cumulative weight 6 vs 3.
	if err := propose(nodes[0], "forkA", groupA[1:4], groupA, cfg.Quorum, sched); err != nil {
		t.Fatalf("group A commit: %v", err)
	}
	if err := propose(nodes[4], "forkB1", groupB[1:4], groupB, cfg.Quorum, sched); err != nil {
		t.Fatalf("group B block 1: %v", err)
	}
	if err := propose(nodes[4], "forkB2", groupB[1:4], groupB, cfg.Quorum, sched); err != nil {
		t.Fatalf("group B block 2: %v", err)
	}

	headA, _ := nodes[0].Chain().Head()
	headB, _ := nodes[4].Chain().Head()
	if nodes[0].Chain().Len() != 2 || nodes[4].Chain().Len() != 3 {
		t.Fatalf("setup: A should hold genesis+1, B genesis+2 (A=%d B=%d)", nodes[0].Chain().Len(), nodes[4].Chain().Len())
	}
	if headA == headB {
		t.Fatal("setup: the partition should have produced two DIFFERENT histories")
	}

	// Heal, and let the lighter side reconcile from a heavier-side peer.
	net.ClearPartition()
	if err := runSync(nodes[0], ids[4], sched); err != nil {
		t.Fatalf("A syncing from B: %v", err)
	}

	// Group A reorged onto the heavier fork B.
	if newHeadA, _ := nodes[0].Chain().Head(); newHeadA != headB {
		t.Fatal("the lighter partition must reorg onto the heavier fork after healing")
	}
	if nodes[0].Chain().Len() != 3 {
		t.Fatalf("group A should now hold B's full 2-block history (len=%d)", nodes[0].Chain().Len())
	}
	for _, name := range []string{"forkB1", "forkB2"} {
		if _, ok := nodes[0].Chain().LookupRoot(ports.HashBytes([]byte(name))); !ok {
			t.Fatalf("group A should now hold fork B's entry %q", name)
		}
	}
	if _, ok := nodes[0].Chain().LookupRoot(ports.HashBytes([]byte("forkA"))); ok {
		t.Fatal("group A's abandoned entry must be gone after the reorg")
	}

	// The heavier side must NOT switch to the lighter fork.
	if err := runSync(nodes[4], ids[0], sched); err != nil {
		t.Fatalf("B syncing from A: %v", err)
	}
	if headB2, _ := nodes[4].Chain().Head(); headB2 != headB {
		t.Fatal("the heavier partition must not adopt the lighter fork")
	}
}

// runSync drives one SyncChain against a single peer to completion.
func runSync(n *node.Node, peer ports.NodeID, sched *simclock.Scheduler) error {
	var serr error
	done := false
	n.SyncChain([]ports.NodeID{peer}, func(_ int, err error) { serr = err; done = true })
	sched.Run()
	if !done {
		return errString("sync never completed")
	}
	return serr
}
