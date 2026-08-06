// Package repairproof is the correctness leg of H7 proof-of-correct-repair
// (docs/design/h7-proof-of-repair.md): given a caretaker's CLAIM that it repaired
// a lost shard, decide whether the produced shard really is the correct Reed–
// Solomon codeword coordinate for its stripe — so a repair bounty pays only for a
// real, correct repair, never a bare claim.
//
// M0 uses the RECOMPUTE construction: reconstruct the target position from k
// survivor shards and check the result is byte-identical to what the manifest
// already commits to (its content-addressed shard ID). This is:
//
//   - SOUND — Reed–Solomon reconstruction is deterministic, so the claim is
//     accepted iff the produced bytes ARE the codeword coordinate the survivors
//     imply, and the survivors are pinned by their manifest-anchored IDs. There is
//     no other ground truth in a content-addressed system: "correct" means
//     "consistent with the manifest-anchored survivors under the public code."
//   - PUBLICLY CHECKABLE — anyone holding the survivor bytes and the committed
//     target ID can rerun it and get the same answer, so a false claim's rejection
//     is a reproducible fraud proof (no trusted verifier, no secret key).
//   - CONTENT-BLIND — silt caretakers verify over ciphertext shards under the
//     layout key; no plaintext is ever seen.
//
// What it is NOT (an explicit M0 non-goal): it is not BANDWIDTH-blind — the
// verifier must hold k survivor shards to recompute. The succinct, fetch-free
// homomorphic-commitment upgrade is impossible in pure Go over silt's GF(2^8)
// storage without changing the storage code: there is no ring homomorphism
// GF(2^8) → F_r (characteristic 2 vs r), so a prime-field Pedersen/KZG commitment
// cannot carry the GF(2^8) XOR relation with a linear homomorphic check. The two
// blind upgrades — moving the correctness code to F_p (Semi-AVID-PR) or adopting a
// characteristic-2-native binding commitment (FRI-Binius) — are fast-follows, not
// M0 (see the design doc §3-§4, §7).
//
// This package is pure: it speaks in shard bytes and IDs, touches no store, no
// network, no ledger. Wiring it into the repair loop, binding it to a Shacham–
// Waters retrievability proof under the repairer's identity, gating the bounty
// release on a caretaker quorum, and slashing an attributable false claim are the
// later steps of the slice (design doc §5-§8).
package repairproof

import (
	"errors"
	"fmt"

	"github.com/nerolabs/silt/core/erasure"
	"github.com/nerolabs/silt/ports"
)

// ErrUnrecoverable is returned when fewer than k survivor shards are supplied, so
// the target cannot be reconstructed at all — a claim that cannot even be checked
// is rejected, never accepted by default.
var ErrUnrecoverable = errors.New("repairproof: fewer than k survivors — target not reconstructable")

// VerifyByRecompute reports whether a claimed repaired shard for stripe position
// `target` is the correct RS codeword coordinate, by reconstructing it from the
// supplied survivors and checking the reconstruction is byte-identical to the
// shard the manifest commits to (its content address `want`).
//
//   - p is the stripe's (k, n) code.
//   - survivors maps a stripe position (0..n-1, data 0..k-1 then parity k..n-1) to
//     that shard's bytes. It must hold at least k entries, none at `target`; the
//     bytes are trusted only as far as the caller has verified them against their
//     own committed IDs (a survivor whose bytes don't match its ID is not a
//     survivor — verify before calling).
//   - realData is how many leading data positions of the stripe are real chunks
//     (the rest are implicit zero padding of a short final stripe); it must be
//     1..k, exactly as core/erasure uses it.
//   - want is the manifest-committed content ID of the target shard.
//
// It returns true iff the recomputed target hashes to `want`. A wrong-but-claimed
// shard, a garbage claim, or a survivor set that reconstructs to something else all
// return false. It errors only on structurally invalid input (bad params, target
// out of range, a survivor at the target, too few survivors) — an error is a
// malformed challenge, distinct from a well-formed claim that fails to verify.
func VerifyByRecompute(p erasure.Params, survivors map[int][]byte, realData, target int, want ports.ChunkID) (bool, error) {
	if err := p.Validate(); err != nil {
		return false, err
	}
	if target < 0 || target >= p.N {
		return false, fmt.Errorf("repairproof: target position %d out of range 0..%d", target, p.N-1)
	}
	if realData < 1 || realData > p.K {
		return false, fmt.Errorf("repairproof: realData=%d, want 1..%d", realData, p.K)
	}
	if target < realData {
		// A real data position is content the object actually carries; verifying it
		// is legitimate. A padding position (realData..k-1) is implicit zero, never
		// stored and never repaired, so a claim to have "repaired" one is malformed.
	} else if target < p.K {
		return false, fmt.Errorf("repairproof: target %d is implicit zero padding, not a repairable shard", target)
	}

	// Assemble the n-slot stripe: survivors in place, everything else (including the
	// target) nil for erasure to fill. Reject a survivor sitting on the target — a
	// claim must repair a MISSING shard, and counting the target as its own survivor
	// would let a claimant "prove" any bytes by supplying them as the survivor.
	shards := make([][]byte, p.N)
	present := 0
	for pos, b := range survivors {
		if pos < 0 || pos >= p.N {
			return false, fmt.Errorf("repairproof: survivor position %d out of range 0..%d", pos, p.N-1)
		}
		if pos == target {
			return false, fmt.Errorf("repairproof: target position %d supplied as its own survivor", target)
		}
		shards[pos] = b
		present++
	}
	if present < p.K {
		return false, ErrUnrecoverable
	}

	if err := erasure.ReconstructStripe(p, shards, realData); err != nil {
		// Below k, inconsistent survivors, or a parity mismatch: not a well-formed,
		// recoverable stripe. That is a failed challenge, not a verified repair.
		return false, nil
	}
	got := ports.HashBytes(shards[target])
	return got == want, nil
}
