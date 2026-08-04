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

// Integration tier for retest G3: through the REAL space-time verifier, an honest
// validator's genuine live bond registration displaces a malicious genesis that
// pre-squatted its plot root under an attacker key. The honest holder ends up
// bonded on-chain; the unproven squatter does not.
func TestGenesisBondSquatDisplacedThroughObjectiveVerifier(t *testing.T) {
	sched := simclock.New()
	net := simnet.New(sched, 1, simnet.DefaultConfig())

	// Honest newcomer V holds a real bond; read its plot root before genesis.
	vID := identity.FromSeed(60)
	v := New(vID.NodeID(), DefaultConfig(), sched, net.Endpoint(vID.NodeID()), memstore.New())
	v.SetLedger(credit.New(50_000, 0))
	v.EnableBond(vID.Signer(), 1<<20)
	probe, ok := v.RegisterBondReg(ports.Hash{})
	if !ok {
		t.Fatal("V should mint a registration from its held bond")
	}
	realRoot := probe.Root

	// Attacker key pre-squats V's real root in genesis (declared, no proof), with
	// launch anchors P/A to bootstrap the immature set.
	attacker := identity.FromSeed(61)
	pID, aID := identity.FromSeed(62), identity.FromSeed(63)
	pub := func(id *identity.Identity) []byte {
		return append([]byte(nil), id.Signer().Public().(ed25519.PublicKey)...)
	}
	anchors := map[ports.NodeID]bool{pID.NodeID(): true, aID.NodeID(): true}
	cfg := chain.Config{Quorum: 1, MinBond: 1 << 20, Anchors: anchors, MatureValidators: 99}
	g := &chain.Block{Version: chain.BlockVersion, Height: 0, Entries: []ports.Entry{mkEntry("g")},
		BondRegs: []chain.BondReg{{Validator: pub(attacker), Root: realRoot, Size: 1 << 20}}}
	chain.Sign(g, pID.Signer())

	// A replica wired with the real bond verifier.
	ch := chain.New(cfg, func(ports.NodeID) int64 { return 0 })
	p := New(pID.NodeID(), DefaultConfig(), sched, net.Endpoint(pID.NodeID()), memstore.New())
	p.EnableChain(ch, pID.Signer())
	p.EnableObjectiveChain()
	if err := ch.AppendGenesis(*g); err != nil {
		t.Fatalf("genesis append: %v", err)
	}
	if got := ch.BondedSize(attacker.NodeID()); got != 1<<20 {
		t.Fatalf("setup: declared genesis squat not provisionally credited (bonded=%d)", got)
	}

	// V registers its REAL bond live; the anchors propose+attest the block.
	reg, ok := v.RegisterBondReg(g.Hash())
	if !ok {
		t.Fatal("V should mint a live registration")
	}
	blk := &chain.Block{Version: chain.BlockVersion, Height: 1, Prev: g.Hash(),
		Entries: []ports.Entry{mkEntry("e1")}, BondRegs: []chain.BondReg{reg}}
	chain.Sign(blk, pID.Signer())
	blk.Atts = []chain.Attestation{chain.Attest(blk, aID.Signer())}
	if err := ch.Append(*blk); err != nil {
		t.Fatalf("V's genuine live registration must be accepted: %v", err)
	}

	// Proof beats declaration: V is bonded on-chain; the squatter is stripped.
	if got := ch.BondedSize(vID.NodeID()); got != 1<<20 {
		t.Fatalf("G3 regression: honest holder bonded=%d, want %d (its proof must displace the squat)", got, 1<<20)
	}
	if got := ch.BondedSize(attacker.NodeID()); got != 0 {
		t.Fatalf("G3 regression: unproven squatter retains bonded=%d, want 0", got)
	}
}
