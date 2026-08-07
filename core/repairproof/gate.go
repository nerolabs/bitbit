package repairproof

// The retrievability leg and the release/slash gate (design doc §5-§8). The
// correctness leg (repairproof.go) proves the produced shard is the right value;
// this file proves the repairer actually HOLDS it now, and composes the two into a
// bounty verdict.

import (
	"crypto/sha256"

	"github.com/nerolabs/silt/core/por"
	"github.com/nerolabs/silt/ports"
)

// RepairChallengeSeed binds a proof-of-retrievability challenge to the repairer's
// OWN node identity: seed = H("silt/repair/challenge/prover/v1" ‖ base ‖ repairer).
// This closes the double-count / relay attack (design §5): a claimant cannot answer
// a bounty challenge by relaying an existing honest holder's proof of an
// already-present replica, because that proof was aggregated under the holder's
// seed and fails under the claimant's. It mirrors core/node porProverSeed for the
// bond-audit path — the same defense H1/RT-1 gave the standing bond, inherited here
// for the durability bounty.
func RepairChallengeSeed(base [32]byte, repairer ports.NodeID) [32]byte {
	h := sha256.New()
	h.Write([]byte("silt/repair/challenge/prover/v1"))
	h.Write(base[:])
	h.Write(repairer[:])
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// VerifyRetrievability checks that `repairer` actually holds the rebuilt shard NOW,
// via a Shacham–Waters PoR challenge bound to its identity. porKey is derived from
// the file's layout key (por.DeriveKey); unitID is the shard's ID (domain-separates
// the tags); base is the challenge nonce for this round; blocks is the shard's
// block count and count the number sampled. It returns true iff the prover
// demonstrably holds every sampled block — a data-less claimant, or one relaying a
// proof built under another identity's seed, cannot make it verify.
func VerifyRetrievability(porKey *por.Key, unitID []byte, repairer ports.NodeID, base [32]byte, blocks, count int, proof por.Proof) bool {
	if porKey == nil || blocks <= 0 {
		return false
	}
	if count > blocks {
		count = blocks
	}
	seed := RepairChallengeSeed(base, repairer)
	return porKey.Verify(unitID, por.Challenge{Seed: seed, Blocks: blocks, Count: count}, proof)
}

// Decision is the verdict for a repair-bounty claim.
type Decision struct {
	Release bool // pay the durability bounty (credit.PayBounty)
	Slash   bool // attributable false correctness claim → bond-slash (credit.SlashFalseRepair)
}

// Decide gates a repair bounty on the composed proof (design §6, §8), splitting the
// two legs by their trust properties:
//
//   - CORRECTNESS is deterministic and publicly reproducible (VerifyByRecompute), so
//     it is single-verifier-sufficient AND self-attributing: a claim that fails it
//     carries its own fraud proof. correctnessOK=false ⇒ Slash, no release — full
//     stop, regardless of retrievability.
//   - RETRIEVABILITY is where independent verifiers add value (each issues its own
//     identity-bound random challenge), so release additionally requires a τ-of-q
//     quorum of caretakers to confirm it.
//
// A retrievability shortfall (fewer than τ confirmations) DENIES the bounty but does
// NOT slash: it may be transient (the repairer is briefly offline or slow), and only
// the unambiguous, mathematically-attributable correctness lie is ever punished.
// τ must be ≥ 1; a non-positive τ is treated as "no valid quorum" and denies.
func Decide(correctnessOK bool, retrievabilityVotes []bool, tau int) Decision {
	if !correctnessOK {
		return Decision{Release: false, Slash: true}
	}
	if tau < 1 {
		return Decision{} // no valid quorum → deny, don't slash
	}
	confirmed := 0
	for _, ok := range retrievabilityVotes {
		if ok {
			confirmed++
		}
	}
	return Decision{Release: confirmed >= tau}
}
