package repairproof

import (
	"testing"

	"github.com/nerolabs/silt/core/erasure"
	"github.com/nerolabs/silt/ports"
)

// makeStripe builds a full, honestly-encoded stripe of `size`-byte shards and
// returns every shard's bytes (n positions: data 0..k-1, parity k..n-1) and its
// content-addressed ID.
func makeStripe(t *testing.T, p erasure.Params, size int) (shards [][]byte, ids []ports.ChunkID) {
	t.Helper()
	data := make([][]byte, p.K)
	for i := range data {
		d := make([]byte, size)
		for j := range d {
			d[j] = byte((i*31 + j*7 + 1) % 251) // deterministic, no RNG
		}
		data[i] = d
	}
	parity, err := erasure.EncodeStripe(p, data)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	shards = make([][]byte, p.N)
	copy(shards, data)
	copy(shards[p.K:], parity)
	ids = make([]ports.ChunkID, p.N)
	for i, s := range shards {
		ids[i] = ports.HashBytes(s)
	}
	return shards, ids
}

// survivorsExcluding picks the first `count` stripe positions that are not
// `target`, returning their bytes keyed by position.
func survivorsExcluding(shards [][]byte, target, count int) map[int][]byte {
	out := make(map[int][]byte, count)
	for pos := 0; pos < len(shards) && len(out) < count; pos++ {
		if pos == target {
			continue
		}
		out[pos] = shards[pos]
	}
	return out
}

// TestVerifyByRecompute_HonestRepairVerifies: for both a data shard and a parity
// shard, reconstructing the "lost" position from exactly k survivors matches the
// manifest-committed ID.
func TestVerifyByRecompute_HonestRepairVerifies(t *testing.T) {
	p := erasure.Params{K: 4, N: 8}
	shards, ids := makeStripe(t, p, 64)

	for _, target := range []int{1 /* a data shard */, p.K + 1 /* a parity shard */} {
		surv := survivorsExcluding(shards, target, p.K)
		ok, err := VerifyByRecompute(p, surv, p.K, target, ids[target])
		if err != nil {
			t.Fatalf("target %d: unexpected error %v", target, err)
		}
		if !ok {
			t.Fatalf("target %d: honest repair failed to verify", target)
		}
	}
}

// TestVerifyByRecompute_ExactlyKSurvivorsSuffice and a full n-1 survivor set both
// verify — the recompute is over ANY k of the survivors.
func TestVerifyByRecompute_SurvivorSetSizes(t *testing.T) {
	p := erasure.Params{K: 4, N: 8}
	shards, ids := makeStripe(t, p, 96)
	target := 2

	for _, count := range []int{p.K, p.N - 1} {
		surv := survivorsExcluding(shards, target, count)
		ok, err := VerifyByRecompute(p, surv, p.K, target, ids[target])
		if err != nil || !ok {
			t.Fatalf("survivor count %d: ok=%v err=%v, want ok=true", count, ok, err)
		}
	}
}

// TestVerifyByRecompute_WrongClaimRejected: a claim whose committed target ID is
// some OTHER shard's ID (a repairer trying to pass off the wrong bytes) fails.
func TestVerifyByRecompute_WrongClaimRejected(t *testing.T) {
	p := erasure.Params{K: 4, N: 8}
	shards, ids := makeStripe(t, p, 64)
	target := 1
	surv := survivorsExcluding(shards, target, p.K)

	// want = a different shard's ID → the recomputed target won't match it.
	ok, err := VerifyByRecompute(p, surv, p.K, target, ids[target+1])
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("a claim against the wrong committed ID must be rejected")
	}

	// want = a garbage ID → also rejected.
	garbage := ports.HashBytes([]byte("not a real shard"))
	ok, _ = VerifyByRecompute(p, surv, p.K, target, garbage)
	if ok {
		t.Fatal("a claim against a garbage ID must be rejected")
	}
}

// TestVerifyByRecompute_CorruptedSurvivorRejected: one survivor with a flipped
// byte reconstructs a different target, so the honest committed ID no longer
// matches — the false repair is caught, not silently accepted.
func TestVerifyByRecompute_CorruptedSurvivorRejected(t *testing.T) {
	p := erasure.Params{K: 4, N: 8}
	shards, ids := makeStripe(t, p, 64)
	target := 3
	surv := survivorsExcluding(shards, target, p.K)

	// Flip a byte in one survivor (copy first so we don't mutate the shared slice).
	for pos, b := range surv {
		c := append([]byte(nil), b...)
		c[0] ^= 0xff
		surv[pos] = c
		break
	}
	ok, err := VerifyByRecompute(p, surv, p.K, target, ids[target])
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("a corrupted survivor set must not verify to the honest target ID")
	}
}

// TestVerifyByRecompute_TooFewSurvivors: below k survivors, the target cannot be
// reconstructed at all, so the claim is unverifiable — never accepted by default.
func TestVerifyByRecompute_TooFewSurvivors(t *testing.T) {
	p := erasure.Params{K: 4, N: 8}
	shards, ids := makeStripe(t, p, 64)
	target := 0
	surv := survivorsExcluding(shards, target, p.K-1) // one short

	ok, err := VerifyByRecompute(p, surv, p.K, target, ids[target])
	if err != ErrUnrecoverable {
		t.Fatalf("err = %v, want ErrUnrecoverable", err)
	}
	if ok {
		t.Fatal("an unrecoverable stripe must not verify")
	}
}

// TestVerifyByRecompute_TargetAsOwnSurvivor: supplying the target position as its
// own survivor is rejected — else a claimant could "prove" any bytes by handing
// them in as the survivor.
func TestVerifyByRecompute_TargetAsOwnSurvivor(t *testing.T) {
	p := erasure.Params{K: 4, N: 8}
	shards, ids := makeStripe(t, p, 64)
	target := 2
	surv := survivorsExcluding(shards, target, p.K)
	surv[target] = shards[target] // sneak the target in

	_, err := VerifyByRecompute(p, surv, p.K, target, ids[target])
	if err == nil {
		t.Fatal("target supplied as its own survivor must be a structural error")
	}
}

// TestVerifyByRecompute_MalformedInputs: out-of-range target, bad realData, and a
// claim to have repaired an implicit-zero padding position are all structural
// errors, distinct from a well-formed claim that fails to verify.
func TestVerifyByRecompute_MalformedInputs(t *testing.T) {
	p := erasure.Params{K: 4, N: 8}
	shards, ids := makeStripe(t, p, 64)
	surv := survivorsExcluding(shards, 0, p.K)

	// target out of range.
	if _, err := VerifyByRecompute(p, surv, p.K, p.N, ids[0]); err == nil {
		t.Fatal("out-of-range target must error")
	}
	// bad realData.
	if _, err := VerifyByRecompute(p, surv, 0, 0, ids[0]); err == nil {
		t.Fatal("realData=0 must error")
	}
	// A padding position (realData..k-1) is not a repairable shard.
	if _, err := VerifyByRecompute(p, surv, 3 /* realData */, 3 /* target = pad */, ids[3]); err == nil {
		t.Fatal("claiming to repair an implicit-zero padding position must error")
	}
	// bad code params.
	if _, err := VerifyByRecompute(erasure.Params{K: 0, N: 8}, surv, 1, 0, ids[0]); err == nil {
		t.Fatal("invalid params must error")
	}
}

// TestVerifyByRecompute_ShortFinalStripe: a stripe with fewer than k real data
// chunks (the padded final stripe) still verifies an honest repair of a REAL data
// shard, with the implicit zeros supplied for free.
func TestVerifyByRecompute_ShortFinalStripe(t *testing.T) {
	p := erasure.Params{K: 4, N: 8}
	const realData = 2
	size := 48

	// Encode a short stripe: only realData real data shards.
	data := make([][]byte, realData)
	for i := range data {
		d := make([]byte, size)
		for j := range d {
			d[j] = byte((i*17 + j*5 + 3) % 251)
		}
		data[i] = d
	}
	parity, err := erasure.EncodeStripe(p, data)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	// Full n-slot picture: realData real shards, (k-realData) implicit zeros, parity.
	shards := make([][]byte, p.N)
	copy(shards, data)
	for i := realData; i < p.K; i++ {
		shards[i] = make([]byte, size) // implicit zero shard
	}
	copy(shards[p.K:], parity)
	ids := make([]ports.ChunkID, p.N)
	for i, s := range shards {
		ids[i] = ports.HashBytes(s)
	}

	// Repair real data shard 1 from k survivors (the zeros count for free inside
	// erasure, so supply the real+parity shards).
	target := 1
	surv := map[int][]byte{}
	for pos := 0; pos < p.N && len(surv) < p.K; pos++ {
		if pos == target || (pos >= realData && pos < p.K) {
			continue // skip target and padding positions
		}
		surv[pos] = shards[pos]
	}
	ok, err := VerifyByRecompute(p, surv, realData, target, ids[target])
	if err != nil || !ok {
		t.Fatalf("short-stripe honest repair: ok=%v err=%v, want ok=true", ok, err)
	}
}
