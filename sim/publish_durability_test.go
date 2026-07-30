package sim

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nerolabs/silt/adapters/capstore"
	"github.com/nerolabs/silt/adapters/memstore"
	"github.com/nerolabs/silt/adapters/simnet"
	"github.com/nerolabs/silt/core/crypto"
	"github.com/nerolabs/silt/core/node"
	"github.com/nerolabs/silt/core/pipeline"
	"github.com/nerolabs/silt/ports"
)

// flakyStore fails the FIRST Put of each distinct chunk, then accepts —
// modelling the transient placement failures (relay hiccups once the
// nearest nodes cap out and load shifts onto NATed hosts) that stranded
// manifest chunks in the scaling run.
type flakyStore struct {
	ports.ChunkStore
	tried map[ports.ChunkID]bool
}

func newFlaky() *flakyStore {
	return &flakyStore{ChunkStore: memstore.New(), tried: map[ports.ChunkID]bool{}}
}

func (f *flakyStore) Put(ctx context.Context, c ports.Chunk) error {
	if !f.tried[c.ID] {
		f.tried[c.ID] = true
		return errors.New("transient store failure")
	}
	return f.ChunkStore.Put(ctx, c)
}

// TestPublishFailsLoudWhenManifestUnplaceable is the B7 / #60 regression:
// when every storage node refuses (all at capacity — the post-cap state that
// stranded ~14% of the scaling run), a manifest chunk lands nowhere. Before
// the fix, Distribute reported success and the publisher returned a link to
// content it never durably stored (a dangling link). Distribute must now
// report a non-nil error naming the unplaceable manifest chunk, so the
// publisher fails loudly instead.
func TestPublishFailsLoudWhenManifestUnplaceable(t *testing.T) {
	// Node 0 is the publisher (roomy scratch). Nodes 1..3 are storage nodes
	// with a ZERO pledge, so capstore refuses every Put with ErrStoreFull —
	// a swarm whose capacity is exhausted.
	cl := NewClusterWithStores(1, 4, simnet.DefaultConfig(), node.DefaultConfig(),
		func(i int) ports.ChunkStore {
			if i == 0 {
				return memstore.New()
			}
			s, err := capstore.Open(memstore.New(), 0)
			if err != nil {
				t.Fatalf("capstore: %v", err)
			}
			return s
		})
	pub := cl.Nodes[0]

	data := make([]byte, 4096)
	cl.rng.Read(data)
	h, err := pipeline.Add(bgCtx, pub.Store(), cl.Registry, bytes.NewReader(data),
		pipeline.Options{ChunkSize: 1024, Mode: crypto.Convergent})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	entry, _, _ := cl.Registry.Lookup(bgCtx, h.Root)
	m, err := pipeline.LoadFull(bgCtx, pub.Store(), entry, h)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	var derr error
	placed := -1
	pub.Distribute(entry, m, false, func(p int, e error) { placed = p; derr = e })
	cl.Sched.Run()

	if derr == nil {
		t.Fatalf("expected a loud error when no node can hold the manifest chunk "+
			"(placed=%d) — a dangling link would otherwise be returned for unstored content", placed)
	}
	if !strings.Contains(derr.Error(), "manifest chunk") {
		t.Fatalf("error should name the unplaceable manifest chunk, got: %v", derr)
	}
}

// TestManifestPlacementRetriesTransientFailure proves the availability half
// of the #60 fix: a manifest chunk whose first placement fails transiently
// is retried (with a fresh lookup) and succeeds, so the publish completes
// instead of failing — no strand, no loud error.
func TestManifestPlacementRetriesTransientFailure(t *testing.T) {
	flaky := newFlaky()
	// Node 0 publishes; node 1 is the only storage node, and it fails each
	// chunk's first Put before accepting — exactly one transient miss.
	cl := NewClusterWithStores(2, 2, simnet.DefaultConfig(), node.DefaultConfig(),
		func(i int) ports.ChunkStore {
			if i == 0 {
				return memstore.New()
			}
			return flaky
		})
	pub := cl.Nodes[0]

	data := make([]byte, 4096)
	cl.rng.Read(data)
	h, err := pipeline.Add(bgCtx, pub.Store(), cl.Registry, bytes.NewReader(data),
		pipeline.Options{ChunkSize: 1024, Mode: crypto.Convergent})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	entry, _, _ := cl.Registry.Lookup(bgCtx, h.Root)
	m, err := pipeline.LoadFull(bgCtx, pub.Store(), entry, h)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	var derr error
	pub.Distribute(entry, m, false, func(_ int, e error) { derr = e })
	cl.Sched.Run()

	if derr != nil {
		t.Fatalf("retry should have recovered from the transient failure, got: %v", derr)
	}
	for _, id := range entry.ManifestChunks {
		if ok, _ := flaky.Has(bgCtx, id); !ok {
			t.Fatalf("manifest chunk %s not stored after retry", id)
		}
	}
}
