package pipeline_test

import (
	"bytes"
	"context"
	"errors"
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

// RegisterAfterDistribute is the gate the networked publish paths (swarm
// add, the UI) run from their Distribute callback. A FAILED scatter must
// leave the registry untouched — the exact register-after-distribute promise
// of #65: no link is ever registered for content the swarm didn't durably
// take. This is the failing-first guard: revert either call site to publish
// unconditionally and this fails.
func TestRegisterAfterDistributeSkipsPublishOnScatterFailure(t *testing.T) {
	ctx := context.Background()
	store := memstore.New()
	reg := registry.New()

	// Stage (does not publish) so we have a real entry to gate on.
	h, entry, err := pipeline.Stage(ctx, store, bytes.NewReader([]byte("stranded")),
		pipeline.Options{ChunkSize: testChunkSize, Mode: crypto.Convergent})
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}

	scatterErr := errors.New("stripe unrecoverable: network full")
	placed, gotErr := pipeline.RegisterAfterDistribute(ctx, reg, entry, 3, scatterErr)
	if !errors.Is(gotErr, scatterErr) {
		t.Fatalf("scatter error must be surfaced unchanged, got: %v", gotErr)
	}
	if placed != 3 {
		t.Fatalf("placement count must pass through, got %d", placed)
	}
	if _, ok, _ := reg.Lookup(ctx, h.Root); ok {
		t.Fatal("a failed scatter left a dangling registry entry (#65 regression)")
	}
}

// The other half: a confirmed scatter (derr == nil) DOES publish, and a
// publish error is surfaced rather than swallowed.
func TestRegisterAfterDistributePublishesOnSuccess(t *testing.T) {
	ctx := context.Background()
	store := memstore.New()
	reg := registry.New()

	h, entry, err := pipeline.Stage(ctx, store, bytes.NewReader([]byte("placed")),
		pipeline.Options{ChunkSize: testChunkSize, Mode: crypto.Convergent})
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	if _, gotErr := pipeline.RegisterAfterDistribute(ctx, reg, entry, 7, nil); gotErr != nil {
		t.Fatalf("confirmed scatter should publish cleanly, got: %v", gotErr)
	}
	if _, ok, _ := reg.Lookup(ctx, h.Root); !ok {
		t.Fatal("a confirmed scatter did not register the entry")
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
