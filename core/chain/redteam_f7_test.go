package chain

import (
	"crypto/ed25519"
	"testing"

	"github.com/nerolabs/silt/ports"
)

// F7 (red-team) — cross-height double-backing: a validator signs blocks on two
// incompatible forks but never the SAME height on both, evading the same-height
// equivocation slash. The sound resolution, verified below:
//
//  1. SAME-HEIGHT double-signing IS slashed (the provable, distinguishable
//     misbehavior) — FindEquivocations catches it.
//  2. A cross-height reorg-follow is PROVABLY INDISTINGUISHABLE from malicious
//     cross-height double-backing from the blocks alone: an honest validator that
//     attested A@1, then followed a heavier fork and attested B@2 (B not
//     containing A@1), produced the IDENTICAL evidence. So a rule that slashed
//     "signed two incompatible forks" would slash honest reorg-followers — a
//     regression of the forged-slash-griefing corner that HOLDS. FindEquivocations
//     therefore (correctly) does NOT flag it.
//  3. It is NEUTRALIZED by objective fork-choice (F6): the double-backer cannot
//     make both histories stand — the heavier-bond fork wins on every replica —
//     so the unslashed misbehavior gains nothing.
//
// Adopting a finality gadget (Casper-FFG source/target votes) purely to enable
// surround-slashing would be a large architectural addition for a threat F6
// already neutralizes, and even then would not flag the A@1/B@2 pattern (its
// spans do not surround). See docs/design/m0-consensus.md.

// f7Block builds a block at height on prev, proposed by prop and attested by atts.
func f7Block(prop ed25519.PrivateKey, height uint64, prev ports.Hash, e ports.Entry, atts ...ed25519.PrivateKey) Block {
	b := Block{Version: BlockVersion, Height: height, Prev: prev, Entries: []ports.Entry{e}}
	Sign(&b, prop)
	for _, a := range atts {
		b.Atts = append(b.Atts, Attest(&b, a))
	}
	return b
}

// (1) Same-height double-signing is caught: a validator that attests two
// DIFFERENT blocks at the same height on competing forks is a provable
// equivocator and is flagged.
func TestF7_SameHeightDoubleSignIsCaught(t *testing.T) {
	propA, propB := key(1), key(5)
	v := key(2)
	g := f7Block(propA, 0, ports.Hash{}, entry(0))

	a1 := f7Block(propA, 1, g.Hash(), entry(10), v, key(3))
	b1 := f7Block(propB, 1, g.Hash(), entry(11), v, key(4)) // v signs BOTH at height 1

	eqs := FindEquivocations([]Block{g, a1}, []Block{g, b1})
	found := false
	for _, e := range eqs {
		if e.CulpritID() == idOf(v) {
			found = true
		}
	}
	if !found {
		t.Fatal("a same-height cross-fork double-sign must be caught")
	}
}

// (2) THE GUARD: an honest reorg-follower — attested A@1, then followed a heavier
// fork and attested B@2 (never the same height on both) — is NOT flagged. If this
// ever fails, a slashing change is punishing honest validators (a regression).
func TestF7_HonestReorgFollowerNotSlashed(t *testing.T) {
	propA, propB := key(1), key(5)
	v := key(2)
	g := f7Block(propA, 0, ports.Hash{}, entry(0))

	// Fork A: genesis → A1, attested by V (and a disjoint validator).
	a1 := f7Block(propA, 1, g.Hash(), entry(10), v, key(3))
	// Fork B (heavier, the one V reorgs onto): genesis → B1 → B2. V sits out B1
	// and attests B2 — the classic cross-height pattern, here entirely honest.
	b1 := f7Block(propB, 1, g.Hash(), entry(11), key(4), key(6))
	b2 := f7Block(propB, 2, b1.Hash(), entry(12), v, key(4))

	eqs := FindEquivocations([]Block{g, a1}, []Block{g, b1, b2})
	for _, e := range eqs {
		if e.CulpritID() == idOf(v) {
			t.Fatal("F7 guard: an honest cross-height reorg-follower must NOT be slashed — " +
				"its evidence is indistinguishable from malicious double-backing")
		}
	}
}

// (3) NEUTRALIZED by F6: even unslashed, a cross-height double-backer cannot make
// both histories stand — objective fork-choice adopts the heavier-bond fork, so
// the fork carrying its extra attestation is abandoned on reconcile.
func TestF7_ObjectiveForkChoiceNeutralizesDoubleBacker(t *testing.T) {
	prop := key(1)
	vals := []ed25519.PrivateKey{key(2), key(3), key(4), key(5)}
	v := vals[0] // the double-backer

	c, g := objectiveChain(prop, vals, func(ports.NodeID) int64 { return 0 })

	// Fork A (lighter): one block at height 1, attested by V + 2 others.
	forkA := attestedFork(prop, vals, g.Hash(), entry(1), 3)
	// Fork B (heavier): two blocks; V double-backs by also attesting on B.
	b1 := f7Block(prop, 1, g.Hash(), entry(2), vals[1], vals[2], vals[3])
	b2 := f7Block(prop, 2, b1.Hash(), entry(3), v, vals[1], vals[2])

	if err := c.Append(*forkA); err != nil {
		t.Fatalf("append fork A: %v", err)
	}
	// The replica hears the heavier fork B and reorgs onto it — fork A (with the
	// double-backer's attestation) does not stand.
	adopted, err := c.Reconcile([]Block{*g, b1, b2})
	if err != nil || !adopted {
		t.Fatalf("the heavier fork B must be adopted (adopted=%v err=%v)", adopted, err)
	}
	if _, ok := c.LookupRoot(entry(1).Root); ok {
		t.Fatal("F7: the double-backer's fork A must be abandoned — it cannot make both histories stand")
	}
	if _, ok := c.LookupRoot(entry(3).Root); !ok {
		t.Fatal("the heavier fork B must stand")
	}
}
