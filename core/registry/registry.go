// Package registry is the v1 append-only root registry: a single honest
// in-process log. It is deliberately NOT a blockchain and doesn't pretend
// to be — it just exposes the same interface a chain-backed registry
// would, so swapping one in later touches zero callers.
package registry

import (
	"context"
	"sync"

	"github.com/nerolabs/silt/ports"
)

// Log is an in-memory ports.Registry.
type Log struct {
	mu      sync.RWMutex
	entries []ports.Entry
	byRoot  map[ports.Hash]int
}

var _ ports.Registry = (*Log)(nil)

func New() *Log {
	return &Log{byRoot: make(map[ports.Hash]int)}
}

// Publish appends e. Re-publishing the same CONTENT is a no-op:
// convergent encryption makes duplicate adds of a file yield an identical
// root, manifest, and size, and the Publisher records only WHO added it,
// not WHAT — so a retry from a fresh CLI identity (each `swarm add` mints
// one) or a second person adding the same file must dedup, not collide.
// Only a genuinely different entry under an existing root is an error.
func (l *Log) Publish(_ context.Context, e ports.Entry) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if i, ok := l.byRoot[e.Root]; ok {
		if sameContent(l.entries[i], e) {
			return nil
		}
		return ports.ErrDupPublish
	}
	l.byRoot[e.Root] = len(l.entries)
	l.entries = append(l.entries, e)
	return nil
}

// sameContent reports whether two entries describe the same file —
// everything but Publisher, which is metadata about who added it.
func sameContent(a, b ports.Entry) bool {
	if a.Root != b.Root || a.FileSize != b.FileSize ||
		len(a.ManifestChunks) != len(b.ManifestChunks) {
		return false
	}
	for i := range a.ManifestChunks {
		if a.ManifestChunks[i] != b.ManifestChunks[i] {
			return false
		}
	}
	return true
}

func (l *Log) Lookup(_ context.Context, root ports.Hash) (ports.Entry, bool, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	i, ok := l.byRoot[root]
	if !ok {
		return ports.Entry{}, false, nil
	}
	return l.entries[i], true, nil
}

func (l *Log) All(_ context.Context) ([]ports.Entry, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := make([]ports.Entry, len(l.entries))
	copy(out, l.entries)
	return out, nil
}
