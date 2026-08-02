// Package simnet is the in-process network: a Transport per node, wired
// through the shared simclock scheduler. Latency, packet loss,
// partitions, and node death are all injectable, and every random draw
// comes from one seeded RNG — since the scheduler makes event order
// deterministic, the whole network replay is too.
package simnet

import (
	"fmt"
	"math/rand"

	"github.com/nerolabs/silt/ports"
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
	Dropped   int // loss + partitions + dead endpoints + unsolicited-into-NAT
	Relayed   int // delivered the long way, spliced through the relay (#27)
	// Kinds counts attempted sends per MsgKind, so a test can assert a
	// message type never crossed the wire (e.g. an audit that must verify
	// WITHOUT fetching ground truth sends zero MsgFetchChunk). Indexed by
	// MsgKind; an array keeps Stats value-copyable.
	Kinds [64]int
}

type Network struct {
	sched     Scheduler
	rng       *rand.Rand
	cfg       Config
	endpoints map[ports.NodeID]*Endpoint
	// partitioned maps node → group; nodes in different groups can't
	// talk. Empty map = no partition.
	group map[ports.NodeID]int
	// nat models home routers (#27): a node listed here is un-dialable
	// cold from off its LAN. Empty (the default) = a flat, public net,
	// so directlyReachable short-circuits and there is zero overhead.
	nat map[ports.NodeID]natBox
	// holes are conntrack return mappings: holes[{src,dst}] means src may
	// dial dst because a path was already opened (dst dialed out, or a
	// punch landed). Only reverse paths into a NATed node are tracked.
	holes    map[[2]ports.NodeID]bool
	relay    ports.NodeID
	hasRelay bool
	Stats    Stats
}

func New(sched Scheduler, seed int64, cfg Config) *Network {
	return &Network{
		sched:     sched,
		rng:       rand.New(rand.NewSource(seed)),
		cfg:       cfg,
		endpoints: make(map[ports.NodeID]*Endpoint),
		group:     make(map[ports.NodeID]int),
		nat:       make(map[ports.NodeID]natBox),
		holes:     make(map[[2]ports.NodeID]bool),
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
	if int(msg.Kind) < len(n.Stats.Kinds) {
		n.Stats.Kinds[msg.Kind]++
	}
	dst, ok := n.endpoints[to]
	if !ok {
		return fmt.Errorf("simnet: unknown node %s", to)
	}
	// Draw randomness unconditionally so a run's RNG stream doesn't
	// depend on which failure checks short-circuit.
	lossDraw := n.rng.Float64()
	latency := n.drawLatency()
	if e.dead || dst.dead || n.partitioned(e.id, to) || lossDraw < n.cfg.Loss {
		n.Stats.Dropped++
		return nil
	}
	// NAT routing (#27): deliver direct if the destination is dialable,
	// else splice through the relay, else the packet is an unsolicited
	// inbound to a NAT with nowhere to go. In a flat net (no NAT
	// configured) directlyReachable is always true, so neither the extra
	// latency draw nor these branches ever run — determinism preserved.
	relayed := false
	if !n.directlyReachable(e.id, to) {
		if !n.canRelay(e.id, to) {
			n.Stats.Dropped++
			return nil
		}
		relayed = true
		latency += n.drawLatency() // a second hop, in through the relay
		n.Stats.Relayed++
	}
	n.sched.AfterFunc(latency, func() {
		// Re-check at delivery time: the world may have changed in flight.
		if dst.dead || n.partitioned(e.id, to) || dst.handler == nil {
			n.Stats.Dropped++
			return
		}
		// A direct delivery opens the reverse mapping: the callee may now
		// dial the NATed caller back (conntrack return path). A relayed
		// one does not — that traffic never touched the two NATs directly,
		// so a later direct dial still needs a punch.
		if !relayed && n.isNAT(e.id) {
			n.holes[[2]ports.NodeID{to, e.id}] = true
		}
		n.Stats.Delivered++
		dst.handler(e.id, msg)
	})
	return nil
}

func (n *Network) drawLatency() ports.Duration {
	latency := n.cfg.LatencyMin
	if jitter := int64(n.cfg.LatencyMax - n.cfg.LatencyMin); jitter > 0 {
		latency += ports.Duration(n.rng.Int63n(jitter + 1))
	}
	return latency
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

// --- NAT model (#27): the deterministic mirror of the Docker harness, so
// the relay and hole-punch paths get fast, CI-native coverage too. A real
// home router lets a node dial out (and holds the reverse mapping open so
// replies get back in) but drops unsolicited inbound — so two NATed nodes
// can't dial each other cold; they meet through the relay, or hole-punch. ---

// natBox is one node's NAT: which LAN it sits behind (peers sharing a LAN
// are on one private segment and dial each other directly) and whether
// it's symmetric — a fresh external port per destination, which makes a
// relay-observed endpoint useless for a punch (see HolePunch).
type natBox struct {
	lan       int
	symmetric bool
}

// NAT places id behind a home router on the given LAN (any nonzero id;
// peers sharing it are on one private segment). The node can always dial
// out — and dialing out opens a return mapping so the callee can reply —
// but off-LAN peers can't dial it cold. symmetric picks the NAT's port
// behaviour, which decides whether it can hole-punch.
func (n *Network) NAT(id ports.NodeID, lan int, symmetric bool) {
	n.nat[id] = natBox{lan: lan, symmetric: symmetric}
}

// Relay designates a public node that splices traffic between two peers
// that can't dial each other directly. It models the content-blind byte
// relay: reachable by anyone, and holding a mapping open to every NATed
// node that registered with it (so it can reach them, and they it).
func (n *Network) Relay(id ports.NodeID) { n.relay, n.hasRelay = id, true }

// HolePunch models the #27 coordinated simultaneous-open: both peers fire
// at each other's relay-observed endpoint at once. For endpoint-
// independent (cone) NATs the crossing packets leave matching mappings
// and a direct path opens; if either side is symmetric, its per-
// destination port allocation makes the observed endpoint useless and the
// punch fails — the peers keep leaning on the relay. Reports whether a
// direct path opened.
func (n *Network) HolePunch(a, b ports.NodeID) bool {
	if n.symmetric(a) || n.symmetric(b) {
		return false
	}
	n.holes[[2]ports.NodeID{a, b}] = true
	n.holes[[2]ports.NodeID{b, a}] = true
	return true
}

// directlyReachable reports whether from can put a packet on to without a
// relay. The relay is public and holds a mapping to every registrant, so
// it's always dialable either way; a public destination is dialable by
// anyone; a NATed destination only via a LAN-mate or an already-open
// mapping (to dialed out earlier, or a punch landed).
func (n *Network) directlyReachable(from, to ports.NodeID) bool {
	if n.hasRelay && (to == n.relay || from == n.relay) {
		return true
	}
	tb, natted := n.nat[to]
	if !natted {
		return true
	}
	if fb, ok := n.nat[from]; ok && fb.lan == tb.lan {
		return true
	}
	return n.holes[[2]ports.NodeID{from, to}]
}

// canRelay reports whether the relay can bridge from→to: it must exist
// and be on the same side of any partition as both ends.
func (n *Network) canRelay(from, to ports.NodeID) bool {
	return n.hasRelay && !n.partitioned(from, n.relay) && !n.partitioned(n.relay, to)
}

func (n *Network) isNAT(id ports.NodeID) bool { _, ok := n.nat[id]; return ok }

func (n *Network) symmetric(id ports.NodeID) bool {
	nb, ok := n.nat[id]
	return ok && nb.symmetric
}
