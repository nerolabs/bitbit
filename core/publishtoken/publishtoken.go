// Package publishtoken is the quorum-issued publish token (T3, risk 14 / F1):
// a publisher's authorship is unlinked from its durable reputation key without
// a single issuer. A token is a random serial blind-signed by a QUORUM of
// distinct validators, each with its own issuer key — the same "k distinct
// qualified validators" shape the block chain already uses, so no single party
// can mint publish rights and no trusted key-setup ceremony is needed.
//
// Each signature is a Chaumian blind signature (core/blindtoken): a validator
// charges the fee to the requesting identity and signs a BLINDED serial, so it
// learns nothing linkable. The token later spends as (serial, k·sig) carrying
// no durable identity. What a colluding validator set can still narrow is the
// anonymity SET — identities that requested from the same validator subset in
// the same epoch — not the publisher directly; mitigate by requesting from a
// canonical validator set so everyone shares one set (documented, honest).
package publishtoken

import (
	"crypto/rsa"
	"errors"
	"fmt"

	"github.com/nerolabs/silt/core/blindtoken"
	"github.com/nerolabs/silt/ports"
)

var (
	ErrBadSig           = errors.New("publishtoken: a validator signature does not verify")
	ErrInsufficientSigs = errors.New("publishtoken: too few distinct qualified validator signatures")
)

// Verify checks a quorum token (ports.PublishToken): at least k DISTINCT
// validators, each qualified (qualified) and each signature valid under that
// validator's issuer key (issuerKey). Mirrors chain.ValidateCommit — distinct,
// qualified, count ≥ k. A signature that is present but forged fails the whole
// token (a liar); an unknown or unqualified validator's signature simply
// doesn't count.
func Verify(tok ports.PublishToken, k int, issuerKey func(ports.NodeID) *rsa.PublicKey, qualified func(ports.NodeID) bool) error {
	seen := make(map[ports.NodeID]bool)
	valid := 0
	for _, s := range tok.Sigs {
		if seen[s.Validator] {
			continue
		}
		pub := issuerKey(s.Validator)
		if pub == nil || !qualified(s.Validator) {
			continue // unknown or unqualified issuer — ignored, not fatal
		}
		if !blindtoken.Verify(pub, tok.Serial, s.Sig) {
			return fmt.Errorf("%w: validator %s", ErrBadSig, s.Validator)
		}
		seen[s.Validator] = true
		valid++
	}
	if valid < k {
		return fmt.Errorf("%w: %d of %d", ErrInsufficientSigs, valid, k)
	}
	return nil
}
