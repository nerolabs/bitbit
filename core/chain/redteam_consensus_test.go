package chain

import (
	"crypto/ed25519"
	"testing"

	"github.com/nerolabs/silt/ports"
)

// These are the M0 red-team consensus PoCs (F6) INVERTED as regressions: each
// asserts that objective, on-chain-bond fork-choice makes honest replicas AGREE
// where the subjective reputation view let them diverge forever. See
// docs/design/m0-consensus.md §5 and docs/reviews/M0-REDTEAM-REPORT.md §6.

// objectiveVerify is a stub bond verifier: it accepts a registration iff its
// proof bytes are the sentinel "valid". Objectivity (all replicas compute the
// same thing) is the property under test here, not the bond crypto itself —
// that is proven in core/bond. A registration's signature is still real
// ed25519, so a forged registration is genuinely rejected.
func objectiveVerify(_ ports.Hash, _ int64, _ uint64, answer []byte) bool {
	return string(answer) == "valid"
}

// bondReg builds a signed, verifier-accepted registration for priv at the given
// parent hash (its fresh nonce), bonding `size` bytes.
func bondReg(priv ed25519.PrivateKey, size int64, prev ports.Hash) BondReg {
	pub := priv.Public().(ed25519.PublicKey)
	r := BondReg{
		Validator: append([]byte(nil), pub...),
		Root:      ports.HashBytes(pub),
		Size:      size,
		Answer:    []byte("valid"),
	}
	r.Sig = ed25519.Sign(priv, r.signingBytes(BondRegNonce(prev)))
	return r
}

const twoMiB = int64(2) << 20

// objectiveChain builds an objective-mode replica seeded with a genesis that
// bonds the proposer and all validators (2 MiB each). rep is the LOCAL
// reputation view — deliberately made irrelevant by objective mode. Returns the
// chain and the shared genesis (identical across replicas, so forks branch from
// the same hash).
func objectiveChain(prop ed25519.PrivateKey, vals []ed25519.PrivateKey, rep func(ports.NodeID) int64) (*Chain, *Block) {
	c := New(Config{Quorum: 3, MinBond: 1 << 20}, rep)
	c.SetBondVerifier(objectiveVerify)
	g := &Block{Version: BlockVersion, Height: 0, Entries: []ports.Entry{entry(0)}}
	g.BondRegs = append(g.BondRegs, bondReg(prop, twoMiB, ports.Hash{}))
	for _, v := range vals {
		g.BondRegs = append(g.BondRegs, bondReg(v, twoMiB, ports.Hash{}))
	}
	Sign(g, prop)
	if err := c.AppendGenesis(*g); err != nil {
		panic(err)
	}
	return c, g
}

func attestedFork(prop ed25519.PrivateKey, vals []ed25519.PrivateKey, prev ports.Hash, e ports.Entry, nAtt int) *Block {
	b := &Block{Version: BlockVersion, Height: 1, Prev: prev, Entries: []ports.Entry{e}}
	Sign(b, prop)
	for _, v := range vals[:nAtt] {
		b.Atts = append(b.Atts, Attest(b, v))
	}
	return b
}

// F6, core: two replicas whose LOCAL reputation views could not be more
// different (one trusts nobody, one trusts everyone) compute the SAME objective
// fork-choice weight for the same block — because weight is the summed on-chain
// bond, not the local audit view. This is the subjectivity the red-team turned
// into a permanent fork; objective mode removes it.
func TestRedteamF6_ObjectiveWeightAgreesAcrossDivergentReplicas(t *testing.T) {
	prop := key(1)
	vals := []ed25519.PrivateKey{key(2), key(3), key(4), key(5)}

	r1, g := objectiveChain(prop, vals, func(ports.NodeID) int64 { return 0 })         // audited nobody
	r2, _ := objectiveChain(prop, vals, func(ports.NodeID) int64 { return 1_000_000 }) // trusts everyone

	fork := attestedFork(prop, vals, g.Hash(), entry(1), 3)
	if err := r1.Append(*fork); err != nil {
		t.Fatalf("r1 append: %v", err)
	}
	if err := r2.Append(*fork); err != nil {
		t.Fatalf("r2 append: %v", err)
	}
	if r1.Weight() != r2.Weight() {
		t.Fatalf("F6 regression: objective weight diverged across replicas: r1=%d r2=%d", r1.Weight(), r2.Weight())
	}
	if want := int64(3) * twoMiB; r1.Weight() != want {
		t.Fatalf("objective weight = %d, want %d (3 attesters × 2 MiB bond)", r1.Weight(), want)
	}
}

// F6, the heal: two replicas commit DIFFERENT forks at the same height (the
// non-healing partition the red-team built). After exchanging chains, both end
// on the SAME (heavier-bond) fork — regardless of their divergent rep views.
// The lighter side reorgs; the heavier side does not budge.
func TestRedteamF6_ObjectiveForkChoiceHeals(t *testing.T) {
	prop := key(1)
	vals := []ed25519.PrivateKey{key(2), key(3), key(4), key(5)}

	r1, g := objectiveChain(prop, vals, func(ports.NodeID) int64 { return 0 })
	r2, _ := objectiveChain(prop, vals, func(ports.NodeID) int64 { return 1_000_000 })

	forkA := attestedFork(prop, vals, g.Hash(), entry(1), 3) // weight 3×2MiB
	forkB := attestedFork(prop, vals, g.Hash(), entry(2), 4) // weight 4×2MiB — heavier

	if err := r1.Append(*forkA); err != nil {
		t.Fatalf("r1 append A: %v", err)
	}
	if err := r2.Append(*forkB); err != nil {
		t.Fatalf("r2 append B: %v", err)
	}

	// r1 (on the lighter fork A) hears B and must adopt it.
	if adopted, err := r1.Reconcile([]Block{*g, *forkB}); err != nil || !adopted {
		t.Fatalf("r1 must adopt the heavier fork B (adopted=%v err=%v)", adopted, err)
	}
	// r2 (on the heavier fork B) hears A and must NOT switch.
	if adopted, err := r2.Reconcile([]Block{*g, *forkA}); err != nil || adopted {
		t.Fatalf("r2 must keep the heavier fork B (adopted=%v err=%v)", adopted, err)
	}

	// Both replicas now agree on the one history: fork B's entry present, A's gone.
	for name, r := range map[string]*Chain{"r1": r1, "r2": r2} {
		if _, ok := r.LookupRoot(entry(2).Root); !ok {
			t.Fatalf("%s: converged history must carry fork B's entry", name)
		}
		if _, ok := r.LookupRoot(entry(1).Root); ok {
			t.Fatalf("%s: abandoned fork A's entry must be gone", name)
		}
	}
}

// The break, for contrast: in LEGACY (reputation) mode, two replicas with
// different rep views of one validator compute DIFFERENT weights for the same
// fork — the exact divergence objective mode removes. This documents what the
// fix is defending against.
func TestRedteamF6_LegacyWeightDivergesAcrossReplicas(t *testing.T) {
	prop := key(1)
	vals := []ed25519.PrivateKey{key(2), key(3), key(4)}
	cfg := Config{Quorum: 1, MinAttesterRep: 100} // legacy: no MinBond

	// Both trust the proposer and vals[0..1]; they DISAGREE about vals[2].
	base := func(n ports.NodeID) int64 {
		if n == idOf(prop) || n == idOf(vals[0]) || n == idOf(vals[1]) {
			return 1000
		}
		return 0
	}
	r1 := New(cfg, func(n ports.NodeID) int64 {
		if n == idOf(vals[2]) {
			return 1000 // r1 audited vals[2] and sees it qualified
		}
		return base(n)
	})
	r2 := New(cfg, func(n ports.NodeID) int64 {
		if n == idOf(vals[2]) {
			return 0 // r2 never audited vals[2]
		}
		return base(n)
	})

	g := &Block{Version: BlockVersion, Height: 0, Entries: []ports.Entry{entry(0)}}
	Sign(g, prop)
	if err := r1.AppendGenesis(*g); err != nil {
		t.Fatal(err)
	}
	if err := r2.AppendGenesis(*g); err != nil {
		t.Fatal(err)
	}
	fork := attestedFork(prop, vals, g.Hash(), entry(1), 3) // attested by all 3 vals
	if err := r1.Append(*fork); err != nil {
		t.Fatalf("r1 append: %v", err)
	}
	if err := r2.Append(*fork); err != nil {
		t.Fatalf("r2 append: %v", err)
	}
	if r1.Weight() == r2.Weight() {
		t.Fatal("setup: legacy weights should diverge on the disputed validator (the F6 break)")
	}
}

// F4 §2c (privacy): the canonical issuer set is DETERMINISTIC and OBJECTIVE —
// two replicas with maximally-divergent local reputation views produce the
// identical ordered set (bonded size desc, NodeID tiebreak), so every publisher
// asks the same validators and the subset it chose leaks nothing.
func TestCanonicalIssuersDeterministicAndObjective(t *testing.T) {
	prop := key(1)
	vals := []ed25519.PrivateKey{key(2), key(3), key(4), key(5)}
	build := func(rep func(ports.NodeID) int64) *Chain {
		c := New(Config{Quorum: 3, MinBond: 1 << 20}, rep)
		c.SetBondVerifier(objectiveVerify)
		g := &Block{Version: BlockVersion, Height: 0, Entries: []ports.Entry{entry(0)}}
		g.BondRegs = []BondReg{
			bondReg(prop, 5<<20, ports.Hash{}),
			bondReg(vals[0], 4<<20, ports.Hash{}),
			bondReg(vals[1], 3<<20, ports.Hash{}),
			bondReg(vals[2], 2<<20, ports.Hash{}), // tie at 2 MiB with vals[3]
			bondReg(vals[3], 2<<20, ports.Hash{}),
		}
		Sign(g, prop)
		if err := c.AppendGenesis(*g); err != nil {
			panic(err)
		}
		return c
	}
	r1 := build(func(ports.NodeID) int64 { return 0 })
	r2 := build(func(ports.NodeID) int64 { return 1_000_000 })

	a, b := r1.CanonicalIssuers(0), r2.CanonicalIssuers(0)
	if len(a) != 5 || len(b) != 5 {
		t.Fatalf("canonical set should hold all 5 bonded validators, got %d/%d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatal("F4 §2c: canonical issuer set differs across divergent replicas — subset choice would leak")
		}
	}
	if a[0] != idOf(prop) {
		t.Fatal("the heaviest bond must come first")
	}
	// The 2 MiB tie between vals[2] and vals[3] breaks by lower NodeID.
	lo, hi := idOf(vals[2]), idOf(vals[3])
	if bytesLess(hi[:], lo[:]) {
		lo, hi = hi, lo
	}
	if a[3] != lo || a[4] != hi {
		t.Fatal("a bonded-size tie must break by lower NodeID, deterministically")
	}
	if got := r1.CanonicalIssuers(2); len(got) != 2 || got[0] != a[0] || got[1] != a[1] {
		t.Fatal("max should cap to the top-N by bonded weight")
	}
}

// A forged on-chain bond registration cannot buy objective weight: neither a
// bad space-time proof nor a bad signature is accepted, so a validator cannot
// register standing it did not prove.
func TestRedteamF6_ForgedBondRegDenied(t *testing.T) {
	prop := key(1)
	vals := []ed25519.PrivateKey{key(2), key(3), key(4), key(5)}
	c, g := objectiveChain(prop, vals, func(ports.NodeID) int64 { return 0 })

	newcomer := key(9)

	// (a) A registration whose proof the verifier rejects ("forged" != "valid").
	badProof := bondReg(newcomer, twoMiB, g.Hash())
	badProof.Answer = []byte("forged")
	// re-sign so only the PROOF is bad, isolating the space-time check
	badProof.Sig = ed25519.Sign(newcomer, badProof.signingBytes(BondRegNonce(g.Hash())))
	blkA := &Block{Version: BlockVersion, Height: 1, Prev: g.Hash(), BondRegs: []BondReg{badProof}}
	Sign(blkA, prop)
	for _, v := range vals[:3] {
		blkA.Atts = append(blkA.Atts, Attest(blkA, v))
	}
	if err := c.Append(*blkA); err == nil {
		t.Fatal("F6 regression: a registration with an invalid space-time proof was accepted")
	}

	// (b) A registration with a valid proof but a bad signature (claimed by
	// someone who does not hold the key) is rejected.
	badSig := bondReg(newcomer, twoMiB, g.Hash())
	badSig.Sig[0] ^= 0xff
	blkB := &Block{Version: BlockVersion, Height: 1, Prev: g.Hash(), BondRegs: []BondReg{badSig}}
	Sign(blkB, prop)
	for _, v := range vals[:3] {
		blkB.Atts = append(blkB.Atts, Attest(blkB, v))
	}
	if err := c.Append(*blkB); err == nil {
		t.Fatal("F6 regression: a registration with a forged signature was accepted")
	}

	// Sanity: a well-formed registration IS accepted, so the newcomer joins the
	// objective validator set.
	good := bondReg(newcomer, twoMiB, g.Hash())
	blkC := &Block{Version: BlockVersion, Height: 1, Prev: g.Hash(), BondRegs: []BondReg{good}}
	Sign(blkC, prop)
	for _, v := range vals[:3] {
		blkC.Atts = append(blkC.Atts, Attest(blkC, v))
	}
	if err := c.Append(*blkC); err != nil {
		t.Fatalf("a valid registration must be accepted: %v", err)
	}
	if c.BondedSize(idOf(newcomer)) != twoMiB {
		t.Fatalf("newcomer bonded size = %d, want %d", c.BondedSize(idOf(newcomer)), twoMiB)
	}
}
