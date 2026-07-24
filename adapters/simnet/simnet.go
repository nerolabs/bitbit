// Package simnet is the in-process network: a Transport per node, wired
// through the shared simclock scheduler. Latency, packet loss,
// partitions, and node death are all injectable, and every random draw
// comes from one seeded RNG — since the scheduler makes event order
// deterministic, the whole network replay is too.
package simnet

import (
	"fmt"
	"math/rand"

	"github.com/nerolabs/bitbit/ports"
)

// Scheduler is the slice of simclock the network needs.
type Scheduler interface {
	Now() ports.Time
	AfterFunc(d ports.Duration, fn func()) func()
}

// Config shapes every link. Latency is uniform in [LatencyMin,
// LatencyMax]; Loss is per-message drop probability.
type Config struct {
	LatencyMin ports.Duration
	LatencyMax ports.Duration
	Loss       float64
}

func DefaultConfig() Config {
	return Config{LatencyMin: 5 * ports.Millisecond, LatencyMax: 50 * ports.Millisecond}
}

// Stats counts network activity; scenarios print these.
type Stats struct {
	Sent      int
	Delivered int
	Dropped   int // loss + partitions + dead endpoints
}

type Network struct {
	sched     Scheduler
	rng       *rand.Rand
	cfg       Config
	endpoints map[ports.NodeID]*Endpoint
	// partitioned maps node → group; nodes in different groups can't
	// talk. Empty map = no partition.
	group map[ports.NodeID]int
	Stats Stats
}

func New(sched Scheduler, seed int64, cfg Config) *Network {
	return &Network{
		sched:     sched,
		rng:       rand.New(rand.NewSource(seed)),
		cfg:       cfg,
		endpoints: make(map[ports.NodeID]*Endpoint),
		group:     make(map[ports.NodeID]int),
	}
}

// Endpoint is one node's ports.Transport.
type Endpoint struct {
	net     *Network
	id      ports.NodeID
	handler func(from ports.NodeID, msg ports.Message)
	dead    bool
}

var _ ports.Transport = (*Endpoint)(nil)

// Endpoint registers (or returns) the transport for id.
func (n *Network) Endpoint(id ports.NodeID) *Endpoint {
	if ep, ok := n.endpoints[id]; ok {
		return ep
	}
	ep := &Endpoint{net: n, id: id}
	n.endpoints[id] = ep
	return ep
}

func (e *Endpoint) SetHandler(h func(from ports.NodeID, msg ports.Message)) {
	e.handler = h
}

// Send queues delivery after a random latency, unless the message is
// lost, a partition separates the pair, or either end is dead. Send
// itself never fails for network reasons — like UDP, you learn about
// loss by not hearing back.
func (e *Endpoint) Send(to ports.NodeID, msg ports.Message) error {
	n := e.net
	n.Stats.Sent++
	dst, ok := n.endpoints[to]
	if !ok {
		return fmt.Errorf("simnet: unknown node %s", to)
	}
	// Draw randomness unconditionally so a run's RNG stream doesn't
	// depend on which failure checks short-circuit.
	lossDraw := n.rng.Float64()
	latency := n.cfg.LatencyMin
	if jitter := int64(n.cfg.LatencyMax - n.cfg.LatencyMin); jitter > 0 {
		latency += ports.Duration(n.rng.Int63n(jitter + 1))
	}
	if e.dead || dst.dead || n.partitioned(e.id, to) || lossDraw < n.cfg.Loss {
		n.Stats.Dropped++
		return nil
	}
	n.sched.AfterFunc(latency, func() {
		// Re-check at delivery time: the world may have changed in flight.
		if dst.dead || n.partitioned(e.id, to) || dst.handler == nil {
			n.Stats.Dropped++
			return
		}
		n.Stats.Delivered++
		dst.handler(e.id, msg)
	})
	return nil
}

func (n *Network) partitioned(a, b ports.NodeID) bool {
	if len(n.group) == 0 {
		return false
	}
	return n.group[a] != n.group[b]
}

// Partition splits the network: nodes in ids form group 1, everyone
// else group 0. Heal with ClearPartition.
func (n *Network) Partition(ids ...ports.NodeID) {
	n.group = make(map[ports.NodeID]int)
	for _, id := range ids {
		n.group[id] = 1
	}
}

func (n *Network) ClearPartition() { n.group = make(map[ports.NodeID]int) }

// Kill silences a node: everything to or from it is dropped until
// Restart. The node's own state is untouched — that's the point, it
// comes back with old data (and stale views).
func (n *Network) Kill(id ports.NodeID)    { n.endpoints[id].dead = true }
func (n *Network) Restart(id ports.NodeID) { n.endpoints[id].dead = false }
func (n *Network) Alive(id ports.NodeID) bool {
	ep, ok := n.endpoints[id]
	return ok && !ep.dead
}
