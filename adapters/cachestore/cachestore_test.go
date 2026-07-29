package cachestore_test

import (
	"context"
	"testing"

	"github.com/nerolabs/silt/adapters/cachestore"
	"github.com/nerolabs/silt/adapters/memstore"
	"github.com/nerolabs/silt/adapters/storetest"
	"github.com/nerolabs/silt/ports"
)

// A cache is still a ChunkStore: it must honor the whole contract.
func TestConformance(t *testing.T) {
	storetest.Run(t, func(t *testing.T) ports.ChunkStore {
		return cachestore.Open(memstore.New(), 1<<20)
	})
}

func TestHitMissAndReadThrough(t *testing.T) {
	ctx := context.Background()
	inner := memstore.New()
	c := ports.NewChunk([]byte("hot chunk"))
	if err := inner.Put(ctx, c); err != nil {
		t.Fatal(err)
	}
	cache := cachestore.Open(inner, 1<<20)

	got, err := cache.Get(ctx, c.ID) // miss: fills the cache
	if err != nil || string(got.Data) != "hot chunk" {
		t.Fatalf("first get: %v / %q", err, got.Data)
	}
	if _, err := cache.Get(ctx, c.ID); err != nil { // hit
		t.Fatal(err)
	}
	if h, m, _ := cache.Stats(); h != 1 || m != 1 {
		t.Fatalf("hits=%d misses=%d, want 1/1", h, m)
	}
}

func TestPutDoesNotWarmCache(t *testing.T) {
	ctx := context.Background()
	cache := cachestore.Open(memstore.New(), 1<<20)
	if err := cache.Put(ctx, ports.NewChunk([]byte("stored, unread"))); err != nil {
		t.Fatal(err)
	}
	if _, _, used := cache.Stats(); used != 0 {
		t.Fatalf("cache-on-read only: a Put must not warm the cache, used=%d", used)
	}
}

func TestLRUEviction(t *testing.T) {
	ctx := context.Background()
	inner := memstore.New()
	a := ports.NewChunk([]byte("aaaaaaaaaa")) // 10 bytes each
	b := ports.NewChunk([]byte("bbbbbbbbbb"))
	c := ports.NewChunk([]byte("cccccccccc"))
	for _, ch := range []ports.Chunk{a, b, c} {
		if err := inner.Put(ctx, ch); err != nil {
			t.Fatal(err)
		}
	}
	cache := cachestore.Open(inner, 25) // room for two 10-byte chunks

	cache.Get(ctx, a.ID) // [a]
	cache.Get(ctx, b.ID) // [b a]
	cache.Get(ctx, a.ID) // touch a -> [a b]
	cache.Get(ctx, c.ID) // admit c, evict LRU (b) -> [c a]

	// a is still resident -> hit; b was evicted -> miss (re-read from inner).
	h0, m0, _ := cache.Stats()
	if _, err := cache.Get(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	h1, m1, _ := cache.Stats()
	if h1 != h0+1 || m1 != m0 {
		t.Fatalf("a should be a cache hit (h %d->%d, m %d->%d)", h0, h1, m0, m1)
	}
	if _, err := cache.Get(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	if _, m2, _ := cache.Stats(); m2 != m1+1 {
		t.Fatalf("b was evicted, expected a miss (m %d->%d)", m1, m2)
	}
}

func TestDeleteEvictsFromCacheAndInner(t *testing.T) {
	ctx := context.Background()
	inner := memstore.New()
	c := ports.NewChunk([]byte("purge me"))
	if err := inner.Put(ctx, c); err != nil {
		t.Fatal(err)
	}
	cache := cachestore.Open(inner, 1<<20)
	cache.Get(ctx, c.ID) // cache it

	if err := cache.Delete(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	if has, _ := cache.Has(ctx, c.ID); has {
		t.Fatal("Delete must remove from both the cache and the inner store")
	}
	if _, _, used := cache.Stats(); used != 0 {
		t.Fatalf("Delete must free the cached bytes, used=%d", used)
	}
}

// A chunk larger than the whole budget is never cached (it would evict
// everything and still not fit).
func TestOversizeChunkNotCached(t *testing.T) {
	ctx := context.Background()
	inner := memstore.New()
	big := ports.NewChunk(make([]byte, 100))
	if err := inner.Put(ctx, big); err != nil {
		t.Fatal(err)
	}
	cache := cachestore.Open(inner, 25)
	if _, err := cache.Get(ctx, big.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, used := cache.Stats(); used != 0 {
		t.Fatalf("oversize chunk must not be cached, used=%d", used)
	}
}
