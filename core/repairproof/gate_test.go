package repairproof

import (
	"testing"

	"github.com/nerolabs/silt/core/por"
	"github.com/nerolabs/silt/ports"
)

func nodeID(b byte) ports.NodeID { return ports.HashBytes([]byte{b, 'n'}) }

// porFixture builds a por key, a rebuilt shard, and its tags — the state a repairer
// holds after reconstructing and re-tagging a shard.
func porFixture(t *testing.T) (key *por.Key, unitID []byte, shard []byte, tags [][]byte, blocks int) {
	t.Helper()
	key, err := por.DeriveKey([]byte("layout-key-seed"), por.DefaultParams)
	if err != nil {
		t.Fatalf("derive key: %v", err)
	}
	shard = make([]byte, 64<<10) // a 64 KiB shard
	for i := range shard {
		shard[i] = byte((i*7 + 5) % 251)
	}
	id := ports.HashBytes(shard)
	unitID = id[:]
	tags = key.Tags(unitID, shard)
	blocks = por.DefaultParams.Blocks(len(shard))
	return key, unitID, shard, tags, blocks
}

// proofUnder builds the PoR proof a prover with identity `who` would submit for the
// given challenge round `base` over `data`.
func proofUnder(t *testing.T, who ports.NodeID, base [32]byte, data []byte, tags [][]byte, blocks, count int) por.Proof {
	t.Helper()
	seed := RepairChallengeSeed(base, who)
	c := por.Challenge{Seed: seed, Blocks: blocks, Count: count}
	p, err := por.Prove(por.DefaultParams, data, tags, c)
	if err != nil {
		t.Fatalf("prove: %v", err)
	}
	return p
}

// TestVerifyRetrievability_HonestHolderPasses: the repairer that holds the rebuilt
// shard answers its own identity-bound challenge and verifies.
func TestVerifyRetrievability_HonestHolderPasses(t *testing.T) {
	key, unitID, shard, tags, blocks := porFixture(t)
	repairer := nodeID(1)
	base := [32]byte{0xa1}
	const count = 4

	proof := proofUnder(t, repairer, base, shard, tags, blocks, count)
	if !VerifyRetrievability(key, unitID, repairer, base, blocks, count, proof) {
		t.Fatal("an honest holder must pass its own identity-bound challenge")
	}
}

// TestVerifyRetrievability_RelayedProofFails: the double-count defense — an attacker
// cannot claim a bounty by relaying a proof another identity built for an
// already-present replica; that proof was aggregated under the holder's seed and
// fails under the attacker's.
func TestVerifyRetrievability_RelayedProofFails(t *testing.T) {
	key, unitID, shard, tags, blocks := porFixture(t)
	holder, attacker := nodeID(1), nodeID(2)
	base := [32]byte{0xb2}
	const count = 4

	// The honest holder's proof, computed under the holder's seed.
	holderProof := proofUnder(t, holder, base, shard, tags, blocks, count)

	// Attacker relays it, claiming the bounty under its own identity → must fail.
	if VerifyRetrievability(key, unitID, attacker, base, blocks, count, holderProof) {
		t.Fatal("a relayed proof must fail the attacker's identity-bound challenge (double-count defense)")
	}
	// Sanity: the same relayed proof DOES verify for the holder it was built for.
	if !VerifyRetrievability(key, unitID, holder, base, blocks, count, holderProof) {
		t.Fatal("the holder's own proof should verify for the holder")
	}
}

// TestVerifyRetrievability_TamperedDataFails: a claimant that lost or altered the
// bytes cannot answer, even under its own seed.
func TestVerifyRetrievability_TamperedDataFails(t *testing.T) {
	key, unitID, shard, tags, blocks := porFixture(t)
	repairer := nodeID(1)
	base := [32]byte{0xc3}
	// SW PoR is probabilistic — a challenge only catches tampering in a SAMPLED
	// block. Sample every block (count = blocks) so a single-byte flip in block 0 is
	// deterministically challenged and caught (a smaller sample would catch it only
	// with probability ≈ count/blocks, which is the honest security model, not a
	// good unit-test assertion).
	count := blocks

	tampered := append([]byte(nil), shard...)
	tampered[0] ^= 0xff // one flipped byte, in block 0
	proof := proofUnder(t, repairer, base, tampered, tags, blocks, count)
	if VerifyRetrievability(key, unitID, repairer, base, blocks, count, proof) {
		t.Fatal("a prover that altered a sampled shard block must not verify")
	}
}

// TestVerifyRetrievability_Guards: nil key or zero blocks are a clean false, not a
// panic.
func TestVerifyRetrievability_Guards(t *testing.T) {
	if VerifyRetrievability(nil, []byte("u"), nodeID(1), [32]byte{}, 4, 2, por.Proof{}) {
		t.Fatal("nil key must not verify")
	}
	key, unitID, _, _, _ := porFixture(t)
	if VerifyRetrievability(key, unitID, nodeID(1), [32]byte{}, 0, 2, por.Proof{}) {
		t.Fatal("zero blocks must not verify")
	}
}

// TestDecide_TruthTable exercises the release/slash gate across the correctness ×
// retrievability × quorum space.
func TestDecide_TruthTable(t *testing.T) {
	cases := []struct {
		name          string
		correctnessOK bool
		votes         []bool
		tau           int
		want          Decision
	}{
		{"false correctness slashes regardless", false, []bool{true, true, true}, 2, Decision{Release: false, Slash: true}},
		{"false correctness, no retrievability", false, nil, 1, Decision{Release: false, Slash: true}},
		{"correct + quorum met releases", true, []bool{true, true, true}, 2, Decision{Release: true, Slash: false}},
		{"correct + quorum exactly met", true, []bool{true, false, true}, 2, Decision{Release: true, Slash: false}},
		{"correct + quorum short denies, no slash", true, []bool{true, false, false}, 2, Decision{Release: false, Slash: false}},
		{"correct + no votes denies", true, nil, 1, Decision{Release: false, Slash: false}},
		{"correct + tau=0 denies (no valid quorum)", true, []bool{true, true, true}, 0, Decision{Release: false, Slash: false}},
		{"correct + negative tau denies", true, []bool{true}, -1, Decision{Release: false, Slash: false}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Decide(c.correctnessOK, c.votes, c.tau); got != c.want {
				t.Fatalf("Decide(%v, %v, %d) = %+v, want %+v", c.correctnessOK, c.votes, c.tau, got, c.want)
			}
		})
	}
}
