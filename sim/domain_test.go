package sim

import (
	"bytes"
	"testing"

	"github.com/nerolabs/silt/core/crypto"
	"github.com/nerolabs/silt/core/erasure"
	"github.com/nerolabs/silt/core/manifest"
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
		a.Distribute(entry, m, false, func(int) {})
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
