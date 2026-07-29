package registry_test

import (
	"context"
	"errors"
	"testing"

	"github.com/nerolabs/silt/core/registry"
	"github.com/nerolabs/silt/ports"
)

func entry(rootByte byte, size int64) ports.Entry {
	var root ports.Hash
	root[0] = rootByte
	return ports.Entry{Root: root, FileSize: size,
		ManifestChunks: []ports.ChunkID{ports.HashBytes([]byte{rootByte})}}
}

func TestPublishLookupAll(t *testing.T) {
	ctx := context.Background()
	r := registry.New()
	e1, e2 := entry(1, 100), entry(2, 200)
	if err := r.Publish(ctx, e1); err != nil {
		t.Fatal(err)
	}
	if err := r.Publish(ctx, e2); err != nil {
		t.Fatal(err)
	}
	got, ok, err := r.Lookup(ctx, e1.Root)
	if err != nil || !ok || got.FileSize != 100 {
		t.Fatalf("Lookup: %+v ok=%v err=%v", got, ok, err)
	}
	all, _ := r.All(ctx)
	if len(all) != 2 || all[0].Root != e1.Root || all[1].Root != e2.Root {
		t.Fatal("All must preserve append order")
	}
}

func TestRepublishSemantics(t *testing.T) {
	ctx := context.Background()
	r := registry.New()
	e := entry(1, 100)
	if err := r.Publish(ctx, e); err != nil {
		t.Fatal(err)
	}
	if err := r.Publish(ctx, e); err != nil {
		t.Fatalf("identical republish must be a no-op, got %v", err)
	}
	conflicting := e
	conflicting.FileSize = 999
	if err := r.Publish(ctx, conflicting); !errors.Is(err, ports.ErrDupPublish) {
		t.Fatalf("conflicting publish: want ErrDupPublish, got %v", err)
	}
	all, _ := r.All(ctx)
	if len(all) != 1 {
		t.Fatalf("log has %d entries, want 1", len(all))
	}
}

func TestRepublishIgnoresPublisher(t *testing.T) {
	ctx := context.Background()
	r := registry.New()
	e := entry(1, 100)
	e.Publisher = ports.HashBytes([]byte("alice"))
	if err := r.Publish(ctx, e); err != nil {
		t.Fatal(err)
	}
	// Same content, different publisher — a retry from a fresh CLI identity
	// (each `swarm add` mints one), or a second person adding the same file.
	// Must dedup, not collide: regression for the "already published with
	// different entry" failure that made retries unrecoverable (#46).
	other := e
	other.Publisher = ports.HashBytes([]byte("bob"))
	if err := r.Publish(ctx, other); err != nil {
		t.Fatalf("same content from a different publisher must be idempotent, got %v", err)
	}
	if all, _ := r.All(ctx); len(all) != 1 {
		t.Fatalf("log has %d entries, want 1 (content dedup)", len(all))
	}
}

func TestLookupMissing(t *testing.T) {
	_, ok, err := registry.New().Lookup(context.Background(), ports.HashBytes([]byte("nope")))
	if ok || err != nil {
		t.Fatalf("ok=%v err=%v, want false,nil", ok, err)
	}
}
