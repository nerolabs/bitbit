// Package sim is the simulation harness: it wires N nodes together with
// the in-process adapters (simclock scheduler, simnet transport,
// memstores, one shared honest registry) and runs scenarios. The core
// never knows it's in a sim — that's the whole point of the ports.
//
// Everything is derived from one seed. Same seed, same run, byte for
// byte. Every scenario result carries its seed so failures reproduce.
package sim

import (
	"bytes"
	"context"
	"fmt"
	"math/rand"

	"github.com/nerolabs/silt/adapters/memstore"
	"github.com/nerolabs/silt/adapters/simclock"
	"github.com/nerolabs/silt/adapters/simnet"
	"github.com/nerolabs/silt/core/crypto"
	"github.com/nerolabs/silt/core/node"
	"github.com/nerolabs/silt/core/pipeline"
	"github.com/nerolabs/silt/core/registry"
	"github.com/nerolabs/silt/ports"
)

var bgCtx = context.Background()

type Cluster struct {
	Seed     int64
	Sched    *simclock.Scheduler
	Net      *simnet.Network
	Nodes    []*node.Node
	Registry ports.Registry
	rng      *rand.Rand
}

// NewCluster builds and bootstraps nNodes with unbounded stores.
func NewCluster(seed int64, nNodes int, netCfg simnet.Config, nodeCfg node.Config) *Cluster {
	return NewClusterWithStores(seed, nNodes, netCfg, nodeCfg,
		func(int) ports.ChunkStore { return memstore.New() })
}

// NewClusterWithStores builds and bootstraps nNodes, with per-node
// stores from the factory (the capacity scenario hands out bounded
// ones). Each node seeds its routing table with a few earlier nodes
// (like joining via known peers) and runs the standard self-lookup
// join; the scheduler then runs to quiescence, so the returned cluster
// has settled routing tables.
func NewClusterWithStores(seed int64, nNodes int, netCfg simnet.Config, nodeCfg node.Config,
	storeFor func(i int) ports.ChunkStore) *Cluster {
	sched := simclock.New()
	net := simnet.New(sched, seed, netCfg)
	rng := rand.New(rand.NewSource(seed))
	cl := &Cluster{Seed: seed, Sched: sched, Net: net, Registry: registry.New(), rng: rng}

	for i := 0; i < nNodes; i++ {
		var id ports.NodeID
		rng.Read(id[:])
		ep := net.Endpoint(id)
		cl.Nodes = append(cl.Nodes, node.New(id, nodeCfg, sched, ep, storeFor(i)))
	}
	for i, nd := range cl.Nodes {
		if i == 0 {
			continue
		}
		var seeds []ports.NodeID
		for _, j := range rng.Perm(i)[:min(3, i)] {
			seeds = append(seeds, cl.Nodes[j].ID())
		}
		nd.Bootstrap(seeds, func() {})
	}
	sched.Run()
	return cl
}

// ScatterResult is what the scatter scenario reports.
type ScatterResult struct {
	Seed       int64
	Root       ports.Hash
	FileSize   int
	Placed     int // chunk replicas successfully stored on peers
	Match      bool
	Net        simnet.Stats
	Timeouts   int
	SimSeconds float64
}

func (r ScatterResult) String() string {
	status := "FAILED — bytes differ"
	if r.Match {
		status = "OK — bit-perfect"
	}
	return fmt.Sprintf(
		"scatter: %s\n  seed %d | file %d bytes | root %s…\n  placed %d chunk replicas | net: %d sent, %d delivered, %d dropped | %d timeouts\n  simulated time: %.2fs",
		status, r.Seed, r.FileSize, r.Root.String()[:16], r.Placed,
		r.Net.Sent, r.Net.Delivered, r.Net.Dropped, r.Timeouts, r.SimSeconds)
}

// ScatterOpts parametrizes the M3 scenario.
type ScatterOpts struct {
	Nodes     int
	FileSize  int
	ChunkSize int
	Net       simnet.Config
	// Kill, if >0, kills that many provider-holding nodes after
	// distribution — retrieval must survive via replication + parity.
	Kill int
}

func DefaultScatterOpts() ScatterOpts {
	return ScatterOpts{Nodes: 50, FileSize: 1 << 20, ChunkSize: 64 << 10, Net: simnet.DefaultConfig()}
}

// Scatter is the M3 demo: node A adds a file and walks away (keeps no
// chunks); node Z — never having spoken to A — retrieves it from the
// swarm by root hash alone.
func Scatter(seed int64, o ScatterOpts) (ScatterResult, error) {
	res := ScatterResult{Seed: seed, FileSize: o.FileSize}
	cl := NewCluster(seed, o.Nodes, o.Net, node.DefaultConfig())
	a, z := cl.Nodes[0], cl.Nodes[len(cl.Nodes)-1]

	data := make([]byte, o.FileSize)
	cl.rng.Read(data)

	// A ingests locally, then distributes every chunk to the swarm.
	h, err := pipeline.Add(bgCtx, a.Store(), cl.Registry, bytes.NewReader(data),
		pipeline.Options{ChunkSize: o.ChunkSize, Mode: crypto.Convergent})
	if err != nil {
		return res, err
	}
	res.Root = h.Root
	entry, _, _ := cl.Registry.Lookup(bgCtx, h.Root)
	m, err := pipeline.LoadFull(bgCtx, a.Store(), entry, h)
	if err != nil {
		return res, err
	}
	distributed := false
	a.Distribute(entry, m, false, func(placed int) {
		res.Placed = placed
		distributed = true
	})
	cl.Sched.Run()
	if !distributed {
		return res, fmt.Errorf("scatter(seed=%d): distribution never completed", seed)
	}
	if ids, _ := a.Store().List(bgCtx); len(ids) != 0 {
		return res, fmt.Errorf("scatter(seed=%d): A still holds %d chunks after walking away", seed, len(ids))
	}

	for i := 0; i < o.Kill; i++ { // kill arbitrary mid-cluster nodes
		cl.Net.Kill(cl.Nodes[1+i].ID())
	}

	var out bytes.Buffer
	var getErr error
	got := false
	z.NetGet(cl.Registry, h, &out, func(err error) {
		getErr = err
		got = true
	})
	cl.Sched.Run()
	if !got {
		return res, fmt.Errorf("scatter(seed=%d): NetGet never completed", seed)
	}
	res.Timeouts = totalTimeouts(cl)
	res.Net = cl.Net.Stats
	res.SimSeconds = float64(cl.Sched.Now()) / float64(ports.Second)
	if getErr != nil {
		return res, fmt.Errorf("scatter(seed=%d): NetGet: %w", seed, getErr)
	}
	res.Match = bytes.Equal(out.Bytes(), data)
	return res, nil
}

func totalTimeouts(cl *Cluster) int {
	n := 0
	for _, nd := range cl.Nodes {
		n += nd.Stats.Timeouts
	}
	return n
}
