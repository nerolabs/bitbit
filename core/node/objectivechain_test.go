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

func pubOf(id *identity.Identity) []byte {
	return append([]byte(nil), id.Signer().Public().(ed25519.PublicKey)...)
}

// Unit coverage for the objective-fork-choice node wiring (F6): a node mints a
// live on-chain bond registration from its held bond (RegisterBondReg), and a
// replica with the REAL space-time verifier (EnableObjectiveChain) accepts it —
// so the validator joins the objective set — while a tampered proof is rejected.
func TestRegisterBondRegRoundTripsThroughObjectiveVerifier(t *testing.T) {
	sched := simclock.New()
	net := simnet.New(sched, 1, simnet.DefaultConfig())

	// Newcomer B holds a real bond and mints its registration.
	bID := identity.FromSeed(20)
	b := New(bID.NodeID(), DefaultConfig(), sched, net.Endpoint(bID.NodeID()), memstore.New())
	b.SetLedger(credit.New(50_000, 0))
	b.EnableBond(bID.Signer(), 1<<20)

	// A launch validator set (proposer P + attester V), seeded bonded at genesis;
	// Quorum 1 keeps the block minimal.
	pID, vID := identity.FromSeed(21), identity.FromSeed(22)
	cfg := chain.Config{Quorum: 1, MinBond: 1 << 20}
	g := &chain.Block{Version: chain.BlockVersion, Height: 0, Entries: []ports.Entry{mkEntry("g")},
		BondRegs: []chain.BondReg{
			{Validator: pubOf(pID), Size: 64 << 20},
			{Validator: pubOf(vID), Size: 64 << 20},
		}}
	chain.Sign(g, pID.Signer())

	// A replica wired with the real bond verifier (as EnableObjectiveChain does).
	p := New(pID.NodeID(), DefaultConfig(), sched, net.Endpoint(pID.NodeID()), memstore.New())
	ch := chain.New(cfg, func(ports.NodeID) int64 { return 0 })
	p.EnableChain(ch, pID.Signer())
	p.EnableObjectiveChain()
	if err := ch.AppendGenesis(*g); err != nil {
		t.Fatal(err)
	}

	reg, ok := b.RegisterBondReg(g.Hash())
	if !ok {
		t.Fatal("B should mint a registration from its held bond")
	}

	// A tampered space-time proof is rejected by the real verifier.
	bad := reg
	bad.Answer = append([]byte(nil), reg.Answer...)
	bad.Answer[len(bad.Answer)-1] ^= 0xff
	badBlk := &chain.Block{Version: chain.BlockVersion, Height: 1, Prev: g.Hash(),
		Entries: []ports.Entry{mkEntry("bad")}, BondRegs: []chain.BondReg{bad}}
	chain.Sign(badBlk, pID.Signer())
	badBlk.Atts = []chain.Attestation{chain.Attest(badBlk, vID.Signer())}
	if err := ch.Append(*badBlk); err == nil {
		t.Fatal("a tampered space-time proof must be rejected")
	}

	// The genuine registration is accepted, and B joins the objective set.
	blk := &chain.Block{Version: chain.BlockVersion, Height: 1, Prev: g.Hash(),
		Entries: []ports.Entry{mkEntry("e1")}, BondRegs: []chain.BondReg{reg}}
	chain.Sign(blk, pID.Signer())
	blk.Atts = []chain.Attestation{chain.Attest(blk, vID.Signer())}
	if err := ch.Append(*blk); err != nil {
		t.Fatalf("a genuine live registration must be accepted: %v", err)
	}
	if got := ch.BondedSize(bID.NodeID()); got != 1<<20 {
		t.Fatalf("B should now be bonded on-chain: got %d, want %d", got, 1<<20)
	}
}
