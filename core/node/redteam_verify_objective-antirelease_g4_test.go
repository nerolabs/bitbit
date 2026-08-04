package node

import (
	"strconv"
	"testing"

	"github.com/nerolabs/silt/adapters/identity"
	"github.com/nerolabs/silt/adapters/memstore"
	"github.com/nerolabs/silt/adapters/simclock"
	"github.com/nerolabs/silt/adapters/simnet"
	"github.com/nerolabs/silt/core/chain"
	"github.com/nerolabs/silt/core/credit"
	"github.com/nerolabs/silt/ports"
)

// Integration tier for retest G4 (c): through the REAL space-time verifier, a
// validator's objective standing decays once it stops renewing — modelling a
// prover that registered a genuine bond and then RELEASED its plot. A validator
// that keeps renewing (it still holds the plot, so it can answer the fresh
// challenge) retains standing. This enforces the "time" half of
// proof-of-space-TIME on the objective fork-choice path.
func TestObjectiveStandingDecaysWhenValidatorStopsRenewing(t *testing.T) {
	const ttl = uint64(2)
	sched := simclock.New()
	net := simnet.New(sched, 1, simnet.DefaultConfig())

	// Honest validator V holds a real bond.
	vID := identity.FromSeed(80)
	v := New(vID.NodeID(), DefaultConfig(), sched, net.Endpoint(vID.NodeID()), memstore.New())
	v.SetLedger(credit.New(50_000, 0))
	v.EnableBond(vID.Signer(), 1<<20)

	pID, aID := identity.FromSeed(81), identity.FromSeed(82)
	anchors := map[ports.NodeID]bool{pID.NodeID(): true, aID.NodeID(): true}
	cfg := chain.Config{Quorum: 1, MinBond: 1 << 20, BondTTLBlocks: ttl, Anchors: anchors, MatureValidators: 99}

	ch := chain.New(cfg, func(ports.NodeID) int64 { return 0 })
	p := New(pID.NodeID(), DefaultConfig(), sched, net.Endpoint(pID.NodeID()), memstore.New())
	p.EnableChain(ch, pID.Signer())
	p.EnableObjectiveChain()
	g := &chain.Block{Version: chain.BlockVersion, Height: 0, Entries: []ports.Entry{mkEntry("g")}}
	chain.Sign(g, pID.Signer())
	if err := ch.AppendGenesis(*g); err != nil {
		t.Fatal(err)
	}

	// Anchors advance the chain by one block; V optionally renews its real bond.
	advance := func(prev *chain.Block, renew bool) *chain.Block {
		var regs []chain.BondReg
		if renew {
			reg, ok := v.RegisterBondReg(prev.Hash())
			if !ok {
				t.Fatal("V should mint a live registration")
			}
			regs = []chain.BondReg{reg}
		}
		nb := &chain.Block{Version: chain.BlockVersion, Height: prev.Height + 1, Prev: prev.Hash(),
			Entries: []ports.Entry{mkEntry("e" + strconv.FormatUint(prev.Height+1, 10))}, BondRegs: regs}
		chain.Sign(nb, pID.Signer())
		nb.Atts = []chain.Attestation{chain.Attest(nb, aID.Signer())}
		if err := ch.Append(*nb); err != nil {
			t.Fatalf("advance to height %d: %v", prev.Height+1, err)
		}
		return nb
	}

	// V registers a genuine bond at height 1, then stops renewing (releases plot).
	b := advance(g, true)
	if ch.BondedSize(vID.NodeID()) != 1<<20 {
		t.Fatal("V should be bonded on-chain after registering a real proof")
	}
	b = advance(b, false) // h2: within TTL
	b = advance(b, false) // h3: at TTL boundary
	if ch.BondedSize(vID.NodeID()) != 1<<20 {
		t.Fatal("V must retain standing within the TTL window")
	}
	advance(b, false) // h4: past TTL → lapses
	if got := ch.BondedSize(vID.NodeID()); got != 0 {
		t.Fatalf("G4 regression: V kept %d objective standing after releasing (no renewal past TTL), want 0", got)
	}

	// A validator that keeps renewing (still holds the plot) never lapses.
	ch2 := chain.New(cfg, func(ports.NodeID) int64 { return 0 })
	p2 := New(pID.NodeID(), DefaultConfig(), sched, net.Endpoint(pID.NodeID()), memstore.New())
	p2.EnableChain(ch2, pID.Signer())
	p2.EnableObjectiveChain()
	g2 := &chain.Block{Version: chain.BlockVersion, Height: 0, Entries: []ports.Entry{mkEntry("g")}}
	chain.Sign(g2, pID.Signer())
	if err := ch2.AppendGenesis(*g2); err != nil {
		t.Fatal(err)
	}
	prev := g2
	for h := 0; h < 5; h++ {
		reg, ok := v.RegisterBondReg(prev.Hash())
		if !ok {
			t.Fatal("V renew mint failed")
		}
		nb := &chain.Block{Version: chain.BlockVersion, Height: prev.Height + 1, Prev: prev.Hash(),
			Entries: []ports.Entry{mkEntry("r" + strconv.FormatUint(prev.Height+1, 10))}, BondRegs: []chain.BondReg{reg}}
		chain.Sign(nb, pID.Signer())
		nb.Atts = []chain.Attestation{chain.Attest(nb, aID.Signer())}
		if err := ch2.Append(*nb); err != nil {
			t.Fatalf("renew advance: %v", err)
		}
		prev = nb
	}
	if got := ch2.BondedSize(vID.NodeID()); got != 1<<20 {
		t.Fatalf("G4 regression: a continuously-renewing validator lost standing: got %d, want %d", got, 1<<20)
	}
}
