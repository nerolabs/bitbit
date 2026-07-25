// Network-facing ports: node identity, simulated time, transport, and
// the wire message vocabulary.
//
// Design note on determinism: the whole sim runs on ONE event loop.
// Nodes never block and never spawn goroutines; instead of Clock.After
// returning a channel (which invites blocking reads and scheduler
// nondeterminism), the port is AfterFunc — schedule a callback, get a
// cancel function. A wall-clock adapter can trivially implement this
// with time.AfterFunc later; the sim implements it with a priority
// queue, which is what makes same-seed runs byte-identical.
package ports

// NodeID identifies a node. Same 256-bit space as chunk hashes, which
// is exactly what Kademlia wants: nodes and content live in one metric
// space, and a chunk is stored/announced "near" the nodes whose IDs are
// closest to its hash.
type NodeID = Hash

// Time is nanoseconds since the sim epoch; Duration is a span of them.
// Deliberately not time.Time — core code must not touch the wall clock,
// and an int64 makes "the clock is just a number the scheduler owns"
// impossible to get wrong.
type Time int64

type Duration int64

const (
	Millisecond Duration = 1_000_000
	Second      Duration = 1000 * Millisecond
)

func (t Time) Add(d Duration) Time { return t + Time(d) }

// Clock is injected everywhere core logic needs "later".
type Clock interface {
	Now() Time
	// AfterFunc schedules fn after d; the returned cancel is idempotent
	// and a no-op once fn has run.
	AfterFunc(d Duration, fn func()) (cancel func())
}

// MsgKind discriminates the wire messages. One flat message struct with
// a kind tag keeps the sim transport trivially copyable and keeps the
// future serialization story simple.
type MsgKind uint8

const (
	MsgFindNode          MsgKind = iota + 1 // Target: ID to search near
	MsgFindNodeReply                        // Nodes: up to k closer peers
	MsgGetProviders                         // Target: chunk hash
	MsgGetProvidersReply                    // Providers + Nodes (closer peers)
	MsgAddProvider                          // Target: chunk hash; sender announces itself
	MsgAddProviderAck
	MsgStoreChunk // ChunkID + Data: push a chunk to a peer
	MsgStoreChunkAck
	MsgFetchChunk      // ChunkID
	MsgFetchChunkReply // Found + Data
	MsgHasChunk        // ChunkID: cheap availability probe (repair loop)
	MsgHasChunkReply   // Found
	MsgChallenge       // ChunkID + Nonce: prove you hold this shard of Proof.Root
	MsgChallengeReply  // Found + Proof + Tag
	MsgProposeBlock    // Data: CBOR block awaiting attestation
	MsgAttestReply     // OK + Data: CBOR attestation (or OK=false refusal)
	MsgCommitBlock     // Data: CBOR block with quorum attached
	MsgCommitAck       // OK
	MsgGetChain        // Height: send me blocks from here up
	MsgChainReply      // Data: CBOR []Block
)

// StorageProof is a Merkle inclusion proof shipped alongside a chunk:
// "this chunk is leaf Index of the Total shards under Root". It travels
// with the chunk at store time (a host must be able to prove what it
// holds) and comes back in challenge replies. Path is bottom-up sibling
// hashes, exactly core/manifest's proof shape.
type StorageProof struct {
	Root  Hash
	Index int
	Total int
	Path  []Hash
	// Column is the shard's position within its stripe (0..n-1) for an
	// erasure-coded file — the placement/provider key is hash(root‖Column)
	// so a whole column co-locates and is discovered together. -1 means
	// "no column" (manifest chunks and uncoded files), keyed by chunk id.
	Column int
}

// Message is the single wire envelope. RID correlates requests with
// replies. Fields are used per-kind; unused fields stay zero.
type Message struct {
	Kind      MsgKind
	RID       uint64
	Target    Hash
	Nodes     []NodeID
	Providers []NodeID
	ChunkID   ChunkID
	Data      []byte
	Found     bool
	OK        bool
	// Proof-of-retrieval fields: Proof rides on StoreChunk (so hosts
	// can later prove possession) and ChallengeReply; Nonce freshens a
	// challenge; Tag is the prover's SHA-256(nonce ‖ chunk bytes).
	Proof *StorageProof
	Nonce uint64
	Tag   []byte
	// Capacity gossip: every message from a capacity-pledging node
	// carries its current used/total, so peers accumulate a sample of
	// the network's storage for the M9 capacity estimate.
	CapUsed  int64
	CapTotal int64
	// Domain is a hash of the operator's failure-domain label (AS / rack /
	// geo / operator), gossiped so placement can spread a file's columns
	// across distinct domains, not just node IDs. 0 means "unset" — treated
	// as its own unique domain, never a grouping constraint.
	Domain uint64
	// Lease marks a StoreChunk as a demand-driven cache copy: the receiver
	// holds and serves it but lets it expire if it stops being read, so a
	// hot file fans out under load and contracts on cooldown.
	Lease bool
	// Height is the chain-sync cursor (MsgGetChain).
	Height uint64
}

// IsReply reports whether this kind terminates a pending request.
func (m Message) IsReply() bool {
	switch m.Kind {
	case MsgFindNodeReply, MsgGetProvidersReply, MsgAddProviderAck, MsgStoreChunkAck, MsgFetchChunkReply, MsgHasChunkReply, MsgChallengeReply, MsgAttestReply, MsgCommitAck, MsgChainReply:
		return true
	}
	return false
}

// Transport is node↔node messaging. Handlers are invoked one at a time
// (the sim guarantees single-threaded delivery); a handler must not
// block.
type Transport interface {
	Send(to NodeID, msg Message) error
	SetHandler(func(from NodeID, msg Message))
}
