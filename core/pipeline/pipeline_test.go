package pipeline_test

import (
	"bytes"
	"context"
	"math/rand"
	"strings"
	"testing"
	"testing/quick"

	"github.com/nerolabs/bitbit/adapters/memstore"
	"github.com/nerolabs/bitbit/core/crypto"
	"github.com/nerolabs/bitbit/core/erasure"
	"github.com/nerolabs/bitbit/core/link"
	"github.com/nerolabs/bitbit/core/manifest"
	"github.com/nerolabs/bitbit/core/pipeline"
	"github.com/nerolabs/bitbit/core/registry"
	"github.com/nerolabs/bitbit/ports"
)

const testChunkSize = 1024

func addGet(t *testing.T, data []byte, mode crypto.Mode) {
	t.Helper()
	ctx := context.Background()
	store := memstore.New()
	reg := registry.New()
	opts := pipeline.Options{ChunkSize: testChunkSize, Mode: mode, Rand: rand.New(rand.NewSource(1))}
	h, err := pipeline.Add(ctx, store, reg, bytes.NewReader(data), opts)
	if err != nil {
		t.Fatalf("Add(%d bytes, %s): %v", len(data), mode, err)
	}
	var out bytes.Buffer
	if err := pipeline.Get(ctx, store, reg, h, &out); err != nil {
		t.Fatalf("Get(%s): %v", mode, err)
	}
	if !bytes.Equal(out.Bytes(), data) {
		t.Fatalf("roundtrip mismatch (%s): %d in, %d out", mode, len(data), out.Len())
	}
}

func TestRoundtripBothModes(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	sizes := []int{0, 1, testChunkSize - 9, testChunkSize - 8, testChunkSize, 5000, 100_000}
	for _, n := range sizes {
		data := make([]byte, n)
		rng.Read(data)
		addGet(t, data, crypto.Convergent)
		addGet(t, data, crypto.Private)
	}
}

func TestRoundtripProperty(t *testing.T) {
	f := func(data []byte) bool {
		ctx := context.Background()
		store := memstore.New()
		reg := registry.New()
		h, err := pipeline.Add(ctx, store, reg, bytes.NewReader(data),
			pipeline.Options{ChunkSize: 256, Mode: crypto.Convergent})
		if err != nil {
			return false
		}
		var out bytes.Buffer
		if err := pipeline.Get(ctx, store, reg, h, &out); err != nil {
			return false
		}
		return bytes.Equal(out.Bytes(), data)
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 200}); err != nil {
		t.Fatal(err)
	}
}

func TestConvergentDedup(t *testing.T) {
	// Same file added twice ⇒ same root, and no extra chunks stored.
	ctx := context.Background()
	store := memstore.New()
	reg := registry.New()
	data := make([]byte, 10_000)
	rand.New(rand.NewSource(3)).Read(data)
	opts := pipeline.Options{ChunkSize: testChunkSize, Mode: crypto.Convergent}

	h1, err := pipeline.Add(ctx, store, reg, bytes.NewReader(data), opts)
	if err != nil {
		t.Fatal(err)
	}
	ids1, _ := store.List(ctx)
	h2, err := pipeline.Add(ctx, store, reg, bytes.NewReader(data), opts)
	if err != nil {
		t.Fatal(err)
	}
	ids2, _ := store.List(ctx)

	if h1 != h2 {
		t.Fatal("convergent mode: same content must yield same root AND same link")
	}
	if len(ids1) != len(ids2) {
		t.Fatalf("second add stored %d new chunks; dedup should store none", len(ids2)-len(ids1))
	}
}

func TestPrivateModeDistinctRoots(t *testing.T) {
	ctx := context.Background()
	store := memstore.New()
	reg := registry.New()
	data := []byte("identical content, added privately twice")
	h1, err := pipeline.Add(ctx, store, reg, bytes.NewReader(data),
		pipeline.Options{ChunkSize: testChunkSize, Mode: crypto.Private, Rand: rand.New(rand.NewSource(1))})
	if err != nil {
		t.Fatal(err)
	}
	h2, err := pipeline.Add(ctx, store, reg, bytes.NewReader(data),
		pipeline.Options{ChunkSize: testChunkSize, Mode: crypto.Private, Rand: rand.New(rand.NewSource(2))})
	if err != nil {
		t.Fatal(err)
	}
	if h1.Root == h2.Root {
		t.Fatal("private mode: different file keys must yield different roots")
	}
}

// loadManifest fetches and parses the stored manifest for root, so tests
// can aim their destruction at specific stripes.
func loadManifest(t *testing.T, ctx context.Context, store ports.ChunkStore, reg ports.Registry, h link.Handle) *manifest.Manifest {
	t.Helper()
	entry, ok, err := reg.Lookup(ctx, h.Root)
	if err != nil || !ok {
		t.Fatalf("lookup: ok=%v err=%v", ok, err)
	}
	m, err := pipeline.LoadFull(ctx, store, entry, h)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// The M2 money test: delete ANY n-k shards from every stripe and the
// file still comes back bit-perfect; delete one more and it fails with
// a clear error.
func TestSurvivesAnyNMinusKLosses(t *testing.T) {
	ctx := context.Background()
	params := erasure.Params{K: 4, N: 7}
	data := make([]byte, 37_000) // 37 chunks ⇒ 10 stripes, last one short
	rand.New(rand.NewSource(4)).Read(data)

	for trial := 0; trial < 20; trial++ {
		rng := rand.New(rand.NewSource(int64(100 + trial)))
		store := memstore.New()
		reg := registry.New()
		root, err := pipeline.Add(ctx, store, reg, bytes.NewReader(data),
			pipeline.Options{ChunkSize: testChunkSize, Mode: crypto.Convergent, Erasure: params})
		if err != nil {
			t.Fatal(err)
		}
		m := loadManifest(t, ctx, store, reg, root)
		dataIDs, parityIDs := m.ChunkIDs(), m.ParityIDs()

		// In every stripe, destroy a random n-k of the stored shards.
		// (A short final stripe stores fewer than n shards; implicit
		// zero shards can't be destroyed, they're mathematical.)
		for j := 0; j < params.Stripes(len(dataIDs)); j++ {
			lo := j * params.K
			hi := min(lo+params.K, len(dataIDs))
			var stored []ports.ChunkID
			stored = append(stored, dataIDs[lo:hi]...)
			stored = append(stored, parityIDs[j*params.ParityShards():(j+1)*params.ParityShards()]...)
			for _, idx := range rng.Perm(len(stored))[:params.N-params.K] {
				if err := store.Delete(ctx, stored[idx]); err != nil {
					t.Fatal(err)
				}
			}
		}
		var out bytes.Buffer
		if err := pipeline.Get(ctx, store, reg, root, &out); err != nil {
			t.Fatalf("trial %d: Get after n-k losses per stripe: %v", trial, err)
		}
		if !bytes.Equal(out.Bytes(), data) {
			t.Fatalf("trial %d: reconstructed file differs", trial)
		}
	}
}

func TestOneLossBeyondToleranceFailsLoudly(t *testing.T) {
	ctx := context.Background()
	params := erasure.Params{K: 4, N: 6}
	store := memstore.New()
	reg := registry.New()
	data := make([]byte, 20_000)
	rand.New(rand.NewSource(5)).Read(data)
	root, err := pipeline.Add(ctx, store, reg, bytes.NewReader(data),
		pipeline.Options{ChunkSize: testChunkSize, Mode: crypto.Convergent, Erasure: params})
	if err != nil {
		t.Fatal(err)
	}
	m := loadManifest(t, ctx, store, reg, root)
	// Kill n-k+1 shards of stripe 0 (1 data + both parity): unrecoverable.
	victims := []ports.ChunkID{m.ChunkIDs()[0], m.ParityIDs()[0], m.ParityIDs()[1]}
	if len(victims) != params.N-params.K+1 {
		t.Fatalf("test setup: %d victims", len(victims))
	}
	for _, id := range victims {
		store.Delete(ctx, id)
	}
	err = pipeline.Get(ctx, store, reg, root, &bytes.Buffer{})
	if err == nil {
		t.Fatal("Get must fail once losses exceed n-k in a stripe")
	}
	if !strings.Contains(err.Error(), "stripe 0") || !strings.Contains(err.Error(), "unrecoverable") {
		t.Fatalf("error should name the stripe and say unrecoverable, got: %v", err)
	}
}

// Corruption is just loss with extra steps: a shard that fails its hash
// check gets rebuilt from parity like a missing one.
func TestCorruptionIsRepairedFromParity(t *testing.T) {
	ctx := context.Background()
	store := memstore.New()
	reg := registry.New()
	data := make([]byte, 20_000)
	rand.New(rand.NewSource(6)).Read(data)
	root, err := pipeline.Add(ctx, store, reg, bytes.NewReader(data),
		pipeline.Options{ChunkSize: testChunkSize, Mode: crypto.Convergent, Erasure: erasure.Params{K: 4, N: 6}})
	if err != nil {
		t.Fatal(err)
	}
	m := loadManifest(t, ctx, store, reg, root)
	if !store.Corrupt(m.ChunkIDs()[0]) {
		t.Fatal("test setup: corrupt failed")
	}
	var out bytes.Buffer
	if err := pipeline.Get(ctx, store, reg, root, &out); err != nil {
		t.Fatalf("Get with one corrupt chunk should recover via parity: %v", err)
	}
	if !bytes.Equal(out.Bytes(), data) {
		t.Fatal("recovered file differs")
	}
}

func TestUnknownRoot(t *testing.T) {
	var h link.Handle
	h.Root[0] = 0xAB
	err := pipeline.Get(context.Background(), memstore.New(), registry.New(), h, &bytes.Buffer{})
	if err == nil {
		t.Fatal("Get of unpublished root must fail")
	}
}

func BenchmarkAddGet8MiB(b *testing.B) {
	ctx := context.Background()
	data := make([]byte, 8<<20)
	rand.New(rand.NewSource(1)).Read(data)
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		store := memstore.New()
		reg := registry.New()
		root, err := pipeline.Add(ctx, store, reg, bytes.NewReader(data),
			pipeline.Options{ChunkSize: 64 << 10, Mode: crypto.Convergent})
		if err != nil {
			b.Fatal(err)
		}
		var out bytes.Buffer
		out.Grow(len(data))
		if err := pipeline.Get(ctx, store, reg, root, &out); err != nil {
			b.Fatal(err)
		}
	}
}
