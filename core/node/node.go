// Package node composes store + transport + clock + DHT into a peer.
// Behaviors: answer DHT queries, store pushed chunks, serve fetches,
// announce what it holds, and retrieve files from the swarm.
//
// Concurrency model: there is none, on purpose. All of a node's code
// runs inside single-threaded scheduler callbacks (transport handler or
// timer), so there are no locks and no goroutines; long operations are
// chains of callbacks. This is what keeps the sim deterministic — and a
// future real-network adapter just has to serialize its I/O onto one
// loop to inherit the same guarantee.
package node

import (
	"context"
	"errors"
	"fmt"

	"shardnet/core/dht"
	"shardnet/ports"
)

type Config struct {
	K              int            // Kademlia bucket size
	Alpha          int            // lookup parallelism
	RequestTimeout ports.Duration // per-request; a timeout marks the peer failed
	// Replication is how many closest nodes receive each chunk at
	// distribute/repair time. With erasure coding doing the heavy
	// lifting, even 1 is viable — parity across nodes replaces copies.
	Replication int
	// RepairInterval is how often a caretaker sweeps the files it cares
	// about; RepairSlack is how many missing shards a stripe may have
	// before repair kicks in (repair when missing > slack).
	RepairInterval ports.Duration
	RepairSlack    int
}

func DefaultConfig() Config {
	return Config{
		K: 8, Alpha: 3,
		RequestTimeout: 500 * ports.Millisecond,
		Replication:    3,
		RepairInterval: 60 * ports.Second,
		RepairSlack:    2,
	}
}

var ErrTimeout = errors.New("node: request timed out")

// Stats are per-node counters the sim reports on.
type Stats struct {
	QueriesSent    int // DHT + fetch requests issued
	Timeouts       int
	ChunksServed   int
	ChunksReceived int // chunks pushed to us via StoreChunk
	BytesServed    int64
	Probes         int // shard availability checks (repair loop)
	Repairs        int // stripes repaired
	ShardsRebuilt  int // shards reconstructed and re-distributed
	RepairFailures int // repair attempts that couldn't reconstruct (retried next sweep)
}

type pending struct {
	cb     func(ports.Message, error)
	cancel func()
	to     ports.NodeID
}

type Node struct {
	cfg   Config
	id    ports.NodeID
	clock ports.Clock
	tr    ports.Transport
	store ports.ChunkStore
	table *dht.Table
	provs *dht.Providers

	rid     uint64
	pending map[uint64]*pending
	Stats   Stats

	// caretaker state (repair loop)
	reg           ports.Registry
	care          []ports.Hash
	repairRunning bool

	// ledger, when set, is credited for every chunk this node serves.
	ledger ports.CreditLedger
	// freeload makes the node a pure consumer: it refuses to store
	// pushed chunks and refuses to serve fetches, while still fetching
	// and using DHT routing. Exists so the economy scenario can watch
	// leeches go broke.
	freeload bool
	// liar makes the node accept chunk placements, keep the PROOF, and
	// throw away the DATA — then claim to have the chunk when asked.
	// It can still answer a challenge with a valid Merkle proof (it
	// kept that), but it cannot compute the nonce tag, which is the
	// whole point of the tag. Exists so audits have someone to catch.
	liar bool
	// proofs holds the storage proof for each chunk this node hosts.
	proofs map[ports.ChunkID]ports.StorageProof
}

// SetLedger wires credit accounting; nil disables it.
func (n *Node) SetLedger(l ports.CreditLedger) { n.ledger = l }

// SetFreeload toggles leech behavior.
func (n *Node) SetFreeload(v bool) { n.freeload = v }

// SetLiar toggles fake-storage behavior (see the liar field).
func (n *Node) SetLiar(v bool) { n.liar = v }

func New(id ports.NodeID, cfg Config, clock ports.Clock, tr ports.Transport, store ports.ChunkStore) *Node {
	n := &Node{
		cfg:     cfg,
		id:      id,
		clock:   clock,
		tr:      tr,
		store:   store,
		table:   dht.NewTable(id, cfg.K),
		provs:   dht.NewProviders(),
		pending: make(map[uint64]*pending),
		proofs:  make(map[ports.ChunkID]ports.StorageProof),
	}
	tr.SetHandler(n.handle)
	return n
}

func (n *Node) ID() ports.NodeID        { return n.id }
func (n *Node) Table() *dht.Table       { return n.table }
func (n *Node) Store() ports.ChunkStore { return n.store }

// bg is the context for local store calls. The event loop has no
// cancellation semantics, so a background context is honest.
func bg() context.Context { return context.Background() }

// request sends msg expecting a reply; cb fires exactly once, with
// ErrTimeout if none arrives in time. Timeouts also evict the peer from
// the routing table — a Kademlia table must only hold live peers.
func (n *Node) request(to ports.NodeID, msg ports.Message, cb func(ports.Message, error)) {
	n.rid++
	rid := n.rid
	msg.RID = rid
	p := &pending{cb: cb, to: to}
	p.cancel = n.clock.AfterFunc(n.cfg.RequestTimeout, func() {
		delete(n.pending, rid)
		n.Stats.Timeouts++
		n.table.Remove(to)
		cb(ports.Message{}, fmt.Errorf("%w (to %s)", ErrTimeout, to))
	})
	n.pending[rid] = p
	n.Stats.QueriesSent++
	if err := n.tr.Send(to, msg); err != nil {
		p.cancel()
		delete(n.pending, rid)
		cb(ports.Message{}, err)
	}
}

// handle is the single entry point for every incoming message.
func (n *Node) handle(from ports.NodeID, msg ports.Message) {
	n.table.Observe(from) // any message is proof of life

	if msg.IsReply() {
		p, ok := n.pending[msg.RID]
		if !ok || p.to != from {
			return // late, duplicate, or forged reply: drop
		}
		delete(n.pending, msg.RID)
		p.cancel()
		p.cb(msg, nil)
		return
	}

	switch msg.Kind {
	case ports.MsgFindNode:
		n.reply(from, msg, ports.Message{
			Kind:  ports.MsgFindNodeReply,
			Nodes: n.closestExcluding(msg.Target, from),
		})
	case ports.MsgGetProviders:
		n.reply(from, msg, ports.Message{
			Kind:      ports.MsgGetProvidersReply,
			Providers: n.provs.Get(msg.Target),
			Nodes:     n.closestExcluding(msg.Target, from),
		})
	case ports.MsgAddProvider:
		// The announcer vouches for itself; fetchers verify hashes, so
		// false claims waste time but can't corrupt anything.
		n.provs.Add(msg.Target, from)
		n.reply(from, msg, ports.Message{Kind: ports.MsgAddProviderAck, OK: true})
	case ports.MsgStoreChunk:
		if n.freeload {
			n.reply(from, msg, ports.Message{Kind: ports.MsgStoreChunkAck, OK: false})
			return
		}
		c := ports.Chunk{ID: msg.ChunkID, Data: msg.Data}
		ok := c.Verify() // never store what doesn't hash right
		if ok && msg.Proof != nil && !verifyStorageProof(*msg.Proof, msg.ChunkID) {
			ok = false // refuse chunks with proofs we couldn't defend under audit
		}
		if ok && n.liar {
			// Keep the receipt, ditch the goods.
			if msg.Proof != nil {
				n.proofs[msg.ChunkID] = copyProof(*msg.Proof)
			}
			n.provs.Add(msg.ChunkID, n.id)
			n.reply(from, msg, ports.Message{Kind: ports.MsgStoreChunkAck, OK: true})
			return
		}
		if ok {
			if err := n.store.Put(bg(), c); err != nil {
				ok = false
			} else {
				n.Stats.ChunksReceived++
				n.provs.Add(msg.ChunkID, n.id) // we are now a provider
				if msg.Proof != nil {
					n.proofs[msg.ChunkID] = copyProof(*msg.Proof)
				}
			}
		}
		n.reply(from, msg, ports.Message{Kind: ports.MsgStoreChunkAck, OK: ok})
	case ports.MsgFetchChunk:
		if n.freeload {
			n.reply(from, msg, ports.Message{Kind: ports.MsgFetchChunkReply, Found: false})
			return
		}
		c, err := n.store.Get(bg(), msg.ChunkID)
		if err != nil {
			n.reply(from, msg, ports.Message{Kind: ports.MsgFetchChunkReply, Found: false})
			return
		}
		n.Stats.ChunksServed++
		n.Stats.BytesServed += int64(len(c.Data))
		if n.ledger != nil {
			n.ledger.RecordServe(n.id, from, msg.ChunkID, int64(len(c.Data)))
		}
		n.reply(from, msg, ports.Message{Kind: ports.MsgFetchChunkReply, Found: true, Data: c.Data})
	case ports.MsgHasChunk:
		ok, _ := n.store.Has(bg(), msg.ChunkID)
		if n.liar {
			_, ok = n.proofs[msg.ChunkID] // "of course I have it"
		}
		n.reply(from, msg, ports.Message{Kind: ports.MsgHasChunkReply, Found: ok})
	case ports.MsgChallenge:
		n.reply(from, msg, n.answerChallenge(msg))
	}
}

func (n *Node) reply(to ports.NodeID, req ports.Message, resp ports.Message) {
	resp.RID = req.RID
	n.tr.Send(to, resp)
}

func (n *Node) closestExcluding(target ports.Hash, exclude ports.NodeID) []ports.NodeID {
	out := make([]ports.NodeID, 0, n.cfg.K)
	for _, id := range n.table.Closest(target, n.cfg.K+1) {
		if id != exclude {
			out = append(out, id)
			if len(out) == n.cfg.K {
				break
			}
		}
	}
	return out
}

// Bootstrap introduces the node to the network via seed peers, then
// looks up its own ID — the standard Kademlia join, which populates the
// buckets closest to self (the ones that matter most).
func (n *Node) Bootstrap(seeds []ports.NodeID, done func()) {
	for _, s := range seeds {
		n.table.Observe(s)
	}
	n.IterativeFindNode(n.id, func([]ports.NodeID) { done() })
}

// IterativeFindNode drives the pure dht.Lookup over the wire and calls
// done with the k closest live nodes to target.
func (n *Node) IterativeFindNode(target ports.Hash, done func([]ports.NodeID)) {
	n.newWalk(ports.MsgFindNode, target, nil, done).step()
}

// walk drives one dht.Lookup over the transport. finished guards
// against late replies re-entering a completed walk (every callback may
// fire after the walk has already converged or been aborted).
type walk struct {
	n           *Node
	l           *dht.Lookup
	kind        ports.MsgKind
	target      ports.Hash
	onProviders func([]ports.NodeID) bool // optional; true = stop the walk
	done        func([]ports.NodeID)      // fires at most once
	finished    bool
}

func (n *Node) newWalk(kind ports.MsgKind, target ports.Hash,
	onProviders func([]ports.NodeID) bool, done func([]ports.NodeID)) *walk {
	return &walk{
		n:           n,
		l:           dht.NewLookup(target, n.cfg.K, n.cfg.Alpha, n.table.Closest(target, n.cfg.K)),
		kind:        kind,
		target:      target,
		onProviders: onProviders,
		done:        done,
	}
}

func (w *walk) step() {
	if w.finished {
		return
	}
	if w.l.Done() {
		w.finished = true
		w.done(w.l.Result())
		return
	}
	for _, peer := range w.l.NextQueries() {
		peer := peer
		w.n.request(peer, ports.Message{Kind: w.kind, Target: w.target}, func(resp ports.Message, err error) {
			if w.finished {
				return
			}
			if err != nil {
				w.l.OnFailure(peer)
			} else {
				w.l.OnReply(peer, resp.Nodes)
				for _, id := range resp.Nodes {
					w.n.table.Observe(id)
				}
				if w.onProviders != nil && len(resp.Providers) > 0 && w.onProviders(resp.Providers) {
					w.finished = true // caller has what it needs
					return
				}
			}
			w.step()
		})
	}
}
