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
	"github.com/nerolabs/silt/core/manifest"
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

// selectiveStore refuses to Put any chunk whose ID is in refuse, accepting
// the rest — so a test can let manifest chunks land while starving the data
// shards, isolating the #64 stripe-durability check from the #60 manifest one.
// refuse is shared by pointer and populated AFTER pipeline.Add (which writes
// only to the publisher's own store), so it bites only during Distribute.
type selectiveStore struct {
	ports.ChunkStore
	refuse map[ports.ChunkID]bool
}

func (s *selectiveStore) Put(ctx context.Context, c ports.Chunk) error {
	if s.refuse[c.ID] {
		return errors.New("refused: no capacity for this shard")
	}
	return s.ChunkStore.Put(ctx, c)
}

// codedFile stages a small erasure-coded file on pub and returns its entry
// and manifest. With DefaultParams (k=10,n=16) and a handful of data chunks
// it is a single short stripe: a few real data shards plus 6 parity.
func codedFile(t *testing.T, cl *Cluster, pub *node.Node, size, chunkSize int) (ports.Entry, *manifest.Manifest) {
	t.Helper()
	data := make([]byte, size)
	cl.rng.Read(data)
	h, err := pipeline.Add(bgCtx, pub.Store(), cl.Registry, bytes.NewReader(data),
		pipeline.Options{ChunkSize: chunkSize, Mode: crypto.Convergent})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	entry, _, _ := cl.Registry.Lookup(bgCtx, h.Root)
	m, err := pipeline.LoadFull(bgCtx, pub.Store(), entry, h)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if m.K == 0 {
		t.Fatalf("test needs an erasure-coded file, got k=0")
	}
	return entry, m
}

// TestPublishFailsLoudWhenStripeUnrecoverable is the B7 / #64 regression, the
// data-shard twin of the #60 manifest case: the manifest chunk places fine,
// but every data and parity shard is refused, so the one stripe is left with
// zero of its shards placed — unrecoverable. Before the fix, Distribute
// ignored data-column placement entirely and reported success, returning a
// link to a file the swarm could never rebuild (f123 in the field: "stripe 0:
// only 9 of 16 shards, need k=10"). Distribute must now fail loud, naming the
// understocked stripe, so the publisher never registers the link.
func TestPublishFailsLoudWhenStripeUnrecoverable(t *testing.T) {
	refuse := map[ports.ChunkID]bool{}
	cl := NewClusterWithStores(7, 5, simnet.DefaultConfig(), node.DefaultConfig(),
		func(i int) ports.ChunkStore {
			if i == 0 {
				return memstore.New()
			}
			return &selectiveStore{ChunkStore: memstore.New(), refuse: refuse}
		})
	pub := cl.Nodes[0]
	entry, m := codedFile(t, cl, pub, 4096, 1024)

	// Refuse every data + parity leaf on the storage nodes; leave the manifest
	// chunk placeable so the failure is unambiguously the data stripe, not #60.
	for _, id := range m.Leaves() {
		refuse[id] = true
	}

	var derr error
	placed := -1
	pub.Distribute(entry, m, false, func(p int, e error) { placed = p; derr = e })
	cl.Sched.Run()

	if derr == nil {
		t.Fatalf("expected a loud error when a stripe's shards can't be placed "+
			"(placed=%d) — an unrecoverable file would otherwise get a link", placed)
	}
	if !strings.Contains(derr.Error(), "stripe") || !strings.Contains(derr.Error(), "unrecoverable") {
		t.Fatalf("error should name the understocked stripe, got: %v", derr)
	}
}

// TestPublishSucceedsWhenStripeStillRecoverable proves the #64 check does not
// false-positive: with all 6 parity shards refused but every real data shard
// placed, the stripe still reconstructs (the missing parity isn't needed, and
// the short stripe's padding positions are known zero), so Distribute must
// return NO error — availability degraded, integrity intact, link is real.
func TestPublishSucceedsWhenStripeStillRecoverable(t *testing.T) {
	refuse := map[ports.ChunkID]bool{}
	cl := NewClusterWithStores(11, 5, simnet.DefaultConfig(), node.DefaultConfig(),
		func(i int) ports.ChunkStore {
			if i == 0 {
				return memstore.New()
			}
			return &selectiveStore{ChunkStore: memstore.New(), refuse: refuse}
		})
	pub := cl.Nodes[0]
	entry, m := codedFile(t, cl, pub, 4096, 1024)

	// Refuse only the parity shards; the real data shards still place, so the
	// stripe keeps >= its real-data-shard count and stays recoverable.
	for _, id := range m.ParityIDs() {
		refuse[id] = true
	}

	var derr error
	pub.Distribute(entry, m, false, func(_ int, e error) { derr = e })
	cl.Sched.Run()

	if derr != nil {
		t.Fatalf("a recoverable stripe (all real data placed, only parity lost) "+
			"must not fail loud, got: %v", derr)
	}
}
