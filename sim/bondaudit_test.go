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

// TestBondAuditEarnsStandingOverTheNetwork is the integration/sim tier for
// T1b: validators challenge each other's storage bonds THROUGH THE NODE LOOP
// (gossip → MsgBondChallenge → MsgBondReply → Verify → ledger), and the
// OUTCOMES are asserted:
//  1. standing earned over the network lets a bonded quorum commit a block;
//  2. a node that runs no bond earns no standing and is refused;
//  3. standing must be sustained — once bonds stop being re-proven, decay
//     retires it and a formerly-bonded validator can no longer commit.
func TestBondAuditEarnsStandingOverTheNetwork(t *testing.T) {
	const (
		seed     = int64(7)
		V        = 5       // bonded validators
		bondSize = 8 << 20 // 8 MiB → rep 128, clears the 100 bar
	)
	ledger := credit.New(50_000, 0)
	repFn := func(n ports.NodeID) int64 { return ledger.Reputation(n) }
	cfg := chain.DefaultConfig() // quorum 3, rep 100
	nodeCfg := node.DefaultConfig()

	sched := simclock.New()
	net := simnet.New(sched, seed, simnet.DefaultConfig())

	mk := func(idSeed int64, bonded bool) *node.Node {
		ident := identity.FromSeed(idSeed)
		id := ident.NodeID()
		st := memstore.New()
		nd := node.New(id, nodeCfg, sched, net.Endpoint(id), st)
		nd.SetLedger(ledger)
		if bonded {
			nd.EnableBond(bondSize) // seal + advertise BEFORE bootstrap so gossip carries it
		}
		ch := chain.New(cfg, repFn)
		if gb, _, _, gerr := genesis.Build(st); gerr == nil {
			ch.AppendGenesis(gb)
		}
		nd.EnableChain(ch, ident.Signer())
		return nd
	}

	var vals []*node.Node
	var valIDs []ports.NodeID
	for i := 0; i < V; i++ {
		nd := mk(seed*1000+int64(i), true)
		vals = append(vals, nd)
		valIDs = append(valIDs, nd.ID())
	}
	lazy := mk(seed*1000+999, false) // the no-bond negative control

	// Bootstrap everyone to vals[0] so bond roots gossip out.
	for _, nd := range append(append([]*node.Node{}, vals[1:]...), lazy) {
		nd.Bootstrap([]ports.NodeID{vals[0].ID()}, func() {})
	}
	sched.Run()

	// Each validator runs one audit sweep — challenging the bonds it learned.
	for _, nd := range vals {
		nd.AuditBondsOnce()
	}
	sched.Run()

	// OUTCOME 1: standing was earned over the network → a bonded quorum commits.
	if err := propose(vals[0], "bond-earned", valIDs[1:], valIDs, cfg.Quorum, sched); err != nil {
		t.Fatalf("bonded validators earned no committable standing over the net: %v", err)
	}
	if vals[0].Chain().Len() != 2 { // genesis + the committed block
		t.Fatalf("expected the block to commit (len 2), got chain len %d", vals[0].Chain().Len())
	}

	// OUTCOME 2: the no-bond node earned no standing → its proposal is refused.
	if err := propose(lazy, "lazy", valIDs[:cfg.Quorum], valIDs, cfg.Quorum, sched); err == nil {
		t.Fatal("a node running no storage bond must earn no standing and be refused")
	}

	// OUTCOME 3: standing must be sustained — decay it, and the same validator
	// can no longer commit.
	ledger.DecayStale(uint64(1)<<40, 1)
	if err := propose(vals[0], "after-decay", valIDs[1:], valIDs, cfg.Quorum, sched); err == nil {
		t.Fatal("standing must lapse when bonds stop being re-proven (DecayStale)")
	}
}

// propose drives one ProposeEntry to completion on the sim scheduler and
// returns the commit error (nil = committed).
func propose(n *node.Node, name string, attesters, broadcast []ports.NodeID, quorum int, sched *simclock.Scheduler) error {
	entry := ports.Entry{
		Root:           ports.HashBytes([]byte(name)),
		ManifestChunks: []ports.ChunkID{ports.HashBytes([]byte(name + "/m"))},
	}
	var propErr error
	done := false
	n.ProposeEntry(entry, attesters, broadcast, quorum, func(err error) { propErr = err; done = true })
	sched.Run()
	if !done {
		return errNotDone
	}
	return propErr
}

var errNotDone = errString("propose never completed")

type errString string

func (e errString) Error() string { return string(e) }
