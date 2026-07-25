// The capacity scenario — M9's demo. Every node pledges a fixed
// budget ("silt daemon -capacity 2G" writ small). Files are added
// until the network fills: spill-over placement keeps chunks landing
// while any node has room, stripe anti-affinity keeps each node's
// death cheap, and every node independently estimates the network's
// total pledged storage — from local knowledge only — and lands near
// the truth.
package sim

import (
	"bytes"
	"fmt"

	"github.com/nerolabs/silt/adapters/capstore"
	"github.com/nerolabs/silt/adapters/memstore"
	"github.com/nerolabs/silt/adapters/simnet"
	"github.com/nerolabs/silt/core/crypto"
	"github.com/nerolabs/silt/core/erasure"
	linkpkg "github.com/nerolabs/silt/core/link"
	"github.com/nerolabs/silt/core/manifest"
	"github.com/nerolabs/silt/core/node"
	"github.com/nerolabs/silt/core/pipeline"
	"github.com/nerolabs/silt/ports"
)

type CapacityOpts struct {
	Nodes      int
	NodePledge int64 // bytes each node contributes
	FileSize   int
	ChunkSize  int
	MaxFiles   int
	Erasure    erasure.Params
	Net        simnet.Config
	NodeCfg    node.Config
	Report     func(string)
}

func DefaultCapacityOpts() CapacityOpts {
	cfg := node.DefaultConfig()
	cfg.Replication = 2
	return CapacityOpts{
		Nodes:      40,
		NodePledge: 512 << 10, // 512 KiB each → 20 MiB network
		FileSize:   256 << 10,
		ChunkSize:  8 << 10,
		MaxFiles:   40,
		Erasure:    erasure.DefaultParams,
		Net:        simnet.DefaultConfig(),
		NodeCfg:    cfg,
	}
}

type CapacityResult struct {
	Seed           int64
	TrueTotal      int64 // sum of pledges (omniscient)
	TrueUsed       int64
	FilesStored    int   // files fully placed at target replication
	FilesDegraded  int   // files placed below target (network filling up)
	StripeConflict int   // stripes with any doubled-up node (info)
	WorstOverlap   int   // most shards of one stripe on any single node
	MedianEstimate int64 // median node's estimate of network total capacity
	EstimateRatio  float64
	Retrieved      bool
	Timeline       []string
	Net            simnet.Stats
}

func (r CapacityResult) String() string {
	return fmt.Sprintf(
		"capacity: seed %d | true %s pledged, %s used | %d files full + %d degraded | median estimate %s (%.2fx true) | worst stripe overlap %d | first file retrievable: %v",
		r.Seed, mb(r.TrueTotal), mb(r.TrueUsed), r.FilesStored, r.FilesDegraded,
		mb(r.MedianEstimate), r.EstimateRatio, r.WorstOverlap, r.Retrieved)
}

func mb(b int64) string { return fmt.Sprintf("%.1fMB", float64(b)/(1<<20)) }

func Capacity(seed int64, o CapacityOpts) (CapacityResult, error) {
	res := CapacityResult{Seed: seed}
	// Nodes 0 and 1 are CLIENTS (publisher / retriever): unbounded
	// scratch space, freeloading (they host nothing for the network) —
	// exactly the M8 ephemeral-client shape. A pledge bounds what you
	// host for others, never your own staging area, and no host ever
	// needs a whole file: that's the product rule this scenario proves.
	caps := make([]*capstore.Store, 0, o.Nodes)
	cl := NewClusterWithStores(seed, o.Nodes, o.Net, o.NodeCfg, func(i int) ports.ChunkStore {
		if i < 2 {
			return memstore.New()
		}
		s, err := capstore.Open(memstore.New(), o.NodePledge)
		if err != nil {
			panic(err)
		}
		caps = append(caps, s)
		return s
	})
	uploader, downloader := cl.Nodes[0], cl.Nodes[1]
	uploader.SetFreeload(true)
	downloader.SetFreeload(true)
	res.TrueTotal = int64(o.Nodes-2) * o.NodePledge

	say := func(line string) {
		res.Timeline = append(res.Timeline, line)
		if o.Report != nil {
			o.Report(line)
		}
	}
	say(fmt.Sprintf("network   | %d hosts × %s pledged = %s total (+2 client nodes)",
		o.Nodes-2, mb(o.NodePledge), mb(res.TrueTotal)))

	// The client publishes files until the swarm can't take one at
	// target replication.
	var firstHandle linkpkg.Handle
	var firstData []byte
	perFileTarget := 0 // learned from the first file's full placement
	for f := 0; f < o.MaxFiles; f++ {
		pub := uploader
		data := make([]byte, o.FileSize)
		cl.rng.Read(data)
		h, err := pipeline.Add(bgCtx, pub.Store(), cl.Registry, bytes.NewReader(data),
			pipeline.Options{ChunkSize: o.ChunkSize, Mode: crypto.Convergent, Erasure: o.Erasure})
		if err != nil {
			return res, err
		}
		if f == 0 {
			firstHandle, firstData = h, data
		}
		entry, _, _ := cl.Registry.Lookup(bgCtx, h.Root)
		m, err := pipeline.LoadFull(bgCtx, pub.Store(), entry, h)
		if err != nil {
			return res, err
		}
		placed := -1
		pub.Distribute(entry, m, false, func(p int) { placed = p })
		cl.Sched.Run()
		if perFileTarget == 0 {
			perFileTarget = placed // first file on an empty network = the ideal
		}
		used, total := networkUsage(caps)
		if placed >= perFileTarget {
			res.FilesStored++
		} else {
			res.FilesDegraded++
		}
		if f%5 == 0 || placed < perFileTarget {
			say(fmt.Sprintf("file %2d   | %3d replicas placed (target %3d) | network %s/%s (%.0f%%)",
				f, placed, perFileTarget, mb(used), mb(total), 100*float64(used)/float64(total)))
		}
		if placed < perFileTarget/2 {
			say("network effectively full; stopping publishers")
			break
		}
	}
	res.TrueUsed, _ = networkUsage(caps)

	// Anti-affinity check (omniscient) on the FIRST file's placement.
	entry, _, _ := cl.Registry.Lookup(bgCtx, firstHandle.Root)
	if m, err := pipeline.LoadFull(bgCtx, anyHolderStore(cl, entry), entry, firstHandle); err == nil {
		res.StripeConflict, res.WorstOverlap = stripeConflicts(cl, m)
	}
	say(fmt.Sprintf("placement | worst single-node loss for any stripe of file 0: %d shard(s) (parity budget %d)",
		res.WorstOverlap, o.Erasure.N-o.Erasure.K))

	// Every node's private estimate of the network total, vs truth.
	var estimates []int64
	for _, nd := range cl.Nodes {
		estimates = append(estimates, nd.EstimateNetwork().EstimatedTotal)
	}
	res.MedianEstimate = median(estimates)
	res.EstimateRatio = float64(res.MedianEstimate) / float64(res.TrueTotal)
	say(fmt.Sprintf("estimates | median node believes the network holds %s (truth %s, ratio %.2f)",
		mb(res.MedianEstimate), mb(res.TrueTotal), res.EstimateRatio))

	// The first file must still come back from a nearly-full network,
	// fetched by the client node (hosts never need whole files).
	var out bytes.Buffer
	var gerr error
	got := false
	downloader.NetGet(cl.Registry, firstHandle, &out, func(err error) { gerr = err; got = true })
	cl.Sched.Run()
	res.Retrieved = got && gerr == nil && bytes.Equal(out.Bytes(), firstData)
	say(fmt.Sprintf("retrieve  | first file from the filled network, bit-perfect: %v", res.Retrieved))
	res.Net = cl.Net.Stats
	return res, nil
}

func networkUsage(caps []*capstore.Store) (used, total int64) {
	for _, s := range caps {
		u, t := s.Capacity()
		used += u
		total += t
	}
	return
}

// anyHolderStore finds a store containing the first manifest chunk so
// the harness can read the manifest omnisciently.
func anyHolderStore(cl *Cluster, entry ports.Entry) ports.ChunkStore {
	if len(entry.ManifestChunks) == 0 {
		return cl.Nodes[0].Store()
	}
	for _, nd := range cl.Nodes {
		if ok, _ := nd.Store().Has(bgCtx, entry.ManifestChunks[0]); ok {
			return nd.Store()
		}
	}
	return cl.Nodes[0].Store()
}

// stripeConflicts measures placement quality omnisciently: how many
// stripes have any doubled-up node, and the worst case — the most
// shards of one stripe any single node holds. The second number is the
// availability truth: one node dying costs each stripe at most that
// many shards. With 2×16 placements per stripe flowing through 8-wide
// candidate windows, some doubling is pigeonhole-inevitable; what
// anti-affinity must guarantee is that the worst case stays small
// relative to the parity budget.
func stripeConflicts(cl *Cluster, m *manifest.Manifest) (stripesWithDoubles, worstOverlap int) {
	p := erasure.Params{K: m.K, N: m.N}
	dataIDs, parityIDs := m.ChunkIDs(), m.ParityIDs()
	for j := 0; j < p.Stripes(len(dataIDs)); j++ {
		lo, hi := j*p.K, min((j+1)*p.K, len(dataIDs))
		ids := append(append([]ports.ChunkID{}, dataIDs[lo:hi]...),
			parityIDs[j*p.ParityShards():(j+1)*p.ParityShards()]...)
		doubled := false
		for _, nd := range cl.Nodes {
			held := 0
			for _, id := range ids {
				if ok, _ := nd.Store().Has(bgCtx, id); ok {
					held++
				}
			}
			if held >= 2 {
				doubled = true
			}
			if held > worstOverlap {
				worstOverlap = held
			}
		}
		if doubled {
			stripesWithDoubles++
		}
	}
	return
}

func median(xs []int64) int64 {
	if len(xs) == 0 {
		return 0
	}
	sorted := append([]int64(nil), xs...)
	for i := range sorted {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[j] < sorted[i] {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}
	return sorted[len(sorted)/2]
}
