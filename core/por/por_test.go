package por

import (
	"crypto/rand"
	"math/big"
	mrand "math/rand"
	"testing"
)

// small params keep the field arithmetic cheap; a couple of tests also
// exercise DefaultParams on a realistic chunk.
var testParams = Params{SectorsPerBlock: 8}

func makeUnit(t *testing.T, params Params, blocks int) (key *Key, unitID, data []byte, tags [][]byte) {
	t.Helper()
	key, err := Keygen(rand.Reader, params)
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	data = make([]byte, blocks*params.blockBytes())
	if _, err := rand.Read(data); err != nil {
		t.Fatalf("rand data: %v", err)
	}
	unitID = []byte("chunk-under-test")
	tags = key.Tags(unitID, data)
	if len(tags) != blocks {
		t.Fatalf("expected %d tags, got %d", blocks, len(tags))
	}
	return key, unitID, data, tags
}

// challengeAll samples every block, so any single altered/lost block is
// deterministically in the sample — the tightest adversarial check.
func challengeAll(t *testing.T, key *Key, unitID, data []byte) Challenge {
	t.Helper()
	nb := key.params.Blocks(len(data))
	c, err := NewChallenge(rand.Reader, nb, nb)
	if err != nil {
		t.Fatalf("challenge: %v", err)
	}
	return c
}

// The promise: an honest holder of the bytes + tags always passes, and the
// verifier never touches the data.
func TestHonestProverPasses(t *testing.T) {
	key, unitID, data, tags := makeUnit(t, testParams, 6)
	c := challengeAll(t, key, unitID, data)
	proof, err := Prove(key.params, data, tags, c)
	if err != nil {
		t.Fatalf("prove: %v", err)
	}
	if !key.Verify(unitID, c, proof) {
		t.Fatal("honest prover failed verification")
	}
}

// Retrievability: a prover that ALTERED one byte of a sampled block cannot
// pass — the whole point of the scheme (a non-holder is caught).
func TestTamperedBlockFails(t *testing.T) {
	key, unitID, data, tags := makeUnit(t, testParams, 6)
	c := challengeAll(t, key, unitID, data)
	// flip a bit inside block 3.
	bad := append([]byte(nil), data...)
	off := 3 * key.params.blockBytes()
	bad[off] ^= 0x01
	proof, err := Prove(key.params, bad, tags, c)
	if err != nil {
		t.Fatalf("prove: %v", err)
	}
	if key.Verify(unitID, c, proof) {
		t.Fatal("a prover that altered a sampled block passed verification")
	}
}

// A prover that LOST a block (models silt's liar: kept the tags, dropped the
// bytes) cannot answer for it.
func TestDeletedBlockFails(t *testing.T) {
	key, unitID, data, tags := makeUnit(t, testParams, 5)
	c := challengeAll(t, key, unitID, data)
	// zero out block 1's bytes — the tag is retained, the data is gone.
	lost := append([]byte(nil), data...)
	bb := key.params.blockBytes()
	for i := bb; i < 2*bb; i++ {
		lost[i] = 0
	}
	proof, err := Prove(key.params, lost, tags, c)
	if err != nil {
		t.Fatalf("prove: %v", err)
	}
	if key.Verify(unitID, c, proof) {
		t.Fatal("a prover that lost a sampled block's bytes passed verification")
	}
}

// The forgery a key-less adversary would attempt: keep the honest σ (from the
// stored tags) but submit μ computed from corrupted data. Without the secret
// αⱼ it cannot pick a compensating μ, so the equation fails.
func TestForgeryWithoutKeyFails(t *testing.T) {
	key, unitID, data, tags := makeUnit(t, testParams, 6)
	c := challengeAll(t, key, unitID, data)

	honest, _ := Prove(key.params, data, tags, c)
	// Adversary corrupts the data it holds, then reuses the honest σ.
	bad := append([]byte(nil), data...)
	bad[0] ^= 0xff
	forged, _ := Prove(key.params, bad, tags, c)
	forged.Sigma = honest.Sigma // splice the correct aggregate tag

	if key.Verify(unitID, c, forged) {
		t.Fatal("key-less forgery (honest σ + corrupted μ) passed verification")
	}
}

// Domain separation: tags made for chunk A must not verify as chunk B, so a
// prover can't answer a challenge on one chunk with another's stored tags.
func TestWrongUnitIDFails(t *testing.T) {
	key, unitID, data, tags := makeUnit(t, testParams, 4)
	c := challengeAll(t, key, unitID, data)
	proof, _ := Prove(key.params, data, tags, c)
	if key.Verify([]byte("a-different-chunk"), c, proof) {
		t.Fatal("proof verified under the wrong unitID")
	}
}

// A different key must reject an honest proof — standing is bound to the
// auditor's secret, not public.
func TestWrongKeyFails(t *testing.T) {
	key, unitID, data, tags := makeUnit(t, testParams, 4)
	c := challengeAll(t, key, unitID, data)
	proof, _ := Prove(key.params, data, tags, c)

	other, err := Keygen(rand.Reader, testParams)
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	if other.Verify(unitID, c, proof) {
		t.Fatal("a proof verified under an unrelated key")
	}
}

// The challenge is compact because both sides expand the same seed to the
// same (index, coefficient) set. If they diverged, honest proofs would fail.
func TestChallengeExpansionDeterministic(t *testing.T) {
	c := Challenge{Blocks: 50, Count: 12}
	if _, err := rand.Read(c.Seed[:]); err != nil {
		t.Fatal(err)
	}
	a, b := c.expand(), c.expand()
	if len(a) != c.Count || len(b) != c.Count {
		t.Fatalf("expected %d queries, got %d/%d", c.Count, len(a), len(b))
	}
	seen := map[int]bool{}
	for i := range a {
		if a[i].i != b[i].i || a[i].nu.Cmp(b[i].nu) != 0 {
			t.Fatal("challenge expansion is not deterministic")
		}
		if seen[a[i].i] {
			t.Fatalf("sampled block %d twice; expansion must be without replacement", a[i].i)
		}
		seen[a[i].i] = true
	}
}

// A proof carrying a non-canonical field element (≥ p) is rejected rather
// than silently reduced — reduction would admit a second encoding of a value.
func TestNonCanonicalElementRejected(t *testing.T) {
	key, unitID, data, tags := makeUnit(t, testParams, 3)
	c := challengeAll(t, key, unitID, data)
	proof, _ := Prove(key.params, data, tags, c)
	// set μ₀ = p (out of range) and σ likewise.
	proof.Mu[0] = encodeElem(prime)
	if key.Verify(unitID, c, proof) {
		t.Fatal("a proof with a non-canonical μ was accepted")
	}
	proof2, _ := Prove(key.params, data, tags, c)
	proof2.Sigma = encodeElem(new(big.Int).Add(prime, big.NewInt(5)))
	if key.Verify(unitID, c, proof2) {
		t.Fatal("a proof with a non-canonical σ was accepted")
	}
}

// Partial sampling must still catch a corrupted block WHENEVER that block is
// in the sample, and pass when it isn't (the prover answers honestly on the
// blocks it kept). Over many random challenges the detection rate should track
// the sampled fraction — here we assert the two structural facts, not a rate.
func TestPartialSamplingDetectsWhenSampled(t *testing.T) {
	key, unitID, data, tags := makeUnit(t, testParams, 20)
	bad := append([]byte(nil), data...)
	badBlock := 7
	bad[badBlock*key.params.blockBytes()] ^= 0x80

	rng := mrand.New(mrand.NewSource(1))
	sampledAndCaught, notSampledAndPassed := 0, 0
	for trial := 0; trial < 40; trial++ {
		var c Challenge
		rng.Read(c.Seed[:])
		c.Blocks, c.Count = 20, 5
		inSample := false
		for _, q := range c.expand() {
			if q.i == badBlock {
				inSample = true
			}
		}
		proof, _ := Prove(key.params, bad, tags, c)
		ok := key.Verify(unitID, c, proof)
		switch {
		case inSample && !ok:
			sampledAndCaught++
		case !inSample && ok:
			notSampledAndPassed++
		case inSample && ok:
			t.Fatal("corrupted block was sampled but verification passed")
		case !inSample && !ok:
			t.Fatal("verification failed on a challenge that avoided the corrupted block")
		}
	}
	if sampledAndCaught == 0 {
		t.Fatal("no trial ever sampled the corrupted block; test is not exercising detection")
	}
	t.Logf("caught-when-sampled=%d passed-when-not-sampled=%d", sampledAndCaught, notSampledAndPassed)
}

// Realistic layout: DefaultParams over a 64 KiB chunk with a ragged final
// block (not a whole multiple of the block size) round-trips.
func TestDefaultParamsRealisticChunk(t *testing.T) {
	key, err := Keygen(rand.Reader, DefaultParams)
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	data := make([]byte, 64<<10+123) // deliberately not block-aligned
	rand.Read(data)
	unitID := []byte("root:abcd/leaf:0007")
	tags := key.Tags(unitID, data)
	if got, want := len(tags), DefaultParams.Blocks(len(data)); got != want {
		t.Fatalf("tags=%d want %d", got, want)
	}
	c, err := NewChallenge(rand.Reader, len(tags), 30)
	if err != nil {
		t.Fatalf("challenge: %v", err)
	}
	proof, err := Prove(DefaultParams, data, tags, c)
	if err != nil {
		t.Fatalf("prove: %v", err)
	}
	if len(proof.Mu) != DefaultParams.SectorsPerBlock {
		t.Fatalf("proof carries %d μ, want %d", len(proof.Mu), DefaultParams.SectorsPerBlock)
	}
	if !key.Verify(unitID, c, proof) {
		t.Fatal("honest DefaultParams proof failed to verify")
	}
	// and tamper the ragged tail block.
	bad := append([]byte(nil), data...)
	bad[len(bad)-1] ^= 0x01
	full, _ := NewChallenge(rand.Reader, len(tags), len(tags))
	badProof, _ := Prove(DefaultParams, bad, tags, full)
	if key.Verify(unitID, full, badProof) {
		t.Fatal("tampered tail block passed verification")
	}
}

// Sanity: encodeElem/verify agree on fixed width and a padded final block
// hashes identically for prover and verifier (both treat missing bytes as 0).
func TestPaddingAgrees(t *testing.T) {
	key, unitID, data, tags := makeUnit(t, testParams, 1)
	// truncate the unit to a ragged length; re-tag so tags match the data.
	data = data[:len(data)-5]
	tags = key.Tags(unitID, data)
	c := challengeAll(t, key, unitID, data)
	proof, _ := Prove(key.params, data, tags, c)
	if !key.Verify(unitID, c, proof) {
		t.Fatal("ragged (zero-padded) final block did not round-trip")
	}
}
