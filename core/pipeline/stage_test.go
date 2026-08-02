package pipeline_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/nerolabs/silt/adapters/memstore"
	"github.com/nerolabs/silt/core/crypto"
	"github.com/nerolabs/silt/core/pipeline"
	"github.com/nerolabs/silt/core/registry"
)

// Stage stores the content but does NOT register the entry — the caller
// publishes only after a confirmed scatter, so a failed distribution never
// leaves a dangling registry entry (register-after-distribute, #65).
func TestStageStoresButDoesNotPublish(t *testing.T) {
	ctx := context.Background()
	store := memstore.New()
	reg := registry.New()

	h, entry, err := pipeline.Stage(ctx, store, bytes.NewReader([]byte("hello silt")),
		pipeline.Options{ChunkSize: testChunkSize, Mode: crypto.Convergent})
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}

	// The registry is untouched: no entry names this root yet.
	if _, ok, _ := reg.Lookup(ctx, h.Root); ok {
		t.Fatal("Stage published a registry entry; it must defer to the caller")
	}
	// But the content IS staged: the entry carries manifest pointers and the
	// manifest chunks are in the store, so the caller can distribute now.
	if len(entry.ManifestChunks) == 0 {
		t.Fatal("Stage returned an entry with no manifest pointers")
	}
	for _, id := range entry.ManifestChunks {
		if _, err := store.Get(ctx, id); err != nil {
			t.Fatalf("staged manifest chunk %s missing from store: %v", id, err)
		}
	}

	// Publishing the returned entry makes it retrievable — proving Stage +
	// an explicit publish is a complete substitute for Add.
	if err := reg.Publish(ctx, entry); err != nil {
		t.Fatalf("publish staged entry: %v", err)
	}
	var out bytes.Buffer
	if err := pipeline.Get(ctx, store, reg, h, &out); err != nil {
		t.Fatalf("Get after staged publish: %v", err)
	}
	if out.String() != "hello silt" {
		t.Fatalf("roundtrip mismatch: %q", out.String())
	}
}

// Add still publishes immediately — the one-shot path is unchanged for
// callers that don't distribute separately (local add, genesis, sim).
func TestAddStillPublishes(t *testing.T) {
	ctx := context.Background()
	store := memstore.New()
	reg := registry.New()

	h, err := pipeline.Add(ctx, store, reg, bytes.NewReader([]byte("hi")),
		pipeline.Options{ChunkSize: testChunkSize, Mode: crypto.Convergent})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if _, ok, _ := reg.Lookup(ctx, h.Root); !ok {
		t.Fatal("Add did not publish a registry entry")
	}
}
