package chain

import (
	"testing"

	"github.com/nerolabs/silt/ports"
)

// Unit coverage for the objective cold-start (F6): a declared training-wheels
// anchor bootstraps an EMPTY objective set (it may propose/attest before any bond
// is on chain), but that eligibility SHEDS the moment the network matures — an
// anchor with no real bond then no longer qualifies. Eligibility, never weight,
// and never a permanent exemption.
func TestObjectiveLaunchAnchorBootstrapsThenSheds(t *testing.T) {
	anchor := key(1) // a launch anchor with NO real bond
	v := key(2)      // a real, non-anchor bonded validator
	anchorID, vID := idOf(anchor), idOf(v)

	cfg := Config{
		Quorum: 1, MinBond: 1 << 20,
		Anchors:          map[ports.NodeID]bool{anchorID: true},
		MatureValidators: 1, // one real non-anchor validator matures the network
	}
	c := New(cfg, func(ports.NodeID) int64 { return 0 })
	c.SetBondVerifier(objectiveVerify)

	// Genesis seeds only V's real bond — the ANCHOR is NOT bonded on chain.
	g := &Block{Version: BlockVersion, Height: 0, Entries: []ports.Entry{entry(0)},
		BondRegs: []BondReg{bondReg(v, 2<<20, ports.Hash{})}}
	Sign(g, anchor)
	if err := c.AppendGenesis(*g); err != nil {
		t.Fatal(err)
	}

	// Immature: the anchor qualifies by declaration; V qualifies by real bond.
	if c.Mature() {
		t.Fatal("setup: the network should be immature (no non-anchor validator has attested)")
	}
	if !c.attesterQualified(anchorID) || !c.proposerQualified(anchorID) {
		t.Fatal("a launch anchor must be eligible while the network is immature")
	}
	if !c.attesterQualified(vID) {
		t.Fatal("a really-bonded validator must qualify")
	}
	if c.BondedSize(anchorID) != 0 {
		t.Fatal("the anchor holds NO real on-chain bond — its eligibility is a launch crutch, not weight")
	}

	// The anchor bootstraps the first block (it proposes though unbonded); V, a
	// real non-anchor validator, attests — which matures the network.
	b := &Block{Version: BlockVersion, Height: 1, Prev: g.Hash(), Entries: []ports.Entry{entry(1)}}
	Sign(b, anchor)
	b.Atts = []Attestation{Attest(b, v)}
	if err := c.Append(*b); err != nil {
		t.Fatalf("an anchor must be able to bootstrap the first objective block: %v", err)
	}
	if !c.Mature() {
		t.Fatal("after a real non-anchor validator attests, the network should be mature")
	}

	// Shed: the anchor no longer qualifies (no real bond); the real validator does.
	if c.attesterQualified(anchorID) || c.proposerQualified(anchorID) {
		t.Fatal("F6 cold-start: a launch anchor MUST shed eligibility at maturity — no free standing")
	}
	if !c.attesterQualified(vID) {
		t.Fatal("a really-bonded validator still qualifies after maturity")
	}
}
