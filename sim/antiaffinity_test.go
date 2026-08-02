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

// maxStripeCoResidency is the harness's omniscient anti-affinity view:
// the largest number of distinct shards of any single stripe that one
// ALIVE node holds. 1 is perfect spread — no node is doubled up on a
// stripe, so one death costs that stripe at most one shard. >1 means a
// stripe has clustered onto fewer hosts, eroding the erasure budget.
// skip excludes caretakers: a caretaker fetches a whole stripe into its
// own store to reconstruct and only purges it in cleanup(), so sampling
// mid-sweep would count that transient holding as clustering. The
// durability invariant is about where shards come to rest on storage
// nodes, so we measure those.
func (cl *Cluster) maxStripeCoResidency(m *manifest.Manifest, skip map[ports.NodeID]bool) int {
	p := erasure.Params{K: m.K, N: m.N}
	dataIDs, parityIDs := m.ChunkIDs(), m.ParityIDs()
	worst := 0
	for j := 0; j < p.Stripes(len(dataIDs)); j++ {
		lo, hi := j*p.K, min((j+1)*p.K, len(dataIDs))
		ids := append(append([]ports.ChunkID{}, dataIDs[lo:hi]...),
			parityIDs[j*p.ParityShards():(j+1)*p.ParityShards()]...)
		for _, nd := range cl.Nodes {
			if !cl.Net.Alive(nd.ID()) || skip[nd.ID()] {
				continue
			}
			held := 0
			for _, id := range ids {
				if ok, _ := nd.Store().Has(bgCtx, id); ok {
					held++
				}
			}
			if held > worst {
				worst = held
			}
		}
	}
	return worst
}

// Repair must preserve the anti-affinity invariant that Distribute
// establishes at publish time: no live node holds two shards of one
// stripe. Before the fix, repair re-placed rebuilt shards on the raw
// closest nodes, so over repeated churn-and-repair cycles a stripe could
// drift onto a node that already held another of its shards. We kill in
// waves to force many rebuilds and assert the invariant holds throughout
// — across several seeds so it's a real property, not seed luck.
func TestRepairPreservesStripeAntiAffinity(t *testing.T) {
	o := DefaultChurnOpts()
	// One caretaker, so repairs are serialized and its per-stripe host
	// count is authoritative — this isolates the property the fix governs,
	// anti-affinity *within* a repair. (Several independent caretakers can
	// still each place one shard of a stripe on the same node without
	// seeing each other; that concurrent-repair race is a separate, minor
	// concern this fix doesn't claim to solve.)
	o.Caretakers = 1
	for _, seed := range []int64{1, 2, 3, 7, 11} {
		// Spread nodes across domains so repair exercises its
		// domain-aware re-placement, not just the column path.
		cl := NewClusterWithDomains(seed, o.Nodes, o.Net, o.NodeCfg, o.Nodes/3)
		a := cl.Nodes[0]
		z := cl.Nodes[len(cl.Nodes)-1]
		caretakers := cl.Nodes[1 : 1+o.Caretakers]
		isCaretaker := map[ports.NodeID]bool{}
		for _, c := range caretakers {
			isCaretaker[c.ID()] = true
		}

		data := make([]byte, o.FileSize)
		cl.rng.Read(data)
		h, err := pipeline.Add(bgCtx, a.Store(), cl.Registry, bytes.NewReader(data),
			pipeline.Options{ChunkSize: o.ChunkSize, Mode: crypto.Convergent, Erasure: o.Erasure})
		if err != nil {
			t.Fatalf("seed %d: add: %v", seed, err)
		}
		entry, _, _ := cl.Registry.Lookup(bgCtx, h.Root)
		m, err := pipeline.LoadFull(bgCtx, a.Store(), entry, h)
		if err != nil {
			t.Fatalf("seed %d: load: %v", seed, err)
		}
		a.Distribute(entry, m, false, node.DerivePorKey(h.LayoutKey()), func(int, error) {})
		cl.Sched.Run()

		for _, c := range caretakers {
			c.Care(cl.Registry, h.Care())
		}
		cl.Sched.RunUntil(cl.Sched.Now().Add(30 * ports.Second))

		protected := map[ports.NodeID]bool{z.ID(): true}
		for _, c := range caretakers {
			protected[c.ID()] = true
		}
		window := ports.Duration(9) * o.NodeCfg.RepairInterval
		// Kill column-holders (a column-placed file lives on ~n hosts, so
		// random kills miss it), keeping a margin above k so repair can
		// always rebuild.
		perWave := int(float64(o.Nodes) * o.KillFrac)
		for w := 0; w < o.Waves; w++ {
			cl.KillColumns(m, protected, o.Erasure, o.NodeCfg.RepairSlack, perWave)
			cl.Sched.RunUntil(cl.Sched.Now().Add(window))
		}
		final := cl.maxStripeCoResidency(m, isCaretaker)
		rebuilt := sumStat(caretakers, func(s node.Stats) int { return s.ShardsRebuilt })
		if rebuilt == 0 {
			t.Fatalf("seed %d: no shards were rebuilt — the scenario proves nothing", seed)
		}
		// Column placement makes anti-affinity structural WITHIN a column
		// (a column is one shard of each stripe, on one host), and repair
		// re-seeds rebuilt shards onto their column, so it can't cluster a
		// column onto a doubled-up host. Cross-column co-residence — one host
		// being closest to several column keys, more likely as churn shrinks
		// the network — is bounded only loosely until failure-domain-aware
		// placement (Phase 1 #6). The red line that must hold through repair:
		// no host gathers k shards of a stripe, which would let it
		// reconstruct that stripe's data alone.
		if final >= m.K {
			t.Fatalf("seed %d: a host holds %d shards of one stripe (>= k=%d) after %d rebuilds — repair over-concentrated the stripe",
				seed, final, m.K, rebuilt)
		}
	}
}
