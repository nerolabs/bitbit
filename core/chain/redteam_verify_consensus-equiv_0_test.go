package chain

import (
	"crypto/ed25519"
	"testing"

	"github.com/nerolabs/silt/ports"
)

// Blind red-team F2 (consensus, equivocation slash inert) INVERTED as a
// regression: in the M0-default objective mode, a proven double-sign now costs
// the actor its standing. The slash is an ON-CHAIN record (Block.Slashes) the
// objective set honors — a self-verifying equivocation proof that, on commit,
// evicts the culprit from c.bonded and bars it from re-earning standing. The
// old inert path (slashing only the reputation ledger the objective set never
// reads) is replaced.

func objectiveThree() (*Chain, ed25519.PrivateKey, ed25519.PrivateKey, ed25519.PrivateKey, *Block) {
	equiv, att, prop := key(101), key(102), key(103)
	c := New(Config{Quorum: 1, MinBond: 1 << 20}, func(ports.NodeID) int64 { return 0 })
	c.SetBondVerifier(objectiveVerify)
	g := &Block{Version: BlockVersion, Height: 0, Entries: []ports.Entry{entry(0)},
		BondRegs: []BondReg{bondReg(equiv, twoMiB, ports.Hash{}), bondReg(att, twoMiB, ports.Hash{}), bondReg(prop, twoMiB, ports.Hash{})}}
	Sign(g, prop)
	if err := c.AppendGenesis(*g); err != nil {
		panic(err)
	}
	return c, equiv, att, prop, g
}

// A committed on-chain equivocation proof evicts the culprit from the objective
// set: no eligibility, no fork-choice weight, and it cannot re-register.
func TestObjectiveEquivocationSlashEvicts(t *testing.T) {
	c, equiv, att, prop, g := objectiveThree()
	equivID := idOf(equiv)
	if !c.attesterQualified(equivID) || c.BondedSize(equivID) != twoMiB {
		t.Fatal("setup: the equivocator should start fully bonded and qualified")
	}

	// Evidence: the equivocator PROPOSES two different blocks at the same height.
	mkFork := func(tag byte) Block {
		b := Block{Version: BlockVersion, Height: 1, Prev: g.Hash(), Entries: []ports.Entry{entry(tag)}}
		Sign(&b, equiv)
		return b
	}
	proof := Equivocation{
		Culprit: append([]byte(nil), equiv.Public().(ed25519.PublicKey)...),
		A:       mkFork(10), B: mkFork(11),
	}
	if !VerifyEquivocation(&proof) {
		t.Fatal("setup: the equivocation proof should self-verify")
	}

	// Commit a slash block (prop proposes, att attests) carrying the proof.
	sb := &Block{Version: BlockVersion, Height: 1, Prev: g.Hash(), Slashes: []Equivocation{proof}}
	Sign(sb, prop)
	sb.Atts = []Attestation{Attest(sb, att)}
	if err := c.Append(*sb); err != nil {
		t.Fatalf("a valid slash block should commit: %v", err)
	}

	// DENIED: the equivocator is evicted — the deterrent bites the objective set.
	if c.attesterQualified(equivID) || c.proposerQualified(equivID) {
		t.Fatal("F2 regression: a proven equivocator must be evicted from the objective set")
	}
	if got := c.BondedSize(equivID); got != 0 {
		t.Fatalf("F2 regression: slashed equivocator bonded=%d, want 0", got)
	}

	// And it cannot re-earn standing by re-registering its bond.
	reg := bondReg(equiv, twoMiB, sb.Hash())
	nb := &Block{Version: BlockVersion, Height: 2, Prev: sb.Hash(), BondRegs: []BondReg{reg}}
	Sign(nb, prop)
	nb.Atts = []Attestation{Attest(nb, att)}
	if err := c.Append(*nb); err != nil {
		t.Fatalf("the re-registration block (att quorum) should still commit: %v", err)
	}
	if c.attesterQualified(equivID) || c.BondedSize(equivID) != 0 {
		t.Fatal("F2 regression: a slashed equivocator must not be able to re-earn standing")
	}
}

// Forged-slash griefing stays DENIED: a slash whose proof does not verify (the
// named culprit did not sign both blocks) is rejected, so an honest validator
// cannot be evicted by fabrication.
func TestForgedSlashRejected(t *testing.T) {
	c, equiv, att, prop, g := objectiveThree()

	// A block equiv did NOT sign (proposed by att), paired with one it did — the
	// culprit named is equiv but it did not sign both, so the proof is invalid.
	notSigned := Block{Version: BlockVersion, Height: 1, Prev: g.Hash(), Entries: []ports.Entry{entry(20)}}
	Sign(&notSigned, att)
	signed := Block{Version: BlockVersion, Height: 1, Prev: g.Hash(), Entries: []ports.Entry{entry(21)}}
	Sign(&signed, equiv)
	forged := Equivocation{
		Culprit: append([]byte(nil), equiv.Public().(ed25519.PublicKey)...),
		A:       notSigned, B: signed,
	}
	if VerifyEquivocation(&forged) {
		t.Fatal("setup: a forged proof must not verify")
	}

	sb := &Block{Version: BlockVersion, Height: 1, Prev: g.Hash(), Slashes: []Equivocation{forged}}
	Sign(sb, prop)
	sb.Atts = []Attestation{Attest(sb, att)}
	if err := c.Append(*sb); err == nil {
		t.Fatal("F2 regression: a forged equivocation slash must be rejected (forged-slash griefing denied)")
	}
	if c.BondedSize(idOf(equiv)) != twoMiB {
		t.Fatal("F2 regression: a forged slash must not have evicted the honest validator")
	}
}
