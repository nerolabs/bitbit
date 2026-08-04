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

import "fmt"

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
	MsgFetchChunk        // ChunkID
	MsgFetchChunkReply   // Found + Data
	MsgHasChunk          // ChunkID: cheap availability probe (repair loop)
	MsgHasChunkReply     // Found
	MsgChallenge         // ChunkID + PorSeed/PorCount: prove you hold this shard of Proof.Root
	MsgChallengeReply    // Found + Proof + PoR proof (PorMu/PorSigma/PorBlocks)
	MsgProposeBlock      // Data: CBOR block awaiting attestation
	MsgAttestReply       // OK + Data: CBOR attestation (or OK=false refusal)
	MsgCommitBlock       // Data: CBOR block with quorum attached
	MsgCommitAck         // OK
	MsgGetChain          // Height: send me blocks from here up
	MsgChainReply        // Data: CBOR []Block
	MsgCheckReachability // Nonce: "dial me back at my advertised address"
	MsgReachabilityReply // Nonce: the dial-back landed (its arrival is the proof)
	MsgBondChallenge     // Nonce: prove you still hold the storage bond you advertised
	MsgBondReply         // Data: CBOR bond.Answer (empty if the bond isn't held)
	MsgTokenRequest      // Data: a blinded publish-token serial to blind-sign (fee charged to sender)
	MsgTokenReply        // Data: the blind signature; OK=false if refused
	MsgGetIssuerKey      // ask a validator for its publish-token issuer public key
	MsgIssuerKeyReply    // Data: the issuer public key (blindtoken.MarshalPub); OK=false if none
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
	// PorTags are the per-block proof-of-retrievability authenticators for
	// this shard (core/por), computed by the publisher under a key derived
	// from the file's layout key. They travel with the chunk at store time
	// and are kept by the host (like the Merkle Path) so it can later PROVE
	// possession without the auditor fetching the bytes. One 32-byte tag per
	// por-block; len(PorTags) is the shard's block count. Nil for chunks
	// shipped without PoR (e.g. manifest chunks, which audits don't sample).
	PorTags [][]byte
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
	// Proof-of-retrieval fields: Proof (the Merkle inclusion proof, now
	// carrying PorTags) rides on StoreChunk so hosts can later prove
	// possession, and comes back on ChallengeReply. Nonce freshens a bond
	// challenge (MsgBondChallenge).
	Proof *StorageProof
	Nonce uint64
	// PoR challenge (MsgChallenge → auditor): PorSeed expands to the sampled
	// block indices and coefficients; PorCount is how many blocks to sample.
	// The block count itself is len(the host's stored PorTags), echoed back
	// as PorBlocks so the auditor can reconstruct the identical challenge.
	PorSeed  []byte
	PorCount int
	// PoR proof (MsgChallengeReply → prover): the aggregated response.
	// PorMu is one field element per sector, PorSigma the aggregated tag,
	// PorBlocks the prover's block count. These are core/por wire bytes; the
	// ports layer stays crypto-agnostic and never imports core/por.
	PorMu     [][]byte
	PorSigma  []byte
	PorBlocks int
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
	// Ephemeral marks a message from a short-lived client (a `swarm add` /
	// `swarm get` publisher or fetcher that keeps nothing and then dies).
	// Receivers must NOT add such a sender to their routing table: routing
	// to a peer that will vanish poisons the table with ghosts and drowns
	// lookups in timeouts (#43).
	Ephemeral bool
	// Height is the chain-sync cursor (MsgGetChain).
	Height uint64
	// Bond gossip: a validator advertises its storage-bond commitment root
	// and size on every message (like capacity/domain), so peers can
	// challenge it to prove it still holds real, identity-bound storage —
	// the Sybil cost behind consensus standing. A zero BondRoot means the
	// node advertises no bond and is never bond-challenged.
	BondRoot Hash
	BondSize int64
	// Credit optionally rides a MsgTokenRequest: a prepaid publish credit the
	// issuer verifies and SPENDS instead of charging the requester's durable
	// identity (M0 privacy D3 / F4). Nil ⇒ the legacy fee-at-request path. Only
	// honored when the issuer requires credits (Node.RequirePublishCredits).
	Credit *PublishCredit
}

func (k MsgKind) String() string {
	names := [...]string{
		MsgFindNode: "FindNode", MsgFindNodeReply: "FindNodeReply",
		MsgGetProviders: "GetProviders", MsgGetProvidersReply: "GetProvidersReply",
		MsgAddProvider: "AddProvider", MsgAddProviderAck: "AddProviderAck",
		MsgStoreChunk: "StoreChunk", MsgStoreChunkAck: "StoreChunkAck",
		MsgFetchChunk: "FetchChunk", MsgFetchChunkReply: "FetchChunkReply",
		MsgHasChunk: "HasChunk", MsgHasChunkReply: "HasChunkReply",
		MsgChallenge: "Challenge", MsgChallengeReply: "ChallengeReply",
		MsgProposeBlock: "ProposeBlock", MsgAttestReply: "AttestReply",
		MsgCommitBlock: "CommitBlock", MsgCommitAck: "CommitAck",
		MsgGetChain: "GetChain", MsgChainReply: "ChainReply",
		MsgCheckReachability: "CheckReachability", MsgReachabilityReply: "ReachabilityReply",
	}
	if int(k) < len(names) && names[k] != "" {
		return names[k]
	}
	return fmt.Sprintf("MsgKind(%d)", k)
}

// IsReply reports whether this kind terminates a pending request.
func (m Message) IsReply() bool {
	switch m.Kind {
	case MsgFindNodeReply, MsgGetProvidersReply, MsgAddProviderAck, MsgStoreChunkAck, MsgFetchChunkReply, MsgHasChunkReply, MsgChallengeReply, MsgAttestReply, MsgCommitAck, MsgChainReply, MsgBondReply, MsgTokenReply, MsgIssuerKeyReply:
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
