package httpregistry_test

import (
	"context"
	"errors"
	"testing"

	"github.com/nerolabs/bitbit/adapters/httpregistry"
	"github.com/nerolabs/bitbit/adapters/identity"
	"github.com/nerolabs/bitbit/core/registry"
	"github.com/nerolabs/bitbit/ports"
)

func TestClientServerRoundtrip(t *testing.T) {
	addr, shutdown, err := httpregistry.Serve("127.0.0.1:0", registry.New())
	if err != nil {
		t.Fatal(err)
	}
	defer shutdown()
	c := httpregistry.NewClient("http://" + addr)
	ctx := context.Background()

	e := ports.Entry{
		Root:           ports.HashBytes([]byte("file")),
		ManifestChunks: []ports.ChunkID{ports.HashBytes([]byte("m0")), ports.HashBytes([]byte("m1"))},
		FileSize:       4242,
		Publisher:      ports.HashBytes([]byte("publisher")),
	}
	if err := c.Publish(ctx, e); err != nil {
		t.Fatal(err)
	}
	got, ok, err := c.Lookup(ctx, e.Root)
	if err != nil || !ok {
		t.Fatalf("lookup: ok=%v err=%v", ok, err)
	}
	if got.FileSize != e.FileSize || got.Publisher != e.Publisher ||
		len(got.ManifestChunks) != 2 || got.ManifestChunks[1] != e.ManifestChunks[1] {
		t.Fatalf("entry mangled over HTTP: %+v", got)
	}

	// Registry semantics survive the wire.
	if err := c.Publish(ctx, e); err != nil {
		t.Fatalf("idempotent republish: %v", err)
	}
	conflicting := e
	conflicting.FileSize = 1
	if err := c.Publish(ctx, conflicting); !errors.Is(err, ports.ErrDupPublish) {
		t.Fatalf("conflict: want ErrDupPublish, got %v", err)
	}
	if _, ok, err := c.Lookup(ctx, ports.HashBytes([]byte("nope"))); ok || err != nil {
		t.Fatalf("missing root: ok=%v err=%v", ok, err)
	}
	all, err := c.All(ctx)
	if err != nil || len(all) != 1 {
		t.Fatalf("all: %d entries, err=%v", len(all), err)
	}
}

// Pinned HTTPS: right identity works, wrong identity gets a handshake
// failure — same key-is-identity rule as the swarm transport.
func TestPinnedTLSRegistry(t *testing.T) {
	host := identity.FromSeed(50)
	addr, shutdown, err := httpregistry.ServeTLS("127.0.0.1:0", host, registry.New())
	if err != nil {
		t.Fatal(err)
	}
	defer shutdown()
	ctx := context.Background()
	e := ports.Entry{Root: ports.HashBytes([]byte("f")), FileSize: 1,
		ManifestChunks: []ports.ChunkID{ports.HashBytes([]byte("m"))}}

	good := httpregistry.NewPinnedClient("https://"+addr, host.NodeID())
	if err := good.Publish(ctx, e); err != nil {
		t.Fatalf("pinned publish to the right identity: %v", err)
	}
	if _, ok, err := good.Lookup(ctx, e.Root); err != nil || !ok {
		t.Fatalf("pinned lookup: ok=%v err=%v", ok, err)
	}

	wrong := httpregistry.NewPinnedClient("https://"+addr, identity.FromSeed(51).NodeID())
	if err := wrong.Publish(ctx, e); err == nil {
		t.Fatal("publish pinned to the WRONG identity must fail the handshake")
	}
}
