package chain

import (
	"crypto/ed25519"
	"testing"

	"github.com/nerolabs/silt/ports"
)

func pubOf(p ed25519.PrivateKey) []byte { return []byte(p.Public().(ed25519.PublicKey)) }

// conflicting builds two DIFFERENT blocks at height 1 (different entries) on
// the same genesis, each with the given attesters.
func (w *world) conflicting(g *Block, proposerA, proposerB ed25519.PrivateKey, attA, attB []ed25519.PrivateKey) (a, b *Block) {
	a = &Block{Version: BlockVersion, Height: 1, Prev: g.Hash(), Entries: []ports.Entry{entry(1)}}
	Sign(a, proposerA)
	for _, v := range attA {
		a.Atts = append(a.Atts, Attest(a, v))
	}
	b = &Block{Version: BlockVersion, Height: 1, Prev: g.Hash(), Entries: []ports.Entry{entry(2)}}
	Sign(b, proposerB)
	for _, v := range attB {
		b.Atts = append(b.Atts, Attest(b, v))
	}
	return
}

// A validator that signs two different blocks at the same height is provably an
// equivocator; one signing sequential heights, or accused without a signature,
// is not.
func TestEquivocationProof(t *testing.T) {
	w := newWorld(DefaultConfig())
	g := w.genesis()
	// vals[0] attests BOTH forks; the two forks have different proposers.
	a, b := w.conflicting(g, w.prop, w.vals[3], []ed25519.PrivateKey{w.vals[0]}, []ed25519.PrivateKey{w.vals[0]})

	if !VerifyEquivocation(&Equivocation{Culprit: pubOf(w.vals[0]), A: *a, B: *b}) {
		t.Fatal("a validator signing two different blocks at the same height must be provable")
	}

	// Sequential heights are honest, not equivocation.
	c := &Block{Version: BlockVersion, Height: 2, Prev: a.Hash(), Entries: []ports.Entry{entry(3)}}
	Sign(c, w.prop)
	c.Atts = []Attestation{Attest(c, w.vals[0])}
	if VerifyEquivocation(&Equivocation{Culprit: pubOf(w.vals[0]), A: *a, B: *c}) {
		t.Fatal("signing sequential heights must not count as equivocation")
	}

	// A validator who signed neither block cannot be framed.
	if VerifyEquivocation(&Equivocation{Culprit: pubOf(w.vals[1]), A: *a, B: *b}) {
		t.Fatal("a validator who did not sign both blocks must not be implicated")
	}
	// The same block is not a conflict with itself.
	if VerifyEquivocation(&Equivocation{Culprit: pubOf(w.vals[0]), A: *a, B: *a}) {
		t.Fatal("the same block is not equivocation")
	}
}

// Given two competing histories, every cross-fork double-signer is caught and
// no honest one-fork signer is.
func TestFindEquivocationsAcrossForks(t *testing.T) {
	w := newWorld(DefaultConfig())
	g := w.genesis()
	// vals[0], vals[1] attest both forks (equivocators); vals[2] only fork A.
	a, b := w.conflicting(g, w.prop, w.vals[3],
		[]ed25519.PrivateKey{w.vals[0], w.vals[1], w.vals[2]},
		[]ed25519.PrivateKey{w.vals[0], w.vals[1]})

	got := FindEquivocations([]Block{*g, *a}, []Block{*g, *b})
	caught := map[ports.NodeID]bool{}
	for i := range got {
		caught[got[i].CulpritID()] = true
	}
	if !caught[idOf(w.vals[0])] || !caught[idOf(w.vals[1])] {
		t.Fatal("both cross-fork attesters must be caught")
	}
	if caught[idOf(w.vals[2])] {
		t.Fatal("a validator who backed only one fork must not be implicated")
	}
	// Every returned proof is genuinely self-verifying.
	for i := range got {
		if !VerifyEquivocation(&got[i]) {
			t.Fatalf("FindEquivocations returned an unverifiable proof for %s", got[i].CulpritID())
		}
	}
}
