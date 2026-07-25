// The churn scenario — M4's money demo. A file is scattered across the
// swarm with NO replication (one copy of each shard; parity is the only
// safety net). Then the network starts dying in waves. Caretaker nodes
// run repair loops that rebuild lost shards from parity and push them
// to fresh nodes. The live view shows redundancy draining with each
// wave and pumping back up between them — and at the end, a survivor
// retrieves the file bit-perfectly from a swarm where a third of the
// nodes are gone.
package sim

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/nerolabs/silt/adapters/simnet"
	"github.com/nerolabs/silt/core/crypto"
	"github.com/nerolabs/silt/core/erasure"
	"github.com/nerolabs/silt/core/manifest"
	"github.com/nerolabs/silt/core/node"
	"github.com/nerolabs/silt/core/pipeline"
	"github.com/nerolabs/silt/ports"
)

type ChurnOpts struct {
	Nodes      int
	FileSize   int
	ChunkSize  int
	Erasure    erasure.Params
	Caretakers int
	Waves      int
	KillFrac   float64 // fraction of ALL nodes killed per wave
	Net        simnet.Config
	NodeCfg    node.Config
	// Report receives live-view lines as the scenario runs (nil = quiet).
	Report func(string)
}

func DefaultChurnOpts() ChurnOpts {
	cfg := node.DefaultConfig()
	cfg.Replication = 1 // parity IS the redundancy; make churn bite
	cfg.RepairInterval = 60 * ports.Second
	cfg.RepairSlack = 2
	return ChurnOpts{
		Nodes:      60,
		FileSize:   512 << 10,
		ChunkSize:  4 << 10,
		Erasure:    erasure.DefaultParams, // 10-of-16
		Caretakers: 3,
		Waves:      3,
		KillFrac:   0.10,
		Net:        simnet.DefaultConfig(),
		NodeCfg:    cfg,
	}
}

type ChurnResult struct {
	Seed           int64
	Root           ports.Hash
	Killed         int
	Repairs        int
	ShardsRebuilt  int
	RepairFailures int
	MinAfterKill   int // lowest stripe redundancy seen right after a wave
	MinFinal       int // lowest stripe redundancy after the last repair window
	Match          bool
	Timeline       []string
	Net            simnet.Stats
	SimSeconds     float64
}

func (r ChurnResult) String() string {
	status := "FAILED"
	if r.Match {
		status = "OK — file survived, bit-perfect"
	}
	return fmt.Sprintf(
		"churn: %s\n  seed %d | killed %d nodes | repairs %d | shards rebuilt %d | repair retries %d\n  redundancy: worst after kills %d, final %d\n  net: %d sent, %d delivered, %d dropped | simulated time: %.0fs",
		status, r.Seed, r.Killed, r.Repairs, r.ShardsRebuilt, r.RepairFailures,
		r.MinAfterKill, r.MinFinal, r.Net.Sent, r.Net.Delivered, r.Net.Dropped, r.SimSeconds)
}

// Churn runs the scenario. The caretakers and the final retriever are
// exempt from the kill list (someone has to be left to tell the story);
// everyone else — including the original publisher — is fair game.
func Churn(seed int64, o ChurnOpts) (ChurnResult, error) {
	res := ChurnResult{Seed: seed}
	cl := NewCluster(seed, o.Nodes, o.Net, o.NodeCfg)
	a := cl.Nodes[0]
	z := cl.Nodes[len(cl.Nodes)-1]
	caretakers := cl.Nodes[1 : 1+o.Caretakers]

	data := make([]byte, o.FileSize)
	cl.rng.Read(data)
	h, err := pipeline.Add(bgCtx, a.Store(), cl.Registry, bytes.NewReader(data),
		pipeline.Options{ChunkSize: o.ChunkSize, Mode: crypto.Convergent, Erasure: o.Erasure})
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
	a.Distribute(entry, m, false, func(int) { distributed = true })
	cl.Sched.Run() // no repair timers yet: runs to quiescence
	if !distributed {
		return res, fmt.Errorf("churn(seed=%d): distribution never completed", seed)
	}

	for _, c := range caretakers {
		c.Care(cl.Registry, h.Care())
	}
	// Let the caretakers' warm start (manifest fetch + announce) finish
	// before the dying begins — they were on duty before the disaster.
	cl.Sched.RunUntil(cl.Sched.Now().Add(30 * ports.Second))
	protected := map[ports.NodeID]bool{z.ID(): true}
	for _, c := range caretakers {
		protected[c.ID()] = true
	}

	report := func(label string) {
		counts := cl.StripeRedundancy(m)
		mn, avg := minAvg(counts)
		line := fmt.Sprintf("t=%6.0fs %-12s alive %2d/%2d | shards/stripe %v | min %2d avg %4.1f | repairs %2d rebuilt %2d",
			float64(cl.Sched.Now())/float64(ports.Second), label,
			cl.AliveCount(), o.Nodes, counts, mn, avg,
			sumStat(caretakers, func(s node.Stats) int { return s.Repairs }),
			sumStat(caretakers, func(s node.Stats) int { return s.ShardsRebuilt }))
		res.Timeline = append(res.Timeline, line)
		if o.Report != nil {
			o.Report(line)
		}
	}

	// Enough sim-time per wave for ~3 full repair sweeps — a sweep
	// probes every shard through full provider walks, which is slow by
	// design (thoroughness beat speed when the liar scenario showed
	// what first-answer provider resolution misses).
	window := ports.Duration(9) * o.NodeCfg.RepairInterval
	report("start")
	// Kill the nodes that actually hold columns (a column-placed file
	// concentrates onto ~n hosts, so random kills mostly miss it), keeping
	// a margin above k so repair always has enough to rebuild; between
	// waves it restores redundancy before the next cull.
	perWave := int(float64(o.Nodes) * o.KillFrac)
	for w := 0; w < o.Waves; w++ {
		res.Killed += cl.KillColumns(m, protected, o.Erasure, o.NodeCfg.RepairSlack, perWave)
		report(fmt.Sprintf("wave %d kill", w+1))
		if mn, _ := minAvg(cl.StripeRedundancy(m)); res.MinAfterKill == 0 || mn < res.MinAfterKill {
			res.MinAfterKill = mn
		}
		for i := 0; i < 3; i++ { // let the repair loop work, reporting as it goes
			cl.Sched.RunUntil(cl.Sched.Now().Add(window / 3))
			report(fmt.Sprintf("wave %d +%ds", w+1, (i+1)*int(window/3/ports.Duration(ports.Second))))
		}
	}
	// The survivor's verdict.
	var out bytes.Buffer
	var getErr error
	got := false
	z.NetGet(cl.Registry, h, &out, func(err error) { getErr = err; got = true })
	cl.Sched.RunUntil(cl.Sched.Now().Add(600 * ports.Second))
	res.Repairs = sumStat(caretakers, func(s node.Stats) int { return s.Repairs })
	res.ShardsRebuilt = sumStat(caretakers, func(s node.Stats) int { return s.ShardsRebuilt })
	res.RepairFailures = sumStat(caretakers, func(s node.Stats) int { return s.RepairFailures })
	res.Net = cl.Net.Stats
	res.SimSeconds = float64(cl.Sched.Now()) / float64(ports.Second)
	if !got {
		return res, fmt.Errorf("churn(seed=%d): NetGet never completed", seed)
	}
	if getErr != nil {
		return res, fmt.Errorf("churn(seed=%d): NetGet: %w", seed, getErr)
	}
	res.Match = bytes.Equal(out.Bytes(), data)
	// Measure final redundancy at the actual end: repair sweeps keep
	// running through the retrieval window, and judging them mid-sweep
	// undercounts their work.
	res.MinFinal, _ = minAvg(cl.StripeRedundancy(m))
	report("final get")
	return res, nil
}

// StripeRedundancy is the harness's omniscient view: for each stripe,
// how many of its stored shards exist on some ALIVE node right now.
func (cl *Cluster) StripeRedundancy(m *manifest.Manifest) []int {
	p := erasure.Params{K: m.K, N: m.N}
	dataIDs, parityIDs := m.ChunkIDs(), m.ParityIDs()
	counts := make([]int, p.Stripes(len(dataIDs)))
	for j := range counts {
		lo, hi := j*p.K, min((j+1)*p.K, len(dataIDs))
		ids := append(append([]ports.ChunkID{}, dataIDs[lo:hi]...),
			parityIDs[j*p.ParityShards():(j+1)*p.ParityShards()]...)
		for _, id := range ids {
			if cl.shardAlive(id) {
				counts[j]++
			}
		}
	}
	return counts
}

// AliveHoldersOf returns the alive nodes holding at least one shard of
// the file, in deterministic (creation) order. Column placement
// concentrates a low-replication file onto ~n hosts, so these are the
// only nodes worth killing to actually stress it — uniform-random kills
// mostly hit hosts that carry nothing.
func (cl *Cluster) AliveHoldersOf(m *manifest.Manifest) []ports.NodeID {
	ids := append(append([]ports.ChunkID{}, m.ChunkIDs()...), m.ParityIDs()...)
	var holders []ports.NodeID
	for _, nd := range cl.Nodes {
		if !cl.Net.Alive(nd.ID()) {
			continue
		}
		for _, id := range ids {
			if ok, _ := nd.Store().Has(bgCtx, id); ok {
				holders = append(holders, nd.ID())
				break
			}
		}
	}
	return holders
}

// KillColumns kills alive column-holders (skipping protected) one at a
// time to stress a column-placed file. A single host can carry more than
// one column, so killing by a fixed count is unsafe; it gates on live
// redundancy instead, stopping when EITHER the full stripes have dropped
// enough that repair will trigger (redundancy < n-slack) OR the thinnest
// stripe is one kill from dipping below k (so the file stays
// recoverable). Gates on stripe 0, a genuinely-full stripe that all full
// stripes track together — the short final stripe has fewer columns and
// would mask the drop. Returns how many hosts it killed. With no
// caretakers, redundancy never climbs back, so later waves find the full
// stripes already below the trigger and kill nothing.
func (cl *Cluster) KillColumns(m *manifest.Manifest, protected map[ports.NodeID]bool, p erasure.Params, slack, maxKill int) int {
	killed := 0
	for _, id := range cl.AliveHoldersOf(m) {
		if protected[id] {
			continue
		}
		counts := cl.StripeRedundancy(m)
		if len(counts) == 0 || counts[0] < p.N-slack {
			break
		}
		if mn, _ := minAvg(counts); mn <= p.K+1 {
			break
		}
		cl.Net.Kill(id)
		if killed++; killed == maxKill {
			break
		}
	}
	return killed
}

func (cl *Cluster) shardAlive(id ports.ChunkID) bool {
	for _, nd := range cl.Nodes {
		if cl.Net.Alive(nd.ID()) {
			if ok, _ := nd.Store().Has(bgCtx, id); ok {
				return true
			}
		}
	}
	return false
}

func (cl *Cluster) AliveCount() int {
	n := 0
	for _, nd := range cl.Nodes {
		if cl.Net.Alive(nd.ID()) {
			n++
		}
	}
	return n
}

func minAvg(counts []int) (int, float64) {
	if len(counts) == 0 {
		return 0, 0
	}
	mn, sum := counts[0], 0
	for _, c := range counts {
		if c < mn {
			mn = c
		}
		sum += c
	}
	return mn, float64(sum) / float64(len(counts))
}

func sumStat(nodes []*node.Node, f func(node.Stats) int) int {
	n := 0
	for _, nd := range nodes {
		n += f(nd.Stats)
	}
	return n
}

// TimelineString joins the live view for printing or comparison.
func (r ChurnResult) TimelineString() string { return strings.Join(r.Timeline, "\n") }
