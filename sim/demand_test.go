package sim

import (
	"bytes"
	"io"
	"testing"

	"github.com/nerolabs/silt/core/crypto"
	"github.com/nerolabs/silt/core/node"
	"github.com/nerolabs/silt/core/pipeline"
	"github.com/nerolabs/silt/ports"
)

// leasedTotal sums the demand-driven cache copies held across the swarm.
func (cl *Cluster) leasedTotal() int {
	n := 0
	for _, nd := range cl.Nodes {
		n += nd.LeasedCount()
	}
	return n
}

// Demand-responsive dispersion: a file read hard should fan out to more
// hosts (leased cache copies) so the load divides, and when the reads stop
// those extra copies should expire — the file contracts back to its
// baseline placement rather than hoarding capacity forever.
func TestDemandFanoutAndCooldown(t *testing.T) {
	cfg := node.DefaultConfig()
	cfg.Replication = 1 // few baseline holders, so fan-out is easy to see
	cfg.HotThreshold = 4
	cfg.DemandInterval = 300 * ports.Second // wide, so the whole hammer lands before a tick
	cfg.LeaseTTL = 300 * ports.Second
	cl := NewClusterWithDomains(3, 30, DefaultChurnOpts().Net, cfg, 10)
	a := cl.Nodes[0]
	reader := cl.Nodes[len(cl.Nodes)-1]

	data := make([]byte, 256<<10)
	cl.rng.Read(data)
	h, err := pipeline.Add(bgCtx, a.Store(), cl.Registry, bytes.NewReader(data),
		pipeline.Options{ChunkSize: 4 << 10, Mode: crypto.Convergent}) // default 10/16 erasure
	if err != nil {
		t.Fatal(err)
	}
	entry, _, _ := cl.Registry.Lookup(bgCtx, h.Root)
	m, err := pipeline.LoadFull(bgCtx, a.Store(), entry, h)
	if err != nil {
		t.Fatal(err)
	}
	a.Distribute(entry, m, false, func(int, error) {})
	cl.Sched.Run()

	if got := cl.leasedTotal(); got != 0 {
		t.Fatalf("expected no cache copies before any load, got %d", got)
	}

	// Hammer the whole file: the reader fetches it over and over (clearing
	// its cache each time so every read re-serves from the swarm), all
	// inside one demand interval so serve counts pass the hot threshold.
	leaves := m.Leaves()
	for i := 0; i < 2*cfg.HotThreshold; i++ {
		for _, id := range leaves {
			reader.Store().Delete(bgCtx, id)
		}
		done := false
		reader.NetGet(cl.Registry, h, io.Discard, func(error) { done = true })
		for guard := 0; !done && guard < 60; guard++ {
			cl.Sched.RunUntil(cl.Sched.Now().Add(ports.Second))
		}
		if !done {
			t.Fatalf("read %d never completed", i)
		}
	}

	// A couple of demand intervals to let hot holders fan out.
	cl.Sched.RunUntil(cl.Sched.Now().Add(2 * cfg.DemandInterval))
	hot := cl.leasedTotal()
	if hot == 0 {
		t.Fatal("a hammered file never fanned out any cache copies")
	}

	// Cool off: no more reads. After the lease TTL the cache copies expire.
	cl.Sched.RunUntil(cl.Sched.Now().Add(3 * cfg.LeaseTTL))
	cold := cl.leasedTotal()
	if cold != 0 {
		t.Fatalf("cache copies didn't expire on cooldown: %d still leased (peak %d)", cold, hot)
	}
	t.Logf("leased cache copies: baseline 0 → hot %d → cooled %d", hot, cold)
}
