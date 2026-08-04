package sim

import (
	"crypto/ed25519"
	"testing"

	"github.com/nerolabs/silt/adapters/identity"
	"github.com/nerolabs/silt/adapters/memstore"
	"github.com/nerolabs/silt/adapters/simclock"
	"github.com/nerolabs/silt/adapters/simnet"
	"github.com/nerolabs/silt/core/chain"
	"github.com/nerolabs/silt/core/credit"
	"github.com/nerolabs/silt/core/node"
	"github.com/nerolabs/silt/ports"
)

// Integration proof for M0 consensus F6 (objective fork-choice). This is the
// red-team's non-healing-partition scenario, INVERTED: unlike sim/reorg_test.go
// — which shares ONE ledger across all nodes and so hides the subjectivity — each
// node here holds its OWN (empty) ledger, so the local reputation view is
// useless. With objective mode on (Config.MinBond > 0 + a wired bond verifier +
// genesis-seeded bonds), proposer/attester eligibility, quorum, and fork-choice
// weight come from the on-chain bond, identical on every replica — so a
// partitioned network still commits and, on healing, converges on the
// heavier-bond fork. In legacy mode these nodes could not even propose (rep 0).
func TestObjectiveForkChoiceHealsWithSeparateLedgers(t *testing.T) {
	const (
		seed     = int64(7)
		N        = 10
		bondSize = int64(64) << 20
	)
	cfg := chain.Config{Quorum: 3, MinBond: 1 << 20} // OBJECTIVE fork-choice

	sched := simclock.New()
	net := simnet.New(sched, seed, simnet.DefaultConfig())

	// Genesis declares the launch validator set's bonds (the training-wheels
	// bootstrap); every node appends the identical block, so the objective
	// validator set is shared by construction.
	var ids []ports.NodeID
	var regs []chain.BondReg
	for i := 0; i < N; i++ {
		id := identity.FromSeed(seed*1000 + int64(i))
		ids = append(ids, id.NodeID())
		pub := id.Signer().Public().(ed25519.PublicKey)
		regs = append(regs, chain.BondReg{Validator: append([]byte(nil), pub...), Root: ports.HashBytes(pub), Size: bondSize})
	}
	gsigner := identity.FromSeed(1).Signer() // a distinct genesis signer
	g := &chain.Block{Version: chain.BlockVersion, Height: 0,
		Entries: []ports.Entry{simEntry("genesis")}, BondRegs: regs}
	chain.Sign(g, gsigner)

	var nodes []*node.Node
	for i := 0; i < N; i++ {
		id := identity.FromSeed(seed*1000 + int64(i))
		nd := node.New(id.NodeID(), node.DefaultConfig(), sched, net.Endpoint(id.NodeID()), memstore.New())
		nd.SetLedger(credit.New(50_000, 0)) // a SEPARATE, empty ledger per node — rep is useless
		perNode := credit.New(50_000, 0)
		ch := chain.New(cfg, func(n ports.NodeID) int64 { return perNode.Reputation(n) })
		if err := ch.AppendGenesis(*g); err != nil {
			t.Fatalf("genesis: %v", err)
		}
		nd.EnableChain(ch, id.Signer())
		nd.EnableObjectiveChain() // wire the bond verifier → objective mode
		nodes = append(nodes, nd)
	}
	for i := 1; i < N; i++ {
		nodes[i].Bootstrap([]ports.NodeID{ids[0]}, func() {})
	}
	sched.Run()

	// Sanity: an empty local ledger means legacy fork-choice would be dead — but
	// objective mode lets a genesis-bonded validator propose and attest.
	if nodes[0].Chain().BondedSize(ids[1]) != bondSize {
		t.Fatal("setup: genesis should have seeded every validator's on-chain bond")
	}

	// Partition: A = 4 nodes, B = 6. Each side commits its own history.
	groupA, groupB := ids[0:4], ids[4:10]
	net.Partition(groupB...)
	if err := propose(nodes[0], "forkA", groupA[1:4], groupA, cfg.Quorum, sched); err != nil {
		t.Fatalf("group A commit (objective quorum on empty ledgers): %v", err)
	}
	if err := propose(nodes[4], "forkB1", groupB[1:4], groupB, cfg.Quorum, sched); err != nil {
		t.Fatalf("group B block 1: %v", err)
	}
	if err := propose(nodes[4], "forkB2", groupB[1:4], groupB, cfg.Quorum, sched); err != nil {
		t.Fatalf("group B block 2: %v", err)
	}

	headA, _ := nodes[0].Chain().Head()
	headB, _ := nodes[4].Chain().Head()
	if headA == headB {
		t.Fatal("setup: the partition should have produced two DIFFERENT histories")
	}

	// Heal. The lighter side (A, one block) reorgs onto the heavier fork (B, two
	// blocks) — decided by objective bonded weight, agreed by both despite the
	// separate ledgers.
	net.ClearPartition()
	if err := runSync(nodes[0], ids[4], sched); err != nil {
		t.Fatalf("A syncing from B: %v", err)
	}
	if newHeadA, _ := nodes[0].Chain().Head(); newHeadA != headB {
		t.Fatal("F6 integration: the lighter partition must reorg onto the heavier-bond fork after healing")
	}
	if _, ok := nodes[0].Chain().LookupRoot(ports.HashBytes([]byte("forkA"))); ok {
		t.Fatal("group A's abandoned entry must be gone after the reorg")
	}
	for _, name := range []string{"forkB1", "forkB2"} {
		if _, ok := nodes[0].Chain().LookupRoot(ports.HashBytes([]byte(name))); !ok {
			t.Fatalf("group A should now hold fork B's entry %q", name)
		}
	}

	// The heavier side must not switch to the lighter fork.
	if err := runSync(nodes[4], ids[0], sched); err != nil {
		t.Fatalf("B syncing from A: %v", err)
	}
	if headB2, _ := nodes[4].Chain().Head(); headB2 != headB {
		t.Fatal("the heavier partition must not adopt the lighter fork")
	}
}

func simEntry(name string) ports.Entry {
	return ports.Entry{
		Root:           ports.HashBytes([]byte(name)),
		ManifestChunks: []ports.ChunkID{ports.HashBytes([]byte(name + "/m"))},
	}
}
