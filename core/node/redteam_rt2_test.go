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

// M0 hardening H2 / red-team RT-2 (release-and-coast) INVERTED as a regression.
// The blind red team broke the Sybil corner (over our own G2 merge) through the
// bond-TTL surface: a validator registers a genuine bond ONCE, then RELEASES the
// plot and keeps voting forever off that single one-time proof — because standing
// never expired (the TTL shipped OFF by default). This is the third instance of
// "fixed but off by default" (strategy doc §1). The fix is Invariant B: the TTL
// is safe-by-default on the untrusted objective path (cmd/silt effectiveBondTTL),
// unblocked by the non-proposer renewal path so honest validators don't lapse
// (proven over the wire in sim TestObjectiveBondRenewalSustainsAttestOnlyValidator).
//
// This test isolates the mechanism the default flips: with the TTL OFF a released
// validator coasts indefinitely (the live vuln), and with it ON that same
// validator decays out — so leaving it off is not a tuning choice, it is the hole.
func TestRedteamRT2_ReleaseAndCoast(t *testing.T) {
	// releasedStandingAfter builds an objective chain with the given TTL, has V
	// register a REAL bond at height 1, then advances `blocks` more blocks with NO
	// renewal (V released its plot) and returns V's remaining objective standing.
	releasedStandingAfter := func(t *testing.T, ttl uint64, blocks int) int64 {
		sched := simclock.New()
		net := simnet.New(sched, 1, simnet.DefaultConfig())

		vID := identity.FromSeed(90)
		v := New(vID.NodeID(), DefaultConfig(), sched, net.Endpoint(vID.NodeID()), memstore.New())
		v.SetLedger(credit.New(50_000, 0))
		v.EnableBond(vID.Signer(), 1<<20)

		pID, aID := identity.FromSeed(91), identity.FromSeed(92)
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

		b := advance(g, true) // height 1: V proves a genuine bond
		if ch.BondedSize(vID.NodeID()) != 1<<20 {
			t.Fatal("V should be bonded after a real registration")
		}
		for i := 0; i < blocks; i++ {
			b = advance(b, false) // V released its plot: no more renewals
		}
		return ch.BondedSize(vID.NodeID())
	}

	// TTL OFF (the shipped-but-inert state RT-2 exploited): the released validator
	// keeps FULL standing no matter how long it coasts — the live break.
	if got := releasedStandingAfter(t, 0 /*ttl off*/, 20); got != 1<<20 {
		t.Fatalf("setup: with the TTL off a released validator should coast (this is the RT-2 vuln): got %d, want %d", got, 1<<20)
	}

	// TTL ON (the safe default): that SAME released validator decays to zero once
	// it stops renewing past the window — release-and-coast is denied.
	if got := releasedStandingAfter(t, 3 /*ttl on*/, 20); got != 0 {
		t.Fatalf("RT-2 regression: with the TTL on, a released validator kept %d standing — the coast attack survived", got)
	}
}
