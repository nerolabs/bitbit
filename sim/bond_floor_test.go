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

// Integration/sim tier for the anti-release bond floor (F1/F2): a validator
// whose bond is below the floor earns NO standing over the LIVE audit — even
// though it holds and can answer for that small plot — because a sub-floor plot
// can be released and re-plotted inside the challenge window. A validator at/above
// the floor earns standing. Driven through the node loop (gossip → challenge →
// verify → floor gate → ledger).
func TestBondFloorDeniesStandingOverTheNetwork(t *testing.T) {
	const (
		seed  = int64(17)
		floor = int64(16) << 20
	)
	ledger := credit.New(50_000, 0)
	repFn := func(n ports.NodeID) int64 { return ledger.Reputation(n) }
	cfg := chain.DefaultConfig()
	nodeCfg := node.DefaultConfig()
	nodeCfg.MinBondBytes = floor // the auditor enforces the floor when crediting peers

	sched := simclock.New()
	net := simnet.New(sched, seed, simnet.DefaultConfig())

	mk := func(idSeed int64, bondSize int64) *node.Node {
		ident := identity.FromSeed(idSeed)
		st := memstore.New()
		nd := node.New(ident.NodeID(), nodeCfg, sched, net.Endpoint(ident.NodeID()), st)
		nd.SetLedger(ledger)
		nd.EnableBond(ident.Signer(), bondSize)
		ch := chain.New(cfg, repFn)
		if gb, _, _, gerr := genesis.Build(st); gerr == nil {
			ch.AppendGenesis(gb)
		}
		nd.EnableChain(ch, ident.Signer())
		return nd
	}

	auditor := mk(seed*1000, 32<<20)  // above floor
	honest := mk(seed*1000+1, 32<<20) // above floor
	tiny := mk(seed*1000+2, 4<<20)    // below floor

	for _, nd := range []*node.Node{honest, tiny} {
		nd.Bootstrap([]ports.NodeID{auditor.ID()}, func() {})
	}
	sched.Run()

	auditor.AuditBondsOnce()
	sched.Run()

	if ledger.Reputation(honest.ID()) <= 0 {
		t.Fatal("a validator bonded above the floor must earn standing over the wire")
	}
	if got := ledger.Reputation(tiny.ID()); got > 0 {
		t.Fatalf("F1/F2 floor: a sub-floor validator must earn NO standing over the wire, got %d — "+
			"its bond is small enough to release and re-plot inside the challenge window", got)
	}
}
