// Package memproofs is an in-memory ports.ProofStore — for tests and any
// node whose chunk store is itself ephemeral (a restart loses the chunks too,
// so there is nothing to re-announce). The disk equivalent is adapters/diskproofs.
package memproofs

import (
	"sync"

	"github.com/nerolabs/silt/ports"
)

type Store struct {
	mu sync.Mutex
	m  map[ports.ChunkID]ports.StorageProof
}

var _ ports.ProofStore = (*Store)(nil)

func New() *Store { return &Store{m: make(map[ports.ChunkID]ports.StorageProof)} }

func (s *Store) Put(id ports.ChunkID, p ports.StorageProof) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Copy the Path slice so a later mutation of the caller's proof can't
	// alias what we stored.
	cp := p
	if p.Path != nil {
		cp.Path = append([]ports.Hash(nil), p.Path...)
	}
	s.m[id] = cp
	return nil
}

func (s *Store) Load() (map[ports.ChunkID]ports.StorageProof, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[ports.ChunkID]ports.StorageProof, len(s.m))
	for id, p := range s.m {
		out[id] = p
	}
	return out, nil
}

func (s *Store) Delete(id ports.ChunkID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, id)
	return nil
}
