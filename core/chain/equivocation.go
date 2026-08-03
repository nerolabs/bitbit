// Equivocation is the consensus analogue of a storage liar: a validator that
// signs two DIFFERENT blocks at the SAME height, trying to make two competing
// histories both look supported. Fork-choice already means only the heavier
// fork stands (see Reconcile), and an honest validator refuses to double-sign
// (core/node); this file adds the PENALTY — a compact, self-verifying proof
// that any node can check and act on, so a proven double-sign costs the actor
// its standing (D2, §3e).
package chain

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"

	"github.com/nerolabs/silt/ports"
)

// Equivocation is self-verifying evidence of a double-sign. It carries the two
// conflicting blocks; any node recomputes their hashes, confirms they are the
// SAME height but DIFFERENT blocks, and that the culprit's signature — as
// proposer OR attester — appears and verifies in BOTH. An honest validator
// signing sequential heights is never implicated (the heights differ); a
// forged accusation fails (the signatures won't verify under the culprit key).
type Equivocation struct {
	Culprit []byte `cbor:"1,keyasint"` // the equivocator's ed25519 public key
	A       Block  `cbor:"2,keyasint"`
	B       Block  `cbor:"3,keyasint"`
}

// CulpritID is the NodeID (hash of the public key) of the equivocator.
func (e *Equivocation) CulpritID() ports.NodeID { return sha256.Sum256(e.Culprit) }

// VerifyEquivocation reports whether e is valid, self-verifying proof of a
// double-sign — no external state needed.
func VerifyEquivocation(e *Equivocation) bool {
	if len(e.Culprit) != ed25519.PublicKeySize {
		return false
	}
	if e.A.Height != e.B.Height {
		return false // sequential signing is not equivocation
	}
	ha, hb := e.A.Hash(), e.B.Hash()
	if ha == hb {
		return false // the same block signed twice is not a conflict
	}
	return signedBlock(e.Culprit, &e.A, ha) && signedBlock(e.Culprit, &e.B, hb)
}

// signedBlock reports whether pub's signature over h appears in b — as its
// proposer or as one of its attesters — and verifies.
func signedBlock(pub []byte, b *Block, h ports.Hash) bool {
	if bytes.Equal(b.Proposer, pub) && ed25519.Verify(ed25519.PublicKey(pub), h[:], b.ProposerSig) {
		return true
	}
	for _, a := range b.Atts {
		if bytes.Equal(a.PubKey, pub) && ed25519.Verify(ed25519.PublicKey(a.PubKey), h[:], a.Sig) {
			return true
		}
	}
	return false
}

// FindEquivocations scans two competing histories for validators who signed a
// DIFFERENT block at the SAME height in each — provable double-signers. When a
// node sees a fork (e.g. reconciling to a heavier one), the two chains are the
// evidence: anyone who backed both sides at a shared height equivocated.
// Returns one proof per distinct culprit.
func FindEquivocations(a, b []Block) []Equivocation {
	byHeight := make(map[uint64]*Block, len(a))
	for i := range a {
		byHeight[a[i].Height] = &a[i]
	}
	var out []Equivocation
	caught := make(map[ports.NodeID]bool)
	for i := range b {
		bb := &b[i]
		ab, ok := byHeight[bb.Height]
		if !ok || ab.Hash() == bb.Hash() {
			continue // no block at this height on the other side, or the same block
		}
		for _, pub := range signers(ab) {
			id := ports.NodeID(sha256.Sum256(pub))
			if caught[id] {
				continue
			}
			e := Equivocation{Culprit: pub, A: *ab, B: *bb}
			if VerifyEquivocation(&e) {
				caught[id] = true
				out = append(out, e)
			}
		}
	}
	return out
}

// signers returns the public keys that signed b (proposer + attesters).
func signers(b *Block) [][]byte {
	out := make([][]byte, 0, 1+len(b.Atts))
	if len(b.Proposer) == ed25519.PublicKeySize {
		out = append(out, b.Proposer)
	}
	for _, a := range b.Atts {
		if len(a.PubKey) == ed25519.PublicKeySize {
			out = append(out, a.PubKey)
		}
	}
	return out
}
