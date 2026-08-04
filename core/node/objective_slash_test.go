package node

import (
	"crypto/ed25519"
	"testing"

	"github.com/nerolabs/silt/adapters/identity"
	"github.com/nerolabs/silt/adapters/memstore"
	"github.com/nerolabs/silt/adapters/simclock"
	"github.com/nerolabs/silt/adapters/simnet"
	"github.com/nerolabs/silt/core/chain"
	"github.com/nerolabs/silt/core/credit"
	"github.com/nerolabs/silt/ports"
)

// Integration tier for the blind red-team F2 (equivocation slash inert in
// objective mode): a validator detects a peer's proven double-sign, records it
// ON-CHAIN in the block it proposes, and every replica then evicts the culprit
// from the objective bonded set — the deterrent bites the set consensus actually
// reads, not just the reputation ledger.
func TestObjectiveEquivocationSlashEvictsOverTheLoop(t *testing.T) {
	const bondSize = int64(2) << 20
	sched := simclock.New()
	net := simnet.New(sched, 5, simnet.DefaultConfig())

	idA := identity.FromSeed(501)
	idB := identity.FromSeed(502)
	culprit := identity.FromSeed(503) // a genesis-bonded validator that double-signs
	pub := func(id *identity.Identity) []byte {
		return append([]byte(nil), id.Signer().Public().(ed25519.PublicKey)...)
	}

	// Objective genesis bonds A, B (the honest quorum) and the culprit, each on a
	// distinct root; A and B are the launch anchors that bootstrap the set.
	anchors := map[ports.NodeID]bool{idA.NodeID(): true, idB.NodeID(): true}
	g := &chain.Block{Version: chain.BlockVersion, Height: 0, Entries: []ports.Entry{mkEntry("g")},
		BondRegs: []chain.BondReg{
			{Validator: pub(idA), Root: ports.HashBytes(pub(idA)), Size: bondSize},
			{Validator: pub(idB), Root: ports.HashBytes(pub(idB)), Size: bondSize},
			{Validator: pub(culprit), Root: ports.HashBytes(pub(culprit)), Size: bondSize},
		}}
	chain.Sign(g, idA.Signer())
	cfg := chain.Config{Quorum: 1, MinBond: 1 << 20, Anchors: anchors, MatureValidators: 99}

	mk := func(id *identity.Identity) *Node {
		nd := New(id.NodeID(), DefaultConfig(), sched, net.Endpoint(id.NodeID()), memstore.New())
		nd.SetLedger(credit.New(50_000, 0))
		nd.EnableBond(id.Signer(), bondSize)
		ch := chain.New(cfg, func(ports.NodeID) int64 { return 0 })
		if err := ch.AppendGenesis(*g); err != nil {
			t.Fatal(err)
		}
		nd.EnableChain(ch, id.Signer())
		nd.EnableObjectiveChain()
		return nd
	}
	a, b := mk(idA), mk(idB)
	b.Bootstrap([]ports.NodeID{idA.NodeID()}, func() {})
	sched.Run()

	if a.Chain().BondedSize(culprit.NodeID()) != bondSize {
		t.Fatal("setup: the culprit should start bonded in the objective set")
	}

	// The culprit double-signs: it attests TWO different blocks at height 1 on two
	// forks (each proposed by a throwaway key). This is the evidence A reconciles.
	pA, pB := identity.FromSeed(901).Signer(), identity.FromSeed(902).Signer()
	fork := func(prop ed25519.PrivateKey, tag string) chain.Block {
		blk := chain.Block{Version: chain.BlockVersion, Height: 1, Prev: g.Hash(), Entries: []ports.Entry{mkEntry(tag)}}
		chain.Sign(&blk, prop)
		blk.Atts = []chain.Attestation{chain.Attest(&blk, culprit.Signer())}
		return blk
	}
	a.slashEquivocators([]chain.Block{*g, fork(pA, "forkA")}, []chain.Block{*g, fork(pB, "forkB")})

	// A now records the slash in the block it proposes; B attests; it commits.
	var propErr error
	propDone := false
	a.ProposeEntry(mkEntry("post-slash"), []ports.NodeID{idB.NodeID()},
		[]ports.NodeID{idA.NodeID(), idB.NodeID()}, cfg.Quorum, func(e error) { propErr, propDone = e, true })
	sched.Run()
	if !propDone || propErr != nil {
		t.Fatalf("A should commit a block carrying the on-chain slash: %v", propErr)
	}

	// DENIED on A: the culprit is evicted from the objective set.
	if !a.Chain().IsSlashed(culprit.NodeID()) || a.Chain().BondedSize(culprit.NodeID()) != 0 {
		t.Fatal("F2 integration: the equivocator must be evicted from the objective set on the proposer")
	}

	// And every replica honors it: B syncs the slash block and evicts too.
	syncDone := false
	b.SyncChain([]ports.NodeID{idA.NodeID()}, func(int, error) { syncDone = true })
	sched.Run()
	if !syncDone {
		t.Fatal("B sync did not complete")
	}
	if !b.Chain().IsSlashed(culprit.NodeID()) || b.Chain().BondedSize(culprit.NodeID()) != 0 {
		t.Fatal("F2 integration: every replica must evict the equivocator in lockstep (objective slash is on-chain)")
	}
}
