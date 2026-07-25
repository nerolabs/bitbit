// Package registry is the v1 append-only root registry: a single honest
// in-process log. It is deliberately NOT a blockchain and doesn't pretend
// to be — it just exposes the same interface a chain-backed registry
// would, so swapping one in later touches zero callers.
package registry

import (
	"context"
	"reflect"
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

// Publish appends e. Re-publishing an identical entry is a no-op
// (convergent encryption makes duplicate adds of the same file normal);
// publishing a different entry under an existing root is an error.
func (l *Log) Publish(_ context.Context, e ports.Entry) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if i, ok := l.byRoot[e.Root]; ok {
		if reflect.DeepEqual(l.entries[i], e) {
			return nil
		}
		return ports.ErrDupPublish
	}
	l.byRoot[e.Root] = len(l.entries)
	l.entries = append(l.entries, e)
	return nil
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
