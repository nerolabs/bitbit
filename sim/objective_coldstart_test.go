package sim

import (
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

// Integration proof for the objective-fork-choice COLD START (F6, default
// posture): an objective network with NO genesis-seeded bonds bootstraps purely
// from the declared training-wheels anchors, and validators become REALLY bonded
// on-chain by LIVE self-registration as they propose. Each node holds its own
// (empty) ledger — the local reputation view is useless — so this proves the
// objective set is established from the chain + the anchor bootstrap alone, the
// thing a fresh red-team would test against default daemons.
func TestObjectiveColdStartBootstrapsFromAnchors(t *testing.T) {
	const (
		seed     = int64(23)
		N        = 5
		bondSize = int64(2) << 20 // > MinBond
	)

	sched := simclock.New()
	net := simnet.New(sched, seed, simnet.DefaultConfig())

	// A synthetic genesis with NO bond registrations — the objective set starts
	// EMPTY and must be bootstrapped by the anchors.
	var ids []ports.NodeID
	anchors := map[ports.NodeID]bool{}
	for i := 0; i < N; i++ {
		id := identity.FromSeed(seed*1000 + int64(i))
		ids = append(ids, id.NodeID())
		anchors[id.NodeID()] = true
	}
	gsigner := identity.FromSeed(1).Signer()
	g := &chain.Block{Version: chain.BlockVersion, Height: 0, Entries: []ports.Entry{simEntry("cold-genesis")}}
	chain.Sign(g, gsigner)

	// MatureValidators high so the launch window stays open for the test; anchors
	// provide eligibility, but weight is always the real registered bond.
	cfg := chain.Config{Quorum: 3, MinBond: 1 << 20, Anchors: anchors, MatureValidators: 99}

	var nodes []*node.Node
	for i := 0; i < N; i++ {
		id := identity.FromSeed(seed*1000 + int64(i))
		nd := node.New(id.NodeID(), node.DefaultConfig(), sched, net.Endpoint(id.NodeID()), memstore.New())
		nd.SetLedger(credit.New(50_000, 0))  // separate empty ledger — rep is useless
		nd.EnableBond(id.Signer(), bondSize) // a real held bond to register
		perNode := credit.New(50_000, 0)
		ch := chain.New(cfg, func(n ports.NodeID) int64 { return perNode.Reputation(n) })
		if err := ch.AppendGenesis(*g); err != nil {
			t.Fatalf("genesis: %v", err)
		}
		nd.EnableChain(ch, id.Signer())
		nd.EnableObjectiveChain()
		nodes = append(nodes, nd)
	}
	for i := 1; i < N; i++ {
		nodes[i].Bootstrap([]ports.NodeID{ids[0]}, func() {})
	}
	sched.Run()

	// Before any block: the objective set is empty — nobody is bonded on-chain.
	if nodes[0].Chain().BondedSize(ids[0]) != 0 {
		t.Fatal("setup: the objective bonded set must start empty (no genesis seeding)")
	}

	// The anchors bootstrap consensus: node 0 proposes (eligible as a launch
	// anchor despite an empty bonded set), attested by other anchors — and its
	// block carries its own live bond registration.
	if err := proposeEntry(nodes[0], simEntry("blk1"), ids[1:4], ids, cfg.Quorum, sched); err != nil {
		t.Fatalf("cold-start: an anchor must be able to bootstrap the first objective block: %v", err)
	}

	// OUTCOME: the proposer is now REALLY bonded on-chain via its self-registration
	// — the objective set is being built from proven bonds, not declarations.
	if got := nodes[0].Chain().BondedSize(ids[0]); got != bondSize {
		t.Fatalf("proposer should be really bonded on-chain after proposing (got %d, want %d)", got, bondSize)
	}
	// Every replica agrees on the objective bonded size (it is chain-derived).
	for i, nd := range nodes {
		// nodes 1..N-1 only hear the commit via broadcast; drive one sync so they
		// have block 1 too, then check agreement.
		if nd.Chain().Len() < 2 {
			if err := runSync(nd, ids[0], sched); err != nil {
				t.Fatalf("node %d sync: %v", i, err)
			}
		}
		if nd.Chain().BondedSize(ids[0]) != bondSize {
			t.Fatalf("replica %d disagrees on the proposer's on-chain bond (got %d)", i, nd.Chain().BondedSize(ids[0]))
		}
	}

	// A second proposer registers itself the same way — the set grows by proof.
	if err := proposeEntry(nodes[1], simEntry("blk2"), []ports.NodeID{ids[0], ids[2], ids[3]}, ids, cfg.Quorum, sched); err != nil {
		t.Fatalf("a second anchor proposing must also register + commit: %v", err)
	}
	if got := nodes[1].Chain().BondedSize(ids[1]); got != bondSize {
		t.Fatalf("the second proposer should be really bonded after proposing (got %d)", got)
	}
}
