package sim

import (
	"bytes"
	"testing"

	"github.com/nerolabs/silt/core/crypto"
	"github.com/nerolabs/silt/core/erasure"
	"github.com/nerolabs/silt/core/manifest"
	"github.com/nerolabs/silt/core/node"
	"github.com/nerolabs/silt/core/pipeline"
	"github.com/nerolabs/silt/ports"
)

// worstDomainCoResidency is the harness's failure-domain view: for the
// worst stripe, how many of its shards land in a single failure domain.
// 1 means every shard of that stripe is in a distinct domain — losing a
// whole domain costs the stripe one shard. domOf maps each node to its
// domain index.
func (cl *Cluster) worstDomainCoResidency(m *manifest.Manifest, domOf map[ports.NodeID]int) int {
	p := erasure.Params{K: m.K, N: m.N}
	dataIDs, parityIDs := m.ChunkIDs(), m.ParityIDs()
	worst := 0
	for j := 0; j < p.Stripes(len(dataIDs)); j++ {
		lo, hi := j*p.K, min((j+1)*p.K, len(dataIDs))
		ids := append(append([]ports.ChunkID{}, dataIDs[lo:hi]...),
			parityIDs[j*p.ParityShards():(j+1)*p.ParityShards()]...)
		perDomain := map[int]int{}
		for _, id := range ids {
			for _, nd := range cl.Nodes {
				if cl.Net.Alive(nd.ID()) {
					if ok, _ := nd.Store().Has(bgCtx, id); ok {
						perDomain[domOf[nd.ID()]]++
					}
				}
			}
		}
		for _, c := range perDomain {
			if c > worst {
				worst = c
			}
		}
	}
	return worst
}

// worstDomainExclusive is the harness's view of the dispersion audit's
// target: for the worst stripe, the most shards that live in only ONE
// domain (a column with holders in ≥2 domains survives any single domain
// failure and isn't counted). If this exceeds n-k, losing that domain
// would drop the stripe below k.
func (cl *Cluster) worstDomainExclusive(m *manifest.Manifest, domOf map[ports.NodeID]int) int {
	p := erasure.Params{K: m.K, N: m.N}
	dataIDs, parityIDs := m.ChunkIDs(), m.ParityIDs()
	worst := 0
	for j := 0; j < p.Stripes(len(dataIDs)); j++ {
		lo, hi := j*p.K, min((j+1)*p.K, len(dataIDs))
		ids := append(append([]ports.ChunkID{}, dataIDs[lo:hi]...),
			parityIDs[j*p.ParityShards():(j+1)*p.ParityShards()]...)
		exclusive := map[int]int{}
		for _, id := range ids {
			doms := map[int]bool{}
			for _, nd := range cl.Nodes {
				if cl.Net.Alive(nd.ID()) {
					if ok, _ := nd.Store().Has(bgCtx, id); ok {
						doms[domOf[nd.ID()]] = true
					}
				}
			}
			if len(doms) == 1 {
				for d := range doms {
					exclusive[d]++
				}
			}
		}
		for _, c := range exclusive {
			if c > worst {
				worst = c
			}
		}
	}
	return worst
}

// The dispersion audit: even a fully-reachable stripe is fragile if its
// shards have piled into one failure domain. With only two domains,
// publish must put ~8 of each stripe's 16 columns exclusively in each —
// well over the n-k=6 budget — so a single domain failure would break the
// file. The caretaker's audit must notice and replicate the over-exposed
// columns into the other domain until no single domain loss could drop a
// stripe below k.
func TestDispersionAuditRespreadsConcentratedStripe(t *testing.T) {
	o := DefaultChurnOpts() // coded 10-of-16, Replication 1
	cl := NewClusterWithDomains(7, o.Nodes, o.Net, o.NodeCfg, 2)
	domOf := map[ports.NodeID]int{}
	for i, nd := range cl.Nodes {
		domOf[nd.ID()] = i % 2
	}
	a, caretaker := cl.Nodes[0], cl.Nodes[1]

	data := make([]byte, o.FileSize)
	cl.rng.Read(data)
	h, err := pipeline.Add(bgCtx, a.Store(), cl.Registry, bytes.NewReader(data),
		pipeline.Options{ChunkSize: o.ChunkSize, Mode: crypto.Convergent, Erasure: o.Erasure})
	if err != nil {
		t.Fatal(err)
	}
	entry, _, _ := cl.Registry.Lookup(bgCtx, h.Root)
	m, err := pipeline.LoadFull(bgCtx, a.Store(), entry, h)
	if err != nil {
		t.Fatal(err)
	}
	a.Distribute(entry, m, false, node.DerivePorKey(h.LayoutKey()), func(int, error) {})
	cl.Sched.Run()

	before := cl.worstDomainExclusive(m, domOf)
	if before <= m.N-m.K {
		t.Fatalf("seed 7: expected a domain-concentrated publish (worst exclusive > n-k=%d), got %d — scenario proves nothing",
			m.N-m.K, before)
	}

	caretaker.Care(cl.Registry, h.Care())
	cl.Sched.RunUntil(cl.Sched.Now().Add(12 * o.NodeCfg.RepairInterval))

	after := cl.worstDomainExclusive(m, domOf)
	if caretaker.Stats.Dispersals == 0 {
		t.Fatalf("seed 7: audit never dispersed a stripe (before exclusive %d)", before)
	}
	if after > m.N-m.K {
		t.Fatalf("seed 7: audit didn't relieve domain concentration: worst exclusive %d > n-k=%d (was %d), dispersals %d",
			after, m.N-m.K, before, caretaker.Stats.Dispersals)
	}
	t.Logf("worst domain-exclusive shards/stripe: before %d, after %d (%d dispersals)",
		before, after, caretaker.Stats.Dispersals)
}

// Placement should spread a coded file's columns across distinct failure
// domains, not just node IDs. We publish the same file on two identical
// clusters (same seed → same node IDs and DHT layout) that differ only in
// whether the nodes carry domain labels, so one places domain-aware and
// the other domain-blind. Domain-aware placement must pile fewer shards of
// a stripe into one domain — and never enough (k) for a domain to
// reconstruct the data on its own.
func TestFailureDomainPlacementSpreadsColumns(t *testing.T) {
	o := DefaultChurnOpts() // coded 10-of-16, Replication 1
	nDomains := o.Nodes / 3
	domOf := func(cl *Cluster) map[ports.NodeID]int {
		mp := map[ports.NodeID]int{}
		for i, nd := range cl.Nodes {
			mp[nd.ID()] = i % nDomains
		}
		return mp
	}
	publish := func(cl *Cluster) *manifest.Manifest {
		a := cl.Nodes[0]
		data := make([]byte, o.FileSize)
		cl.rng.Read(data)
		h, err := pipeline.Add(bgCtx, a.Store(), cl.Registry, bytes.NewReader(data),
			pipeline.Options{ChunkSize: o.ChunkSize, Mode: crypto.Convergent, Erasure: o.Erasure})
		if err != nil {
			t.Fatal(err)
		}
		entry, _, _ := cl.Registry.Lookup(bgCtx, h.Root)
		m, err := pipeline.LoadFull(bgCtx, a.Store(), entry, h)
		if err != nil {
			t.Fatal(err)
		}
		a.Distribute(entry, m, false, node.DerivePorKey(h.LayoutKey()), func(int, error) {})
		cl.Sched.Run()
		return m
	}

	var sumAware, sumBlind int
	for _, seed := range []int64{1, 2, 3, 7, 11} {
		aware := NewClusterWithDomains(seed, o.Nodes, o.Net, o.NodeCfg, nDomains)
		am := publish(aware)
		wAware := aware.worstDomainCoResidency(am, domOf(aware))
		if wAware >= am.K {
			t.Fatalf("seed %d: a domain holds %d shards of a stripe (>= k=%d) even with domain-aware placement",
				seed, wAware, am.K)
		}

		blind := NewCluster(seed, o.Nodes, o.Net, o.NodeCfg) // same IDs/layout, domains unset
		bm := publish(blind)
		wBlind := blind.worstDomainCoResidency(bm, domOf(blind))

		sumAware += wAware
		sumBlind += wBlind
	}
	if sumAware >= sumBlind {
		t.Fatalf("domain-aware placement didn't reduce domain co-residence: aware %d vs blind %d (summed over seeds)",
			sumAware, sumBlind)
	}
	t.Logf("worst domain co-residence summed over seeds: aware %d, blind %d", sumAware, sumBlind)
}
