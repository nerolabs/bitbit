package e2e

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/nerolabs/silt/adapters/httpregistry"
	"github.com/nerolabs/silt/ports"
)

var reRegistryOnly = regexp.MustCompile(`registry-only: ([0-9a-f]{64}) serving a file-backed registry at https://(\S+)`)

// TestRegistryOnlyMode (#47): a daemon started with -registry-only serves a file-backed
// registry over HTTPS while constructing NO storage node at all — the leanest public-
// registry role, below -freeload (which is still a full routing node that just refuses
// to host content). We prove it serves publish + lookup over real TLS, and that it never
// announced itself as a routing peer (no node/transport was built).
func TestRegistryOnlyMode(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e spawns processes; skipped under -short")
	}
	d := startDaemon(t, "registry-only",
		"-registry-only", "-serve-registry", "127.0.0.1:0", "-store", t.TempDir(), "-id-seed", "9001")
	m := d.waitFor(t, reRegistryOnly, 20*time.Second)
	regID, err := ports.ParseHash(m[1])
	if err != nil {
		t.Fatalf("parse registry id: %v", err)
	}
	client := httpregistry.NewPinnedClient("https://"+m[2], regID)

	entry := ports.Entry{
		Root:           ports.HashBytes([]byte("registry-only-root")),
		ManifestChunks: []ports.ChunkID{ports.HashBytes([]byte("registry-only-manifest"))},
		FileSize:       1234,
	}
	ctx := context.Background()
	if err := client.Publish(ctx, entry); err != nil {
		t.Fatalf("publish to registry-only daemon: %v", err)
	}
	got, ok, err := client.Lookup(ctx, entry.Root)
	if err != nil || !ok {
		t.Fatalf("lookup after publish: ok=%v err=%v", ok, err)
	}
	if got.Root != entry.Root {
		t.Fatalf("looked-up root %s != published %s", got.Root, entry.Root)
	}

	// It constructed NO storage node: a registry-only daemon never advertises a routing
	// peer address (the -registry-only branch returns before the transport/node exist).
	if peer := d.out.find(rePeer); peer != nil {
		t.Fatalf("registry-only daemon must build no storage node, but it announced a peer: %v", peer)
	}
}
