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

// Integration/sim tier for the M0 Sybil fix G2 (docs/design/m0-sybil-rebind.md).
// The unit tests (core/bond/redteam_g2_test.go) prove the crypto: a plot is
// bound to ONE (identity, size), so a prefix or a foreign plot fails the
// labeling recompute. This drives the IDENTITY half of that binding THROUGH THE
// LIVE AUDIT WIRE.
//
// The G2 premise is Sybil keys are free and one physical plot could back many
// standings. Here a Sybil identity B (distinct NodeID) points at ANOTHER node's
// plot — it seals its bond from A's key, so it advertises A's root and answers
// challenges from A's plot. Before G2 the space-time answer was not identity-
// bound, so B's answer verified against the shared root and B earned standing for
// storage it does not independently hold. After G2 the audit binds the answer's
// key to the challenged identity (sha256(PK)==id) AND recomputes labels from
// H(PK, n): B's answer carries A's key, which does not hash to B, so B earns
// ZERO. N identities can no longer share one plot's standing.
func TestG2SharedPlotSybilEarnsNoStandingOverTheNetwork(t *testing.T) {
	const (
		seed     = int64(23)
		bondSize = int64(8) << 20 // 8 MiB → rep well over the 100 bar when proven
	)
	ledger := credit.New(50_000, 0)
	repFn := func(n ports.NodeID) int64 { return ledger.Reputation(n) }
	cfg := chain.DefaultConfig()
	nodeCfg := node.DefaultConfig()

	sched := simclock.New()
	net := simnet.New(sched, seed, simnet.DefaultConfig())

	// identA owns the one physical plot.
	identA := identity.FromSeed(seed * 7)

	mk := func(idSeed int64, borrowA bool) *node.Node {
		self := identity.FromSeed(idSeed)
		st := memstore.New()
		nd := node.New(self.NodeID(), nodeCfg, sched, net.Endpoint(self.NodeID()), st)
		nd.SetLedger(ledger)
		if borrowA {
			// The Sybil seals its bond from A's key — so it advertises A's root and
			// answers with A's plot, the shared-plot Sybil G2 must deny.
			nd.EnableBond(identA.Signer(), bondSize)
		} else {
			nd.EnableBond(self.Signer(), bondSize)
		}
		ch := chain.New(cfg, repFn)
		if gb, _, _, gerr := genesis.Build(st); gerr == nil {
			ch.AppendGenesis(gb)
		}
		nd.EnableChain(ch, self.Signer())
		return nd
	}

	auditor := mk(seed*1000, false)
	honest := mk(seed*1000+1, false) // its own distinct plot
	cheat := mk(seed*1000+2, true)   // distinct identity, borrows A's plot

	for _, nd := range []*node.Node{honest, cheat} {
		nd.Bootstrap([]ports.NodeID{auditor.ID()}, func() {})
	}
	sched.Run()

	auditor.AuditBondsOnce() // one live sweep: challenge both peers' advertised bonds
	sched.Run()

	if ledger.Reputation(honest.ID()) <= 0 {
		t.Fatal("an honest validator with its own plot must earn standing from a live audit")
	}
	if got := ledger.Reputation(cheat.ID()); got > 0 {
		t.Fatalf("G2 integration: a Sybil pointing at another node's plot must earn NO standing over the wire, got %d — "+
			"its answer carries the plot owner's key, which does not hash to its own identity", got)
	}
}
