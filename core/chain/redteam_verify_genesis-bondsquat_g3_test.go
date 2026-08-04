package chain

import (
	"crypto/ed25519"
	"testing"

	"github.com/nerolabs/silt/ports"
)

// Retest G3 (Accountability, High) INVERTED as a regression: a malicious genesis
// pre-squats an honest validator's real plot root R under the attacker's key with
// NO space-time proof (genesis regs are declared). Before the fix, the F1
// first-owner dedup then worked AGAINST the true holder — when V later registered
// R on the normal path with a REAL, verifier-accepted proof, apply() saw R
// already owned and dropped V's credit. Now PROOF BEATS DECLARATION: V's verified
// registration displaces the unproven squat, so V earns its bond and the squatter
// loses the standing it never proved.
func TestGenesisBondSquatDisplacedByProof(t *testing.T) {
	const minBond = int64(4) << 20

	attacker := key(1) // controls genesis
	honestV := key(2)  // real space-time holder of plot root R
	anchorP, anchorA := key(10), key(11)
	attackerID, honestID := idOf(attacker), idOf(honestV)
	R := ports.HashBytes([]byte("honest-validator-real-plot-root"))

	cfg := Config{
		Quorum:           1,
		MinBond:          minBond,
		Anchors:          map[ports.NodeID]bool{idOf(anchorP): true, idOf(anchorA): true},
		MatureValidators: 100,
	}
	c := New(cfg, func(ports.NodeID) int64 { return 0 })
	c.SetBondVerifier(objectiveVerify) // accepts answer == "valid"

	// Genesis: attacker squats R with its own key and NO valid proof.
	g := &Block{Version: BlockVersion, Height: 0, Entries: []ports.Entry{entry(0)}}
	squat := BondReg{Validator: pubOf(attacker), Root: R, Size: minBond, Answer: nil}
	squat.Sig = ed25519.Sign(attacker, squat.signingBytes(BondRegNonce(ports.Hash{})))
	g.BondRegs = append(g.BondRegs, squat)
	Sign(g, attacker)
	if err := c.AppendGenesis(*g); err != nil {
		t.Fatalf("genesis append: %v", err)
	}
	// The declared squat is provisionally credited (cold-start declarations grant
	// standing) — but it is UNPROVEN, so a real proof can displace it.
	if got := c.BondedSize(attackerID); got != minBond {
		t.Fatalf("setup: declared genesis reg not provisionally credited (bonded=%d)", got)
	}

	// Normal path: honest V registers R with a REAL verifier-accepted proof.
	nonce := BondRegNonce(g.Hash())
	realReg := BondReg{Validator: pubOf(honestV), Root: R, Size: minBond, Answer: []byte("valid")}
	realReg.Sig = ed25519.Sign(honestV, realReg.signingBytes(nonce))
	b := &Block{Version: BlockVersion, Height: 1, Prev: g.Hash(), BondRegs: []BondReg{realReg}}
	Sign(b, anchorP)
	b.Atts = append(b.Atts, Attest(b, anchorA))
	if err := c.Append(*b); err != nil {
		t.Fatalf("honest V's block should validate (real proof passes validateBondRegs): %v", err)
	}

	// THE FIX: V's proof of possession wins — it earns its bond, and the squatter's
	// unproven standing is stripped.
	if got := c.BondedSize(honestID); got != minBond {
		t.Fatalf("G3 regression: honest holder credited %d, want %d (proof must beat the squat)", got, minBond)
	}
	if got := c.BondedSize(attackerID); got != 0 {
		t.Fatalf("G3 regression: squatter retains %d unproven standing after being displaced, want 0", got)
	}

	// F1 STILL HOLDS: a SECOND identity cannot then also claim R — once proven,
	// first-proven-owner wins, so a shared root never backs two real standings.
	sybil := key(3)
	sReg := BondReg{Validator: pubOf(sybil), Root: R, Size: minBond, Answer: []byte("valid")}
	sReg.Sig = ed25519.Sign(sybil, sReg.signingBytes(BondRegNonce(c.mustHead(t))))
	b2 := &Block{Version: BlockVersion, Height: 2, Prev: c.mustHead(t), BondRegs: []BondReg{sReg}}
	Sign(b2, anchorP)
	b2.Atts = append(b2.Atts, Attest(b2, anchorA))
	if err := c.Append(*b2); err != nil {
		t.Fatalf("block 2 should validate: %v", err)
	}
	if got := c.BondedSize(idOf(sybil)); got != 0 {
		t.Fatalf("F1 regression: a second identity earned %d off the same proven root, want 0", got)
	}
	if got := c.BondedSize(honestID); got != minBond {
		t.Fatalf("F1 regression: the true owner lost its standing to a later claimant (bonded=%d)", got)
	}
}

// mustHead returns the current head hash for building the next block.
func (c *Chain) mustHead(t *testing.T) ports.Hash {
	t.Helper()
	prev, _ := c.Head()
	return prev
}
