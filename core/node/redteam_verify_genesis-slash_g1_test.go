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

// Integration tier for retest G1: at the node composition level (objective mode
// on), a malicious genesis carrying a proof-free Slash against an honest bonded
// validator must not establish — so the objective validator set can never be
// corrupted by an unverified genesis eviction. The clean genesis (same bonds, no
// slash) is accepted and the victim holds full objective standing.
func TestGenesisBogusSlashCannotCorruptObjectiveSet(t *testing.T) {
	const bondSize = int64(2) << 20
	sched := simclock.New()
	net := simnet.New(sched, 3, simnet.DefaultConfig())

	victim := identity.FromSeed(701)
	other := identity.FromSeed(702)
	pub := func(id *identity.Identity) []byte {
		return append([]byte(nil), id.Signer().Public().(ed25519.PublicKey)...)
	}
	cfg := chain.Config{Quorum: 1, MinBond: 1 << 20}

	// Two conflicting height-1 blocks signed only by `other` — a FORGED accusation
	// against the victim, who signed neither.
	a := &chain.Block{Version: chain.BlockVersion, Height: 1, Entries: []ports.Entry{mkEntry("a")}}
	chain.Sign(a, other.Signer())
	b := &chain.Block{Version: chain.BlockVersion, Height: 1, Entries: []ports.Entry{mkEntry("b")}}
	chain.Sign(b, other.Signer())
	bogus := chain.Equivocation{Culprit: pub(victim), A: *a, B: *b}

	bonds := []chain.BondReg{
		{Validator: pub(victim), Root: ports.HashBytes(pub(victim)), Size: bondSize},
		{Validator: pub(other), Root: ports.HashBytes(pub(other)), Size: bondSize},
	}

	mkChain := func(g *chain.Block) (*chain.Chain, error) {
		nd := New(victim.NodeID(), DefaultConfig(), sched, net.Endpoint(victim.NodeID()), memstore.New())
		nd.SetLedger(credit.New(50_000, 0))
		ch := chain.New(cfg, func(ports.NodeID) int64 { return 0 })
		nd.EnableChain(ch, victim.Signer())
		nd.EnableObjectiveChain()
		return ch, ch.AppendGenesis(*g)
	}

	// Malicious genesis: real bonds + a bogus slash of the victim. It must NOT
	// establish, so the node never runs on a chain that evicted an honest holder.
	bad := &chain.Block{Version: chain.BlockVersion, Height: 0, Entries: []ports.Entry{mkEntry("g")},
		BondRegs: bonds, Slashes: []chain.Equivocation{bogus}}
	chain.Sign(bad, other.Signer())
	if _, err := mkChain(bad); err == nil {
		t.Fatal("G1 regression: a node accepted a genesis that evicts an honest validator with no proof")
	}

	// Clean genesis (same bonds, no slash): accepted, and the victim is bonded and
	// qualified in the objective set.
	good := &chain.Block{Version: chain.BlockVersion, Height: 0, Entries: []ports.Entry{mkEntry("g")},
		BondRegs: bonds}
	chain.Sign(good, other.Signer())
	ch, err := mkChain(good)
	if err != nil {
		t.Fatalf("a clean genesis must establish: %v", err)
	}
	if ch.IsSlashed(victim.NodeID()) {
		t.Fatal("the victim must not be slashed under the clean genesis")
	}
	if got := ch.BondedSize(victim.NodeID()); got != bondSize {
		t.Fatalf("the victim must hold full objective standing: got %d, want %d", got, bondSize)
	}
}
