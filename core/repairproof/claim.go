package repairproof

// RepairClaim is the wire announcement a caretaker broadcasts after it
// reconstructs a lost shard and places the rebuilt copy on a fresh storage node
// (the paramedic split — design doc §8b). It names WHAT was repaired and WHO now
// holds it, so the object's other caretakers can independently verify and settle
// the bounty. It deliberately does NOT carry the survivor set or the code
// parameters: a verifying caretaker takes those from the manifest it already holds
// and picks its own k survivors to recompute — so a claimant cannot bias the
// correctness check by dictating which survivors to use.
//
// The claim is not trusted on its face: correctness is re-derived (VerifyByRecompute
// against the manifest-committed ShardID) and retrievability is re-challenged
// against Holder under an identity-bound seed. The claim only points the verifiers
// at the work; the proofs are recomputed.

import (
	"github.com/fxamacker/cbor/v2"
	"github.com/nerolabs/silt/ports"
)

// RepairClaim identifies one repaired shard and its new holder.
type RepairClaim struct {
	// Root is the object whose durability escrow pays the bounty.
	Root ports.Hash `cbor:"1,keyasint"`
	// Stripe is the stripe index within the file, ShardPos the position within the
	// stripe (0..n-1: data 0..k-1, parity k..n-1). Together with the manifest they
	// pin which codeword coordinate was repaired.
	Stripe   int `cbor:"2,keyasint"`
	ShardPos int `cbor:"3,keyasint"`
	// ShardID is the manifest-committed content ID of the repaired shard — the
	// correctness target a verifier recomputes to.
	ShardID ports.ChunkID `cbor:"4,keyasint"`
	// Holder is the fresh storage node now holding the rebuilt shard — the bounty
	// payee and the target of the retrievability challenge (design §8b).
	Holder ports.NodeID `cbor:"5,keyasint"`
}

// Marshal serializes the claim to canonical CBOR for the wire.
func (c RepairClaim) Marshal() ([]byte, error) {
	enc, err := cbor.CanonicalEncOptions().EncMode()
	if err != nil {
		return nil, err
	}
	return enc.Marshal(c)
}

// UnmarshalClaim parses a wire claim. A caller should treat every field as
// untrusted input to be re-verified, never as established fact.
func UnmarshalClaim(b []byte) (RepairClaim, error) {
	var c RepairClaim
	if err := cbor.Unmarshal(b, &c); err != nil {
		return RepairClaim{}, err
	}
	return c, nil
}
