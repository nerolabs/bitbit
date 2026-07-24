package pipeline_test

import (
	"bytes"
	"context"
	"math/rand"
	"testing"
	"testing/quick"

	"shardnet/adapters/memstore"
	"shardnet/core/crypto"
	"shardnet/core/pipeline"
	"shardnet/core/registry"
	"shardnet/ports"
)

const testChunkSize = 1024

func addGet(t *testing.T, data []byte, mode crypto.Mode) {
	t.Helper()
	ctx := context.Background()
	store := memstore.New()
	reg := registry.New()
	opts := pipeline.Options{ChunkSize: testChunkSize, Mode: mode, Rand: rand.New(rand.NewSource(1))}
	root, err := pipeline.Add(ctx, store, reg, bytes.NewReader(data), opts)
	if err != nil {
		t.Fatalf("Add(%d bytes, %s): %v", len(data), mode, err)
	}
	var out bytes.Buffer
	if err := pipeline.Get(ctx, store, reg, root, &out); err != nil {
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
		root, err := pipeline.Add(ctx, store, reg, bytes.NewReader(data),
			pipeline.Options{ChunkSize: 256, Mode: crypto.Convergent})
		if err != nil {
			return false
		}
		var out bytes.Buffer
		if err := pipeline.Get(ctx, store, reg, root, &out); err != nil {
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

	root1, err := pipeline.Add(ctx, store, reg, bytes.NewReader(data), opts)
	if err != nil {
		t.Fatal(err)
	}
	ids1, _ := store.List(ctx)
	root2, err := pipeline.Add(ctx, store, reg, bytes.NewReader(data), opts)
	if err != nil {
		t.Fatal(err)
	}
	ids2, _ := store.List(ctx)

	if root1 != root2 {
		t.Fatal("convergent mode: same content must yield same root")
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
	root1, err := pipeline.Add(ctx, store, reg, bytes.NewReader(data),
		pipeline.Options{ChunkSize: testChunkSize, Mode: crypto.Private, Rand: rand.New(rand.NewSource(1))})
	if err != nil {
		t.Fatal(err)
	}
	root2, err := pipeline.Add(ctx, store, reg, bytes.NewReader(data),
		pipeline.Options{ChunkSize: testChunkSize, Mode: crypto.Private, Rand: rand.New(rand.NewSource(2))})
	if err != nil {
		t.Fatal(err)
	}
	if root1 == root2 {
		t.Fatal("private mode: different file keys must yield different roots")
	}
}

func TestCorruptChunkIsCaught(t *testing.T) {
	ctx := context.Background()
	store := memstore.New()
	reg := registry.New()
	data := make([]byte, 20_000)
	rand.New(rand.NewSource(4)).Read(data)
	root, err := pipeline.Add(ctx, store, reg, bytes.NewReader(data),
		pipeline.Options{ChunkSize: testChunkSize, Mode: crypto.Convergent})
	if err != nil {
		t.Fatal(err)
	}
	entry, _, _ := reg.Lookup(ctx, root)
	// Corrupt one data chunk (any chunk that isn't a manifest chunk).
	manifestIDs := map[ports.ChunkID]bool{}
	for _, id := range entry.ManifestChunks {
		manifestIDs[id] = true
	}
	ids, _ := store.List(ctx)
	corrupted := false
	for _, id := range ids {
		if !manifestIDs[id] {
			store.Corrupt(id)
			corrupted = true
			break
		}
	}
	if !corrupted {
		t.Fatal("test setup: found no data chunk to corrupt")
	}
	if err := pipeline.Get(ctx, store, reg, root, &bytes.Buffer{}); err == nil {
		t.Fatal("Get must fail loudly on a corrupted chunk")
	}
}

func TestMissingChunkIsCaught(t *testing.T) {
	ctx := context.Background()
	store := memstore.New()
	reg := registry.New()
	data := make([]byte, 20_000)
	rand.New(rand.NewSource(5)).Read(data)
	root, _ := pipeline.Add(ctx, store, reg, bytes.NewReader(data),
		pipeline.Options{ChunkSize: testChunkSize, Mode: crypto.Convergent})
	ids, _ := store.List(ctx)
	if err := store.Delete(ctx, ids[0]); err != nil {
		t.Fatal(err)
	}
	if err := pipeline.Get(ctx, store, reg, root, &bytes.Buffer{}); err == nil {
		t.Fatal("Get must fail when a chunk is missing (no erasure coding until M2)")
	}
}

func TestUnknownRoot(t *testing.T) {
	var root ports.Hash
	root[0] = 0xAB
	err := pipeline.Get(context.Background(), memstore.New(), registry.New(), root, &bytes.Buffer{})
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
