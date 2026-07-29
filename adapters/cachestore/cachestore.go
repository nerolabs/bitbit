// Package cachestore is a read-through LRU cache in front of any
// ChunkStore. A cache hit skips two costs the inner store pays on every
// read: the disk (or network) fetch, AND the per-read hash
// re-verification — the cached bytes were verified on the way in, so they
// are trusted on the way out. That re-verify is pure CPU spent re-hashing
// chunks we already trust, and for a popular chunk served thousands of
// times it dominates; caching it is the win (the disk itself is usually
// already warm in the OS page cache).
//
// Bounded by a byte budget; least-recently-used chunks evict when it is
// exceeded. Cache-on-read only: a Put writes straight through without
// warming the cache, so a flood of just-stored-but-never-read chunks
// cannot evict genuinely hot ones (basic scan resistance). Delete evicts,
// so purged or denylisted content is never served from RAM.
package cachestore

import (
	"container/list"
	"context"
	"sync"

	"github.com/nerolabs/silt/ports"
)

type entry struct {
	id   ports.ChunkID
	data []byte
}

type Store struct {
	inner  ports.ChunkStore
	budget int64

	mu    sync.Mutex
	items map[ports.ChunkID]*list.Element // id -> *entry element
	ll    *list.List                      // front = most recently used
	used  int64
	hits  int64
	miss  int64
}

var _ ports.ChunkStore = (*Store)(nil)

// Open wraps inner with a read cache of at most budget bytes. A budget of
// zero or less is a programming error — callers gate on it and simply skip
// wrapping when caching is off.
func Open(inner ports.ChunkStore, budget int64) *Store {
	return &Store{
		inner:  inner,
		budget: budget,
		items:  make(map[ports.ChunkID]*list.Element),
		ll:     list.New(),
	}
}

func (s *Store) Get(ctx context.Context, id ports.ChunkID) (ports.Chunk, error) {
	s.mu.Lock()
	if el, ok := s.items[id]; ok {
		s.ll.MoveToFront(el)
		s.hits++
		// Copy out so a caller can't mutate the cached bytes (memstore does
		// the same). A memcpy is far cheaper than the disk read + re-hash a
		// hit avoids.
		src := el.Value.(*entry).data
		out := make([]byte, len(src))
		copy(out, src)
		s.mu.Unlock()
		return ports.Chunk{ID: id, Data: out}, nil
	}
	s.miss++
	s.mu.Unlock()

	c, err := s.inner.Get(ctx, id)
	if err != nil {
		return c, err
	}
	s.admit(id, c.Data)
	return c, nil
}

// admit inserts a freshly-read chunk, evicting LRU entries until the
// budget holds. Chunks larger than the whole budget are never cached.
func (s *Store) admit(id ports.ChunkID, data []byte) {
	size := int64(len(data))
	if size > s.budget {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.items[id]; ok {
		return // raced with another reader; keep the incumbent
	}
	cp := make([]byte, len(data))
	copy(cp, data)
	el := s.ll.PushFront(&entry{id: id, data: cp})
	s.items[id] = el
	s.used += size
	for s.used > s.budget {
		s.evictLRU()
	}
}

// evictLRU drops the least-recently-used entry. Caller holds s.mu.
func (s *Store) evictLRU() {
	el := s.ll.Back()
	if el == nil {
		return
	}
	s.drop(el)
}

// drop removes el from the cache. Caller holds s.mu.
func (s *Store) drop(el *list.Element) {
	e := el.Value.(*entry)
	s.ll.Remove(el)
	delete(s.items, e.id)
	s.used -= int64(len(e.data))
}

func (s *Store) Put(ctx context.Context, c ports.Chunk) error {
	// Write-through, cache-on-read only: don't warm on Put.
	return s.inner.Put(ctx, c)
}

func (s *Store) Delete(ctx context.Context, id ports.ChunkID) error {
	s.mu.Lock()
	if el, ok := s.items[id]; ok {
		s.drop(el)
	}
	s.mu.Unlock()
	return s.inner.Delete(ctx, id)
}

func (s *Store) Has(ctx context.Context, id ports.ChunkID) (bool, error) {
	s.mu.Lock()
	_, ok := s.items[id]
	s.mu.Unlock()
	if ok {
		return true, nil
	}
	return s.inner.Has(ctx, id)
}

// List delegates: the inner store is authoritative about what exists; the
// cache is only a read accelerator over a subset.
func (s *Store) List(ctx context.Context) ([]ports.ChunkID, error) {
	return s.inner.List(ctx)
}

// Stats reports cache effectiveness: cumulative hits and misses, and the
// bytes currently resident. For observability — did the cache earn its
// memory?
func (s *Store) Stats() (hits, misses, usedBytes int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hits, s.miss, s.used
}
