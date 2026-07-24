package erasure

import (
	"bytes"
	"math/rand"
	"testing"
)

func makeStripe(rng *rand.Rand, count, size int) [][]byte {
	data := make([][]byte, count)
	for i := range data {
		data[i] = make([]byte, size)
		rng.Read(data[i])
	}
	return data
}

// The M2 invariant: for random data and ANY loss pattern of up to n-k
// shards, reconstruction returns the original bytes exactly.
func TestReconstructUnderRandomLossProperty(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	p := Params{K: 4, N: 7}
	const shardSize = 256

	for trial := 0; trial < 200; trial++ {
		realData := 1 + rng.Intn(p.K) // includes short final stripes
		data := makeStripe(rng, realData, shardSize)
		orig := make([][]byte, realData)
		for i := range data {
			orig[i] = append([]byte(nil), data[i]...)
		}
		parity, err := EncodeStripe(p, data)
		if err != nil {
			t.Fatalf("trial %d: %v", trial, err)
		}

		// Assemble the full stripe, then lose up to n-k random shards.
		shards := make([][]byte, p.N)
		for i := 0; i < realData; i++ {
			shards[i] = append([]byte(nil), data[i]...)
		}
		for i, par := range parity {
			shards[p.K+i] = append([]byte(nil), par...)
		}
		losses := rng.Intn(p.N - p.K + 1)
		for _, idx := range rng.Perm(p.N)[:losses] {
			shards[idx] = nil
		}

		if err := ReconstructStripe(p, shards, realData); err != nil {
			t.Fatalf("trial %d (lost %d): %v", trial, losses, err)
		}
		for i := 0; i < realData; i++ {
			if !bytes.Equal(shards[i], orig[i]) {
				t.Fatalf("trial %d: data shard %d differs after reconstruction", trial, i)
			}
		}
	}
}

func TestBelowKFailsLoudly(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	p := Params{K: 4, N: 6}
	data := makeStripe(rng, p.K, 128)
	parity, err := EncodeStripe(p, data)
	if err != nil {
		t.Fatal(err)
	}
	shards := make([][]byte, p.N)
	copy(shards, data)
	copy(shards[p.K:], parity)
	// Lose n-k+1 shards: one more than the code tolerates.
	for _, idx := range []int{0, 2, 5} {
		shards[idx] = nil
	}
	err = ReconstructStripe(p, shards, p.K)
	if err == nil {
		t.Fatal("reconstruction below k shards must fail")
	}
	if !bytes.Contains([]byte(err.Error()), []byte("unrecoverable")) {
		t.Fatalf("error should say the data is unrecoverable, got: %v", err)
	}
}

func TestImplicitZeroShardsCountAsAvailable(t *testing.T) {
	// Final stripe: 1 real chunk of k=4. Lose the real chunk AND one
	// parity — the three zero shards plus remaining parity must still
	// reach k available.
	rng := rand.New(rand.NewSource(2))
	p := Params{K: 4, N: 6}
	data := makeStripe(rng, 1, 128)
	orig := append([]byte(nil), data[0]...)
	parity, err := EncodeStripe(p, data)
	if err != nil {
		t.Fatal(err)
	}
	shards := make([][]byte, p.N)
	shards[p.K] = parity[0] // only one parity shard survives...
	// ...data shard 0 lost, positions 1..3 implicit zeros, parity 1 lost.
	if err := ReconstructStripe(p, shards, 1); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(shards[0], orig) {
		t.Fatal("reconstructed shard differs")
	}
}

func TestParamsValidate(t *testing.T) {
	for _, p := range []Params{{0, 5}, {5, 5}, {6, 5}, {200, 300}} {
		if err := p.Validate(); err == nil {
			t.Errorf("Params%+v should be invalid", p)
		}
	}
	for _, p := range []Params{{1, 2}, {10, 16}, {1, 256}} {
		if err := p.Validate(); err != nil {
			t.Errorf("Params%+v should be valid: %v", p, err)
		}
	}
}

func TestEncodeRejectsUnequalShards(t *testing.T) {
	p := Params{K: 2, N: 4}
	_, err := EncodeStripe(p, [][]byte{make([]byte, 10), make([]byte, 11)})
	if err == nil {
		t.Fatal("unequal shard sizes must be rejected")
	}
}

func BenchmarkEncodeStripe10of16_64KiB(b *testing.B) {
	rng := rand.New(rand.NewSource(1))
	p := DefaultParams
	data := makeStripe(rng, p.K, 64<<10)
	b.SetBytes(int64(p.K * 64 << 10))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := EncodeStripe(p, data); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkReconstructTwoLost(b *testing.B) {
	rng := rand.New(rand.NewSource(1))
	p := DefaultParams
	data := makeStripe(rng, p.K, 64<<10)
	parity, _ := EncodeStripe(p, data)
	b.SetBytes(int64(p.K * 64 << 10))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		shards := make([][]byte, p.N)
		for j, d := range data {
			shards[j] = append([]byte(nil), d...)
		}
		for j, par := range parity {
			shards[p.K+j] = append([]byte(nil), par...)
		}
		shards[3], shards[7] = nil, nil
		if err := ReconstructStripe(p, shards, p.K); err != nil {
			b.Fatal(err)
		}
	}
}
