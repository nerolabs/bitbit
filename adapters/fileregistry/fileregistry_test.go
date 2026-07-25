package fileregistry_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/nerolabs/silt/adapters/fileregistry"
	"github.com/nerolabs/silt/ports"
)

func TestPersistsAcrossReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "registry.jsonl")

	e := ports.Entry{
		Root:           ports.HashBytes([]byte("file one")),
		ManifestChunks: []ports.ChunkID{ports.HashBytes([]byte("m0")), ports.HashBytes([]byte("m1"))},
		FileSize:       12345,
	}

	r1, err := fileregistry.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := r1.Publish(ctx, e); err != nil {
		t.Fatal(err)
	}

	r2, err := fileregistry.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	got, ok, err := r2.Lookup(ctx, e.Root)
	if err != nil || !ok {
		t.Fatalf("Lookup after reopen: ok=%v err=%v", ok, err)
	}
	if got.FileSize != e.FileSize || len(got.ManifestChunks) != 2 || got.ManifestChunks[1] != e.ManifestChunks[1] {
		t.Fatalf("entry mangled across reopen: %+v", got)
	}

	// Same append-only semantics as the in-memory log.
	if err := r2.Publish(ctx, e); err != nil {
		t.Fatalf("identical republish: %v", err)
	}
	e.FileSize = 1
	if err := r2.Publish(ctx, e); !errors.Is(err, ports.ErrDupPublish) {
		t.Fatalf("conflicting publish: want ErrDupPublish, got %v", err)
	}
}

func TestOpenMissingFileIsEmpty(t *testing.T) {
	r, err := fileregistry.Open(filepath.Join(t.TempDir(), "nope.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	all, err := r.All(context.Background())
	if err != nil || len(all) != 0 {
		t.Fatalf("want empty registry, got %d entries, err %v", len(all), err)
	}
}
