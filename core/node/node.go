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
	"crypto/ed25519"
	"crypto/rsa"
	"errors"
	"fmt"

	"github.com/nerolabs/silt/core/blindtoken"
	"github.com/nerolabs/silt/core/bond"
	"github.com/nerolabs/silt/core/chain"
	"github.com/nerolabs/silt/core/denylist"
	"github.com/nerolabs/silt/core/dht"
	"github.com/nerolabs/silt/core/link"
	"github.com/nerolabs/silt/ports"
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
	// Domain is the operator's failure-domain label (AS / rack / geo /
	// operator). Placement spreads a file's columns across distinct
	// domains, so a whole domain failing costs a stripe as little as
	// possible. Empty = unset (each such node is its own unique domain).
	Domain string
	// Demand-responsive dispersion: a node that serves a chunk more than
	// HotThreshold times within a DemandInterval pushes FanoutReplicas
	// extra cache copies to other hosts (leased, so they expire after
	// LeaseTTL of not being read). Baseline Replication is the floor; heat
	// a temporary multiplier. HotThreshold 0 disables the whole mechanism.
	HotThreshold   int
	DemandInterval ports.Duration
	LeaseTTL       ports.Duration
	FanoutReplicas int
	// ReachabilityTimeout bounds a reachability check: how long to wait for
	// a helper's dial-back before concluding this node is behind NAT. It
	// spans a fresh outbound dial + handshake on the helper's side, so it is
	// deliberately looser than RequestTimeout.
	ReachabilityTimeout ports.Duration
	// FetchAttempts is how many times a chunk fetch re-sweeps its providers
	// when every provider failed *transiently* (a timeout or a relay-at-
	// capacity refusal, not a clean "don't have it"): the post-cap relay path
	// saturates under concurrent fan-out (#65) and its freed slots make a
	// backed-off retry succeed. FetchBackoff is the base delay, grown
	// linearly per attempt. FetchAttempts <= 1 disables the retry.
	FetchAttempts int
	FetchBackoff  ports.Duration
	// BondAuditInterval is how often a validator challenges the storage
	// bonds of the validators it knows, and BondMaxAge is how long a bond
	// may go un-re-proven before its standing decays — so consensus
	// standing must be backed by *sustained* held storage (T1b, #78).
	BondAuditInterval ports.Duration
	BondMaxAge        ports.Duration
	// BondVDFDelay is the number of sequential squarings a bond proof must
	// bind (core/vdf) — the "time" in proof-of-space-time: a prover cannot
	// answer until it has done this much non-parallelisable work over the
	// fresh challenge, so it cannot have released the pledged space and
	// re-plotted just-in-time. A tuning knob (Evolving): raise it in a real
	// deployment for a stronger elapsed-time floor; the modest default keeps
	// the deterministic sim fast. 0 disables the time binding (space-only).
	BondVDFDelay uint64
}

func DefaultConfig() Config {
	return Config{
		K: 8, Alpha: 3,
		RequestTimeout: 500 * ports.Millisecond,
		Replication:    3,
		RepairInterval: 60 * ports.Second,
		RepairSlack:    2,
		HotThreshold:   8,
		DemandInterval: 60 * ports.Second,
		LeaseTTL:       180 * ports.Second,
		FanoutReplicas: 2,

		ReachabilityTimeout: 3 * ports.Second,
		FetchAttempts:       3,
		FetchBackoff:        200 * ports.Millisecond,

		BondAuditInterval: 60 * ports.Second,
		BondMaxAge:        300 * ports.Second, // ~5 audit intervals unproven → standing lapses
		BondVDFDelay:      1000,               // modest; a real deployment raises it for a stronger time floor
	}
}

var ErrTimeout = errors.New("node: request timed out")

// Stats are per-node counters the sim reports on.
type Stats struct {
	QueriesSent     int // DHT + fetch requests issued
	Timeouts        int
	ChunksServed    int
	ChunksReceived  int // chunks pushed to us via StoreChunk
	BytesServed     int64
	Probes          int // shard availability checks (repair loop)
	Repairs         int // stripes repaired
	ShardsRebuilt   int // shards reconstructed and re-distributed
	RepairFailures  int // repair attempts that couldn't reconstruct (retried next sweep)
	Dispersals      int // stripes re-spread by the dispersion audit (over-concentrated in a domain)
	BlocksCommitted int // chain blocks appended to the local replica
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

	// reachable tracks peers that have answered one of our requests and
	// not since timed out — proof WE can dial THEM, not merely that they
	// reached us. Only these are persisted as warm-restart seeds, so a
	// restart re-seeds from live peers instead of reloading every dead
	// ephemeral identity we ever heard from (#43).
	reachable map[ports.NodeID]ports.Time

	// reachability probes (our AutoNAT): a check sends helpers a nonce and
	// waits for one to dial us back. reachProbes maps an outstanding nonce
	// to its callback; reachSeq mints nonces; reach is the last verdict.
	reachSeq    uint64
	reachProbes map[uint64]*reachProbe
	reach       Reachability
	// observedAddr is this node's public host:port as a relay reported it
	// (STUN-style, #27) — the endpoint a peer aims a hole-punch at. "" until
	// a relay registration reports one.
	observedAddr string

	// caretaker state (repair loop)
	reg           ports.Registry
	care          []link.CareHandle
	repairRunning bool

	// ledger, when set, is credited for every chunk this node serves.
	ledger ports.CreditLedger
	// freeload makes the node a pure consumer: it refuses to store
	// pushed chunks and refuses to serve fetches, while still fetching
	// and using DHT routing. Exists so the economy scenario can watch
	// leeches go broke.
	freeload bool
	// ephemeral marks this node as a short-lived client (publish/fetch that
	// keeps nothing): its outgoing messages are stamped so peers don't route
	// to it. See ports.Message.Ephemeral (#43).
	ephemeral bool
	// liar makes the node accept chunk placements, keep the PROOF, and
	// throw away the DATA — then claim to have the chunk when asked.
	// It can still answer a challenge with a valid Merkle proof (it
	// kept that), but it cannot compute the nonce tag, which is the
	// whole point of the tag. Exists so audits have someone to catch.
	liar bool
	// proofs holds the storage proof for each chunk this node hosts. It is
	// mirrored to proofStore (when set) so a restart can re-announce coded
	// shards under the right column key and still answer audits (#69).
	proofs     map[ports.ChunkID]ports.StorageProof
	proofStore ports.ProofStore // nil = memory-only (sims, ephemeral clients)

	// capacity gossip: capRep is the store's reporter (nil if the store
	// is unbounded); peerCaps accumulates what peers report about
	// themselves, the raw material of the network capacity estimate.
	capRep   ports.CapacityReporter
	peerCaps map[ports.NodeID]capInfo

	// bond audit (T1b, #78): bond is this node's own sealed storage bond
	// (nil = none advertised); peerBonds accumulates the bond roots/sizes
	// peers gossip, which a validator challenges to make consensus standing
	// cost real held storage. See bondaudit.go.
	bond      *bond.Commitment
	peerBonds map[ports.NodeID]bondInfo
	// plotStore persists the bond plot so a restart reloads it instead of
	// re-plotting (#93); nil = memory-only (re-plots each start).
	plotStore ports.PlotStore

	// tokenIssuer, when set, makes this validator blind-sign publish-token
	// requests (T3, #14/F1) — the publisher-privacy issuance role.
	tokenIssuer *blindtoken.Issuer
	// issuer-key distribution: our own issuer pubkey (DER, served on
	// MsgGetIssuerKey) and a cache of peers' issuer keys (fetched), so tokens
	// can be blinded against and verified across the network.
	issuerKeyDER   []byte
	peerIssuerKeys map[ports.NodeID]*rsa.PublicKey

	// failure-domain gossip: domainID is this node's own domain hash
	// (0 = unset); peerDomains accumulates peers' domains from gossip, so
	// placement can spread columns across distinct domains.
	domainID    uint64
	peerDomains map[ports.NodeID]uint64

	// demand-responsive dispersion: serveLoad counts recent serves per
	// chunk (decayed each demand tick); leases holds the expiry of each
	// cache copy we took on under load; demandRunning guards the tick loop,
	// which sleeps itself when there's nothing hot or leased to manage.
	serveLoad     map[ports.ChunkID]int
	leases        map[ports.ChunkID]ports.Time
	demandRunning bool

	// validator role (M12): the local chain replica and the signing key
	// (nil = not a validator).
	chain    *chain.Chain
	signer   ed25519.PrivateKey
	onCommit func(chain.Block)
	// attested records the block hash this validator has signed at each height,
	// so it NEVER signs a second, different block at a height it already
	// attested — an honest validator does not equivocate, even if two competing
	// proposals reach it before either commits (D2). See chainrole.go.
	attested map[uint64]ports.Hash
	// denylist is the operator's local takedown list (nil = none). The
	// effective set also includes on-chain revocations; see isDenied.
	denylist *denylist.Set

	// lg narrates through the observability port; nil = off. The sim
	// leaves it nil, so determinism and benchmarks are untouched.
	lg ports.Logger
}

type capInfo struct{ used, total int64 }

// SetLedger wires credit accounting; nil disables it.
func (n *Node) SetLedger(l ports.CreditLedger) { n.ledger = l }

// SetLogger wires the observability port; nil disables it.
func (n *Node) SetLogger(lg ports.Logger) { n.lg = lg }

// logf narrates. Only rare events go through it (timeouts, repairs,
// verdicts) — nothing per-byte, so the varargs cost is irrelevant and a
// disabled logger stays effectively free.
func (n *Node) logf(lvl ports.LogLevel, event string, kv ...any) {
	ports.LogIf(n.lg, lvl, event, kv...)
}

// SetFreeload toggles leech behavior.
func (n *Node) SetFreeload(v bool) { n.freeload = v }

// SetEphemeral marks this node as a short-lived client so peers don't add
// it to their routing tables (#43).
func (n *Node) SetEphemeral(v bool) { n.ephemeral = v }

// SetLiar toggles fake-storage behavior (see the liar field).
func (n *Node) SetLiar(v bool) { n.liar = v }

// SetObservedAddr records this node's public host:port as a relay reported it
// (#27); ObservedAddr returns it ("" until known). A NATed node hands this to a
// peer as the endpoint to aim a hole-punch at.
func (n *Node) SetObservedAddr(a string) { n.observedAddr = a }

// ObservedAddr is this node's relay-observed public endpoint ("" if unknown).
func (n *Node) ObservedAddr() string { return n.observedAddr }

// SetProofStore attaches durable proof persistence (#69); call before
// LoadProofs and bootstrap. nil keeps proofs memory-only (sims, clients).
func (n *Node) SetProofStore(ps ports.ProofStore) { n.proofStore = ps }

// SetPlotStore attaches durable bond-plot persistence (#93); call before
// EnableBond so a restart reloads the plot instead of re-plotting. nil keeps
// the plot memory-only (re-plotted each start; fine for sims/tests).
func (n *Node) SetPlotStore(ps ports.PlotStore) { n.plotStore = ps }

// LoadProofs repopulates the in-memory proof map from the proof store, so a
// restarted node re-announces its held coded shards under the correct column
// key (AnnounceHeld reads n.proofs) instead of their bare ids — the
// difference between a disk full of content being discoverable or invisible
// (#69). No-op without a proof store. Call after New, before AnnounceHeld.
func (n *Node) LoadProofs() {
	if n.proofStore == nil {
		return
	}
	m, err := n.proofStore.Load()
	if err != nil {
		n.logf(ports.LogWarn, "proof reload failed", "err", err)
	}
	for id, p := range m {
		n.proofs[id] = p
	}
	if len(m) > 0 {
		n.logf(ports.LogInfo, "reloaded storage proofs", "count", len(m))
	}
}

// dropHosted removes a chunk this node hosts, keeping the proof map and store
// in sync so a delete never leaves an orphan proof behind.
func (n *Node) dropHosted(id ports.ChunkID) {
	n.store.Delete(bg(), id)
	delete(n.proofs, id)
	if n.proofStore != nil {
		n.proofStore.Delete(id)
	}
}

func New(id ports.NodeID, cfg Config, clock ports.Clock, tr ports.Transport, store ports.ChunkStore) *Node {
	n := &Node{
		cfg:            cfg,
		id:             id,
		clock:          clock,
		tr:             tr,
		store:          store,
		table:          dht.NewTable(id, cfg.K),
		provs:          dht.NewProviders(),
		pending:        make(map[uint64]*pending),
		reachable:      make(map[ports.NodeID]ports.Time),
		reachProbes:    make(map[uint64]*reachProbe),
		proofs:         make(map[ports.ChunkID]ports.StorageProof),
		peerDomains:    make(map[ports.NodeID]uint64),
		peerBonds:      make(map[ports.NodeID]bondInfo),
		attested:       make(map[uint64]ports.Hash),
		peerIssuerKeys: make(map[ports.NodeID]*rsa.PublicKey),
		serveLoad:      make(map[ports.ChunkID]int),
		leases:         make(map[ports.ChunkID]ports.Time),
	}
	if cfg.Domain != "" {
		n.domainID = domainHash(cfg.Domain)
	}
	if rep, ok := store.(ports.CapacityReporter); ok {
		n.capRep = rep
		n.peerCaps = make(map[ports.NodeID]capInfo)
	} else {
		n.peerCaps = make(map[ports.NodeID]capInfo)
	}
	tr.SetHandler(n.handle)
	return n
}

// send stamps outgoing messages with our current capacity pledge — the
// gossip that lets every node estimate the network's total storage.
func (n *Node) send(to ports.NodeID, msg ports.Message) error {
	if n.capRep != nil {
		msg.CapUsed, msg.CapTotal = n.capRep.Capacity()
	}
	msg.Domain = n.domainID
	msg.Ephemeral = n.ephemeral
	if n.bond != nil {
		msg.BondRoot, msg.BondSize = n.bond.Root, n.bond.Size
	}
	return n.tr.Send(to, msg)
}

func (n *Node) ID() ports.NodeID        { return n.id }
func (n *Node) Table() *dht.Table       { return n.table }
func (n *Node) Store() ports.ChunkStore { return n.store }

// ReachablePeers reports the peers we've had a successful round-trip with
// and haven't since timed out — the set worth persisting as warm-restart
// seeds, as opposed to every address we've ever observed. See the
// reachable field (#43).
func (n *Node) ReachablePeers() map[ports.NodeID]bool {
	out := make(map[ports.NodeID]bool, len(n.reachable))
	for id := range n.reachable {
		out[id] = true
	}
	return out
}

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
		delete(n.reachable, to) // no longer proven reachable (#43)
		n.logf(ports.LogDebug, "request timeout", "to", to, "kind", msg.Kind)
		cb(ports.Message{}, fmt.Errorf("%w (to %s)", ErrTimeout, to))
	})
	n.pending[rid] = p
	n.Stats.QueriesSent++
	if err := n.send(to, msg); err != nil {
		p.cancel()
		delete(n.pending, rid)
		cb(ports.Message{}, err)
	}
}

// handle is the single entry point for every incoming message.
func (n *Node) handle(from ports.NodeID, msg ports.Message) {
	// Any message is proof of life — but a short-lived client (publish/fetch
	// that keeps nothing) will vanish, so routing to it only poisons the
	// table with ghosts; process its message, but don't add it (#43).
	if !msg.Ephemeral {
		n.table.Observe(from)
	}
	if msg.CapTotal > 0 {
		n.peerCaps[from] = capInfo{used: msg.CapUsed, total: msg.CapTotal}
	}
	if msg.Domain != 0 {
		n.peerDomains[from] = msg.Domain
	}
	if msg.BondRoot != (ports.Hash{}) && !msg.Ephemeral {
		n.peerBonds[from] = bondInfo{root: msg.BondRoot, size: msg.BondSize}
	}

	if msg.IsReply() {
		p, ok := n.pending[msg.RID]
		if !ok || p.to != from {
			return // late, duplicate, or forged reply: drop
		}
		delete(n.pending, msg.RID)
		p.cancel()
		n.reachable[from] = n.clock.Now() // a reply proves we can dial them (#43)
		p.cb(msg, nil)
		return
	}

	if n.handleChain(from, msg) {
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
		if n.freeload || (msg.Proof != nil && n.isDenied(msg.Proof.Root)) {
			n.reply(from, msg, ports.Message{Kind: ports.MsgStoreChunkAck, OK: false})
			return
		}
		c := ports.Chunk{ID: msg.ChunkID, Data: msg.Data}
		// Providers register under the placement key — the column key for a
		// coded shard, the chunk's own id otherwise — so a reader walking to
		// that key finds the whole column here.
		key := ports.Hash(msg.ChunkID)
		if msg.Proof != nil {
			key = placementKey(msg.Proof.Root, msg.ChunkID, msg.Proof.Column)
		}
		ok := c.Verify() // never store what doesn't hash right
		if ok && msg.Proof != nil && !verifyStorageProof(*msg.Proof, msg.ChunkID) {
			ok = false // refuse chunks with proofs we couldn't defend under audit
		}
		if ok && n.liar {
			// Keep the receipt, ditch the goods.
			if msg.Proof != nil {
				n.proofs[msg.ChunkID] = copyProof(*msg.Proof)
			}
			n.provs.Add(key, n.id)
			n.reply(from, msg, ports.Message{Kind: ports.MsgStoreChunkAck, OK: true})
			return
		}
		if ok {
			if err := n.store.Put(bg(), c); err != nil {
				n.logf(ports.LogWarn, "store put failed", "chunk", msg.ChunkID, "err", err)
				ok = false
			} else {
				n.Stats.ChunksReceived++
				n.provs.Add(key, n.id) // we are now a provider
				if msg.Proof != nil {
					n.proofs[msg.ChunkID] = copyProof(*msg.Proof)
					if n.proofStore != nil { // persist so a restart re-announces under the right key (#69)
						if err := n.proofStore.Put(msg.ChunkID, *msg.Proof); err != nil {
							n.logf(ports.LogWarn, "proof persist failed", "chunk", msg.ChunkID, "err", err)
						}
					}
				}
				if msg.Lease {
					n.takeLease(msg.ChunkID) // demand-driven cache copy: hold, but let it expire
				}
			}
		}
		n.reply(from, msg, ports.Message{Kind: ports.MsgStoreChunkAck, OK: ok})
	case ports.MsgFetchChunk:
		if n.freeload || n.chunkDenied(msg.ChunkID) {
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
		n.bumpDemand(msg.ChunkID)
		n.reply(from, msg, ports.Message{Kind: ports.MsgFetchChunkReply, Found: true, Data: c.Data})
	case ports.MsgHasChunk:
		ok, _ := n.store.Has(bg(), msg.ChunkID)
		if n.liar {
			_, ok = n.proofs[msg.ChunkID] // "of course I have it"
		}
		if n.chunkDenied(msg.ChunkID) {
			ok = false // taken down: no longer available here
		}
		n.reply(from, msg, ports.Message{Kind: ports.MsgHasChunkReply, Found: ok})
	case ports.MsgChallenge:
		if n.chunkDenied(msg.ChunkID) {
			n.reply(from, msg, ports.Message{Kind: ports.MsgChallengeReply, Found: false})
			return
		}
		n.reply(from, msg, n.answerChallenge(msg))
	case ports.MsgBondChallenge:
		n.reply(from, msg, n.answerBondChallenge(msg))
	case ports.MsgTokenRequest:
		n.reply(from, msg, n.answerTokenRequest(from, msg))
	case ports.MsgGetIssuerKey:
		n.reply(from, msg, n.answerIssuerKey())
	case ports.MsgCheckReachability:
		// A peer wants to know if it is publicly reachable. Answering means
		// dialing it back at its advertised address: if the reply lands, the
		// dial succeeded, which is itself the proof. We echo the nonce so the
		// asker can match the answer to its outstanding check.
		n.send(from, ports.Message{Kind: ports.MsgReachabilityReply, Nonce: msg.Nonce})
	case ports.MsgReachabilityReply:
		// A helper reached us back — we are reachable. Resolve the probe;
		// the timeout handles the silent (NATed) case.
		if p, ok := n.reachProbes[msg.Nonce]; ok {
			delete(n.reachProbes, msg.Nonce)
			p.cancel()
			p.done(true)
		}
	}
}

func (n *Node) reply(to ports.NodeID, req ports.Message, resp ports.Message) {
	resp.RID = req.RID
	n.send(to, resp)
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
