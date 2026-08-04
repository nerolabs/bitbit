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

// Integration/sim tier for the M0 Sybil fix (red-team F1/F2). The unit tests
// (core/bond/redteam_sybil_test.go) prove the crypto — byte-binding defeats a
// leaves-only recompute, and the read-bound VDF seed defeats a released plot.
// This drives that property THROUGH THE LIVE AUDIT WIRE (gossip → MsgBondChallenge
// → answer → VerifySpaceTime → ledger): a validator that pledges a bond,
// advertises it, then RELEASES the resident bytes (holding at most the 32-byte
// leaves — the attacker that frees the space to save disk) FAILS the live audit
// and earns ZERO standing, while an honest full-plot validator earns it. You
// cannot get standing for storage you no longer hold.
func TestReleasedBondEarnsNoStandingOverTheNetwork(t *testing.T) {
	const (
		seed     = int64(11)
		bondSize = int64(8) << 20 // 8 MiB → rep ~128, clears the 100 bar when proven
	)
	ledger := credit.New(50_000, 0)
	repFn := func(n ports.NodeID) int64 { return ledger.Reputation(n) }
	cfg := chain.DefaultConfig()
	nodeCfg := node.DefaultConfig()

	sched := simclock.New()
	net := simnet.New(sched, seed, simnet.DefaultConfig())

	mk := func(idSeed int64, release bool) *node.Node {
		ident := identity.FromSeed(idSeed)
		st := memstore.New()
		nd := node.New(ident.NodeID(), nodeCfg, sched, net.Endpoint(ident.NodeID()), st)
		nd.SetLedger(ledger)
		nd.EnableBond(ident.Signer(), bondSize) // seal + advertise the bond
		if release {
			nd.ReleaseBond() // ...then free the bytes: still advertised, no longer held
		}
		ch := chain.New(cfg, repFn)
		if gb, _, _, gerr := genesis.Build(st); gerr == nil {
			ch.AppendGenesis(gb)
		}
		nd.EnableChain(ch, ident.Signer())
		return nd
	}

	auditor := mk(seed*1000, false)
	honest := mk(seed*1000+1, false)
	cheat := mk(seed*1000+2, true) // advertises a bond it no longer holds

	for _, nd := range []*node.Node{honest, cheat} {
		nd.Bootstrap([]ports.NodeID{auditor.ID()}, func() {})
	}
	sched.Run()

	auditor.AuditBondsOnce() // one live sweep: challenge both peers' advertised bonds
	sched.Run()

	if ledger.Reputation(honest.ID()) <= 0 {
		t.Fatal("an honest full-plot validator must earn standing from a live audit")
	}
	if got := ledger.Reputation(cheat.ID()); got > 0 {
		t.Fatalf("F1/F2 integration: a released-bond validator must earn NO standing over the wire, got %d — "+
			"it advertised a bond it no longer holds and could not answer the live challenge", got)
	}
}
