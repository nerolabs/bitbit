// Package memstore is the in-memory ChunkStore used by tests and the
// simulator. It copies data on the way in and out so callers can't alias
// the stored bytes.
package memstore

import (
	"context"
	"fmt"
	"sync"

	"github.com/nerolabs/bitbit/ports"
)

type Store struct {
	mu     sync.RWMutex
	chunks map[ports.ChunkID][]byte
}

var _ ports.ChunkStore = (*Store)(nil)

func New() *Store {
	return &Store{chunks: make(map[ports.ChunkID][]byte)}
}

func (s *Store) Put(_ context.Context, c ports.Chunk) error {
	if !c.Verify() {
		return fmt.Errorf("memstore put %s: %w", c.ID, ports.ErrCorrupt)
	}
	data := make([]byte, len(c.Data))
	copy(data, c.Data)
	s.mu.Lock()
	s.chunks[c.ID] = data
	s.mu.Unlock()
	return nil
}

func (s *Store) Get(_ context.Context, id ports.ChunkID) (ports.Chunk, error) {
	s.mu.RLock()
	stored, ok := s.chunks[id]
	s.mu.RUnlock()
	if !ok {
		return ports.Chunk{}, fmt.Errorf("memstore get %s: %w", id, ports.ErrNotFound)
	}
	data := make([]byte, len(stored))
	copy(data, stored)
	c := ports.Chunk{ID: id, Data: data}
	if !c.Verify() { // never hand out bytes we can't vouch for
		return ports.Chunk{}, fmt.Errorf("memstore get %s: %w", id, ports.ErrCorrupt)
	}
	return c, nil
}

func (s *Store) Has(_ context.Context, id ports.ChunkID) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.chunks[id]
	return ok, nil
}

func (s *Store) List(_ context.Context) ([]ports.ChunkID, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ids := make([]ports.ChunkID, 0, len(s.chunks))
	for id := range s.chunks {
		ids = append(ids, id)
	}
	return ids, nil
}

func (s *Store) Delete(_ context.Context, id ports.ChunkID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.chunks, id)
	return nil
}

// Corrupt flips a byte of a stored chunk in place. Test/sim hook: it
// exists so scenarios can prove that corruption is always caught.
func (s *Store) Corrupt(id ports.ChunkID) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, ok := s.chunks[id]
	if !ok || len(data) == 0 {
		return false
	}
	data[0] ^= 0xFF
	return true
}
