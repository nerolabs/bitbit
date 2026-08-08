package chain

import (
	"crypto/ed25519"
	"testing"

	"github.com/nerolabs/silt/ports"
)

// bondRegDom is bondReg with a committed A-axis domain label (BondReg.Domain).
func bondRegDom(priv ed25519.PrivateKey, size int64, prev ports.Hash, domain uint64) BondReg {
	pub := priv.Public().(ed25519.PublicKey)
	r := BondReg{
		Validator: append([]byte(nil), pub...),
		Root:      ports.HashBytes(pub),
		Size:      size,
		Answer:    []byte("valid"),
		Domain:    domain,
	}
	r.Sig = ed25519.Sign(priv, r.signingBytes(BondRegNonce(prev)))
	return r
}

type c2DomBond struct {
	priv ed25519.PrivateKey
	size int64
	dom  uint64
}

// buildC2Dom mirrors buildC2 but stamps a committed domain on each bond, and
// enrolls each as a non-proposer attester so it enters the participating set.
func buildC2Dom(t *testing.T, k int, bonds []c2DomBond) *Chain {
	t.Helper()
	const minBond = int64(1) << 20
	a1, a2 := key(1), key(2)
	cfg := Config{
		Quorum: 2, MinBond: minBond, MinProposerRep: 0, MinAttesterRep: 0,
		Anchors:          map[ports.NodeID]bool{idOf(a1): true, idOf(a2): true},
		AnchorQuorum:     1,
		MatureValidators: k,
		OperatorMargin:   1,
	}
	c := New(cfg, func(ports.NodeID) int64 { return 0 })
	c.SetBondVerifier(objectiveVerify)
	regs := []BondReg{bondReg(a1, minBond, ports.Hash{}), bondReg(a2, minBond, ports.Hash{})}
	for _, b := range bonds {
		regs = append(regs, bondRegDom(b.priv, b.size, ports.Hash{}, b.dom))
	}
	g := &Block{Version: BlockVersion, Height: 0, Entries: []ports.Entry{entry(0)}, BondRegs: regs}
	Sign(g, a1)
	if err := c.AppendGenesis(*g); err != nil {
		t.Fatalf("genesis: %v", err)
	}
	prev := g
	for _, b := range bonds {
		prev = appendCommit(t, c, a1, prev, a2, b.priv)
	}
	return c
}

// TestC2Metric_AddressDiversityGate pins the A axis (D-C2): the maturity shed counts
// ADDRESS-DIVERSE participants, so a stake split across many keys in ONE declared
// domain cannot fake the decentralization that retires the launch anchors — only
// distinct domains can. Unset domains reproduce the pre-A-axis behavior exactly.
func TestC2Metric_AddressDiversityGate(t *testing.T) {
	const minBond = int64(1) << 20
	const d1, d2, d3, d4 = uint64(0xA1), uint64(0xB2), uint64(0xC3), uint64(0xD4)

	// A stake split across 4 keys ALL in one domain → one diversity group. Even
	// though there are 4 distinct BONDS, there is 1 distinct DOMAIN, so the shed
	// (which needs MatureValidators=2 distinct domains) does NOT fire.
	same := buildC2Dom(t, 2, []c2DomBond{
		{key(20), minBond, d1}, {key(21), minBond, d1},
		{key(22), minBond, d1}, {key(23), minBond, d1},
	})
	sm := same.C2Metric()
	if sm.DistinctDomains != 1 || sm.NakamotoDomains != 1 {
		t.Fatalf("4 keys in ONE domain must count as 1 diverse group: DistinctDomains=%d NakamotoDomains=%d", sm.DistinctDomains, sm.NakamotoDomains)
	}
	if same.Mature() {
		t.Fatal("A-axis: a stake split across 4 keys in ONE domain must NOT shed the wheels")
	}

	// 4 equal bonds across 4 distinct domains → 4 diversity groups; the shed fires.
	diverse := buildC2Dom(t, 2, []c2DomBond{
		{key(30), minBond, d1}, {key(31), minBond, d2},
		{key(32), minBond, d3}, {key(33), minBond, d4},
	})
	dm := diverse.C2Metric()
	if dm.DistinctDomains != 4 || dm.NakamotoDomains != dm.NakamotoBonds {
		t.Fatalf("4 distinct domains: DistinctDomains=%d NakamotoDomains=%d NakamotoBonds=%d", dm.DistinctDomains, dm.NakamotoDomains, dm.NakamotoBonds)
	}
	if !diverse.Mature() {
		t.Fatal("A-axis: 4 address-diverse validators must shed the wheels")
	}

	// Backward-compat: domain 0 (unset) → each bond independent → NakamotoDomains
	// equals NakamotoBonds and maturity is exactly the pre-A-axis behavior.
	unset := buildC2Dom(t, 2, []c2DomBond{
		{key(40), minBond, 0}, {key(41), minBond, 0},
		{key(42), minBond, 0}, {key(43), minBond, 0},
	})
	um := unset.C2Metric()
	if um.NakamotoDomains != um.NakamotoBonds {
		t.Fatalf("unset domains must behave as pre-A-axis: NakamotoDomains=%d NakamotoBonds=%d", um.NakamotoDomains, um.NakamotoBonds)
	}
	if !unset.Mature() {
		t.Fatal("A-axis regression: an unset-domain set must mature exactly as before")
	}
}

// TestBondRegDomainSignatureBackwardCompatible pins that adding the committed domain
// did not change the signed message for a domain-0 bond (so every existing/genesis
// signature still verifies), while a domain-N bond binds the domain to the signature.
func TestBondRegDomainSignatureBackwardCompatible(t *testing.T) {
	k := key(7)
	nonce := BondRegNonce(ports.Hash{})
	r0 := BondReg{Validator: []byte(k.Public().(ed25519.PublicKey)), Root: ports.HashBytes([]byte("x")), Size: 1 << 20}
	// The pre-A-axis message is: tag(21) ‖ root(32) ‖ size+nonce(16) = 69 bytes.
	const legacyLen = 21 + 32 + 16
	if got := len(r0.signingBytes(nonce)); got != legacyLen {
		t.Fatalf("domain-0 signing message must be the pre-A-axis length: got %d want %d", got, legacyLen)
	}
	// A committed domain extends (binds into) the signed message, so it can't be
	// swapped after signing.
	rD := r0
	rD.Domain = 0xBEEF
	if len(rD.signingBytes(nonce)) <= legacyLen {
		t.Fatal("a committed domain must extend (bind into) the signed message")
	}
}
