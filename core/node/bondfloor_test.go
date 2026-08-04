package node

import (
	"testing"

	"github.com/nerolabs/silt/adapters/identity"
	"github.com/nerolabs/silt/adapters/memstore"
	"github.com/nerolabs/silt/adapters/simclock"
	"github.com/nerolabs/silt/adapters/simnet"
	"github.com/nerolabs/silt/core/credit"
)

// Unit coverage for the anti-release bond floor (M0 Sybil, red-team F1/F2): a
// bond below Config.MinBondBytes earns NO standing, because it is small enough
// to release and re-plot inside the challenge window — so a valid answer proves
// nothing about SUSTAINED possession. A bond at/above the floor earns standing.
func TestBondAntiReleaseFloorGatesStanding(t *testing.T) {
	sched := simclock.New()
	net := simnet.New(sched, 1, simnet.DefaultConfig())
	const floor = int64(4) << 20

	mk := func(idSeed int64, bondSize int64) (*Node, *credit.Ledger) {
		ident := identity.FromSeed(idSeed)
		ledger := credit.New(50_000, 0)
		cfg := DefaultConfig()
		cfg.MinBondBytes = floor
		nd := New(ident.NodeID(), cfg, sched, net.Endpoint(ident.NodeID()), memstore.New())
		nd.SetLedger(ledger)
		nd.EnableBond(ident.Signer(), bondSize)
		nd.AuditBondsOnce() // self-credit sweep applies the floor
		sched.Run()
		return nd, ledger
	}

	sub, subLedger := mk(1, 2<<20) // below the floor
	ok, okLedger := mk(2, 8<<20)   // at/above the floor

	if got := subLedger.Reputation(sub.ID()); got > 0 {
		t.Fatalf("F1/F2: a sub-floor bond must earn NO standing, got %d", got)
	}
	if got := okLedger.Reputation(ok.ID()); got <= 0 {
		t.Fatal("a bond at/above the anti-release floor must earn standing")
	}
}
