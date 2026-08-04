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

// Integration (node ↔ chain) for the canonical issuer set (F4 §2c): a node
// surfaces the chain's deterministic on-chain-bonded issuer set, so two nodes on
// the same objective genesis expose the IDENTICAL set — every publisher asks the
// same validators and the subset it chose leaks nothing. A node with no chain
// surfaces nothing (the caller falls back to its peer list).
func TestNodeCanonicalIssuersMirrorsChain(t *testing.T) {
	sched := simclock.New()
	net := simnet.New(sched, 1, simnet.DefaultConfig())

	v0, v1, v2 := identity.FromSeed(30), identity.FromSeed(31), identity.FromSeed(32)
	g := &chain.Block{Version: chain.BlockVersion, Height: 0, Entries: []ports.Entry{mkEntry("g")},
		BondRegs: []chain.BondReg{
			{Validator: pubOf(v0), Root: ports.HashBytes(pubOf(v0)), Size: 5 << 20},
			{Validator: pubOf(v1), Root: ports.HashBytes(pubOf(v1)), Size: 3 << 20},
			{Validator: pubOf(v2), Root: ports.HashBytes(pubOf(v2)), Size: 4 << 20},
		}}
	chain.Sign(g, v0.Signer())

	mk := func(seed int64) *Node {
		id := identity.FromSeed(seed)
		nd := New(id.NodeID(), DefaultConfig(), sched, net.Endpoint(id.NodeID()), memstore.New())
		ch := chain.New(chain.Config{Quorum: 1, MinBond: 1 << 20}, func(ports.NodeID) int64 { return 0 })
		if err := ch.AppendGenesis(*g); err != nil {
			t.Fatal(err)
		}
		nd.EnableChain(ch, id.Signer())
		nd.EnableObjectiveChain()
		return nd
	}
	a, b := mk(40), mk(41)

	sa, sb := a.CanonicalIssuers(0), b.CanonicalIssuers(0)
	if len(sa) != 3 {
		t.Fatalf("expected all 3 bonded validators, got %d", len(sa))
	}
	for i := range sa {
		if sa[i] != sb[i] {
			t.Fatal("two nodes on the same chain must expose the identical canonical issuer set")
		}
	}
	// Ordered by bonded size: v0 (5) > v2 (4) > v1 (3).
	if sa[0] != v0.NodeID() || sa[1] != v2.NodeID() || sa[2] != v1.NodeID() {
		t.Fatalf("canonical set must be ordered by bonded size, got %v", sa)
	}

	bare := New(identity.FromSeed(99).NodeID(), DefaultConfig(), sched, net.Endpoint(identity.FromSeed(99).NodeID()), memstore.New())
	if bare.CanonicalIssuers(0) != nil {
		t.Fatal("a node with no chain must surface no canonical set (caller falls back to peers)")
	}
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
			{Validator: pubOf(pID), Root: ports.HashBytes(pubOf(pID)), Size: 64 << 20},
			{Validator: pubOf(vID), Root: ports.HashBytes(pubOf(vID)), Size: 64 << 20},
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
