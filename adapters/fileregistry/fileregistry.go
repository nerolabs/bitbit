// Package fileregistry persists the registry as JSON lines so the CLI
// survives between invocations. Same append-only semantics as
// core/registry; the file is just the durability layer.
package fileregistry

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sync"

	"github.com/nerolabs/bitbit/core/registry"
	"github.com/nerolabs/bitbit/ports"
)

type Registry struct {
	mu   sync.Mutex
	log  *registry.Log // in-memory source of truth for semantics
	path string
}

var _ ports.Registry = (*Registry)(nil)

// entryJSON is the wire form: hex strings so the log file is greppable.
type entryJSON struct {
	Root           string   `json:"root"`
	ManifestChunks []string `json:"manifest_chunks"`
	FileSize       int64    `json:"file_size"`
	Publisher      string   `json:"publisher,omitempty"`
}

func Open(path string) (*Registry, error) {
	r := &Registry{log: registry.New(), path: path}
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return r, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<20)
	line := 0
	for sc.Scan() {
		line++
		var ej entryJSON
		if err := json.Unmarshal(sc.Bytes(), &ej); err != nil {
			return nil, fmt.Errorf("fileregistry %s:%d: %w", path, line, err)
		}
		e, err := ej.toEntry()
		if err != nil {
			return nil, fmt.Errorf("fileregistry %s:%d: %w", path, line, err)
		}
		if err := r.log.Publish(context.Background(), e); err != nil {
			return nil, fmt.Errorf("fileregistry %s:%d: %w", path, line, err)
		}
	}
	return r, sc.Err()
}

func (r *Registry) Publish(ctx context.Context, e ports.Entry) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Idempotent republish returns nil from the log without needing a
	// second line in the file.
	if _, ok, _ := r.log.Lookup(ctx, e.Root); ok {
		return r.log.Publish(ctx, e)
	}
	if err := r.log.Publish(ctx, e); err != nil {
		return err
	}
	ej := entryJSON{Root: e.Root.String(), FileSize: e.FileSize}
	if e.Publisher != (ports.NodeID{}) {
		ej.Publisher = e.Publisher.String()
	}
	for _, id := range e.ManifestChunks {
		ej.ManifestChunks = append(ej.ManifestChunks, id.String())
	}
	b, err := json.Marshal(ej)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(r.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(b, '\n')); err != nil {
		return err
	}
	return f.Sync()
}

func (r *Registry) Lookup(ctx context.Context, root ports.Hash) (ports.Entry, bool, error) {
	return r.log.Lookup(ctx, root)
}

func (r *Registry) All(ctx context.Context) ([]ports.Entry, error) {
	return r.log.All(ctx)
}

func (ej entryJSON) toEntry() (ports.Entry, error) {
	root, err := ports.ParseHash(ej.Root)
	if err != nil {
		return ports.Entry{}, err
	}
	e := ports.Entry{Root: root, FileSize: ej.FileSize}
	if ej.Publisher != "" {
		if e.Publisher, err = ports.ParseHash(ej.Publisher); err != nil {
			return ports.Entry{}, err
		}
	}
	for _, s := range ej.ManifestChunks {
		id, err := ports.ParseHash(s)
		if err != nil {
			return ports.Entry{}, err
		}
		e.ManifestChunks = append(e.ManifestChunks, id)
	}
	return e, nil
}
