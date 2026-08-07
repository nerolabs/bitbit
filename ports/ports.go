// Package ports holds every interface that crosses a component boundary,
// plus the primitive types those interfaces share. Core packages and
// adapters both import ports; nothing in ports imports either of them.
package ports

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
)

// Hash is a SHA-256 digest. It doubles as a chunk's identity: content
// addressing means the name of a chunk IS its hash, so verification is
// intrinsic and no host ever has to be trusted.
type Hash [sha256.Size]byte

// ChunkID names a stored chunk. It is always the SHA-256 of the chunk's
// bytes (ciphertext for data chunks, plain bytes for manifest chunks).
type ChunkID = Hash

func (h Hash) String() string { return hex.EncodeToString(h[:]) }

// ParseHash decodes the hex form produced by Hash.String.
func ParseHash(s string) (Hash, error) {
	var h Hash
	b, err := hex.DecodeString(s)
	if err != nil {
		return h, fmt.Errorf("parse hash: %w", err)
	}
	if len(b) != len(h) {
		return h, fmt.Errorf("parse hash: got %d bytes, want %d", len(b), len(h))
	}
	copy(h[:], b)
	return h, nil
}

// HashBytes is the one hashing rule used everywhere.
func HashBytes(b []byte) Hash { return sha256.Sum256(b) }

// Chunk is a blob plus the ID it must hash to.
type Chunk struct {
	ID   ChunkID
	Data []byte
}

// NewChunk builds a chunk whose ID is derived from its data.
func NewChunk(data []byte) Chunk {
	return Chunk{ID: HashBytes(data), Data: data}
}

// Verify reports whether Data actually hashes to ID. Every component that
// receives a chunk from anywhere — a store, a peer, a file — must call
// this before using it. A node that trusts is a bug.
func (c Chunk) Verify() bool { return HashBytes(c.Data) == c.ID }

var (
	ErrNotFound           = errors.New("chunk not found")
	ErrCorrupt            = errors.New("chunk data does not match its ID")
	ErrDupPublish         = errors.New("root already published with different entry")
	ErrNoSuchEntry        = errors.New("root not in registry")
	ErrInsufficientCredit = errors.New("insufficient credit to publish")
	ErrPublisherRequired  = errors.New("gated registry requires a publisher identity")
	ErrStoreFull          = errors.New("store capacity exhausted")
)

// ChunkStore is anywhere chunks can live: an in-memory map, a directory
// tree, eventually a remote peer's disk.
type ChunkStore interface {
	// Put stores c. Implementations must reject chunks that fail Verify.
	Put(ctx context.Context, c Chunk) error
	// Get returns the chunk, re-verified against id.
	Get(ctx context.Context, id ChunkID) (Chunk, error)
	Has(ctx context.Context, id ChunkID) (bool, error)
	List(ctx context.Context) ([]ChunkID, error)
	// Delete exists for capacity eviction (and for sim scenarios that
	// destroy shards on purpose).
	Delete(ctx context.Context, id ChunkID) error
}

// CapacityReporter is implemented by stores with a fixed pledge
// ("silt daemon -capacity 2G"). Nodes whose store reports capacity
// piggyback it on every message, which is how the network learns its
// own total size.
type CapacityReporter interface {
	Capacity() (used, total int64)
}

// ProofStore persists the StorageProof that arrived with each hosted chunk.
// Provider records live only in peers' memories (they die with a process), so
// a restarting daemon must re-announce everything it holds — but a coded
// shard is announced under its COLUMN key hash(root‖column), which it can only
// compute from that shard's proof. Keeping proofs in memory meant a restart
// re-announced coded shards under the wrong key, leaving a disk full of
// content undiscoverable (#69). Persisting them alongside the chunks lets the
// re-announce reconstruct the right keys — and lets the node still answer
// storage-audit challenges after a restart. A nil ProofStore means
// no persistence (memory-only, fine for sims and ephemeral clients).
type ProofStore interface {
	Put(id ChunkID, p StorageProof) error
	// Load returns every persisted proof, for repopulating on startup.
	Load() (map[ChunkID]StorageProof, error)
	Delete(id ChunkID) error
}

// PlotStore persists a node's identity-bound storage-bond plot (core/bond) —
// the large space-time dataset behind its consensus standing. Plotting is
// deliberately expensive (that is the Sybil cost), so a restart must RELOAD
// the plot and re-verify it against its committed root (B7: persisted state is
// re-verified, not trusted) rather than re-plotting from scratch (#93). A nil
// PlotStore means memory-only — the node re-plots on every start, fine for
// sims and tests.
type PlotStore interface {
	// Save writes this identity's plot: its committed root and blocks.
	Save(id NodeID, root Hash, blocks [][]byte) error
	// Load returns the persisted root and blocks; ok is false if none exists.
	Load(id NodeID) (root Hash, blocks [][]byte, ok bool, err error)
}

// Entry is what gets published to the global registry: the Merkle root
// that names a file, plus enough metadata to begin retrieval. The chunk
// IDs of the serialized manifest are included because the root alone
// tells you nothing about where to start; in the networked version these
// IDs are what you ask the DHT for.
type Entry struct {
	Root           Hash
	ManifestChunks []ChunkID
	FileSize       int64
	// Publisher identifies who pays the publish fee when the registry
	// is credit-gated. Zero in ungated (local CLI) use.
	Publisher NodeID
	// Token, when present, is a quorum-issued publish credential that
	// authorizes this entry WITHOUT a durable Publisher identity — the fix
	// for the on-chain authorship leak (F1). The chain verifies it and
	// rejects a reused serial (double-spend). Verification/issuance live in
	// core/publishtoken and core/blindtoken; this is just the wire shape.
	Token *PublishToken `cbor:",omitempty"`
}

// TokenSig is one validator's blind signature on a publish token's serial.
type TokenSig struct {
	Validator NodeID
	Sig       []byte
}

// PublishToken is a publisher-unlinkable publish credential: a random serial
// blind-signed by a quorum of distinct validators. It carries no durable
// identity, so an observer cannot map a reputation key to the roots it published.
type PublishToken struct {
	Serial []byte
	Sigs   []TokenSig
}

// PublishCredit is a prepaid, blind-signed publish credit (M0 privacy D3 / F4):
// a random serial an issuer blind-signed at BULK MINT time (fee charged then).
// Spending it later buys a publish-token signature from that same issuer WITHOUT
// a per-publish fee debit — severing the ledger-level link from the durable
// standing key to a specific publish. The issuer blind-signed the serial, so it
// cannot tie the revealed credit back to the mint session; a spent-set stops
// double-spends (online Chaumian e-cash).
type PublishCredit struct {
	Serial []byte
	Sig    []byte
}

// Registry is the append-only log of published roots. v1 is a single
// honest in-process instance; the interface is the seam where a
// chain-backed implementation would slot in later.
type Registry interface {
	Publish(ctx context.Context, e Entry) error
	Lookup(ctx context.Context, root Hash) (Entry, bool, error)
	All(ctx context.Context) ([]Entry, error)
}

// CreditLedger is the future proof-of-retrieval seam: nodes earn credit
// for serving chunks and spend it on registry publishes. v1 accounting
// is naive and trusting; the interface is what a cryptographically
// audited version would also implement.
type CreditLedger interface {
	// RecordServe credits server for delivering bytes of chunk id to
	// requester.
	RecordServe(server, requester NodeID, id ChunkID, bytes int64)
	// RecordServeToObject is the object-aware serve (H7): like RecordServe, it
	// credits the server for delivering bytes of chunk id, but it diverts a
	// protocol-fixed auto-skim of that revenue into object root's durability
	// escrow, so popular data self-funds its own repair. Returns the credits
	// skimmed. Standing is untouched — serving funds the balance economy only.
	RecordServeToObject(server, requester NodeID, root Hash, id ChunkID, bytes int64) int64
	// RecordAudit settles a storage challenge: a passed audit earns the
	// prover a reward, a failed one costs a slash.
	RecordAudit(prover NodeID, id ChunkID, passed bool)
	// RecordBondChallenge settles a storage-BOND challenge: the prover
	// answered (or failed) a random challenge on its identity-bound bond
	// of provenBytes, rooted at root. This is the Sybil-cost input to
	// reputation — standing that must be backed by real, challenged, held
	// storage, not self-reported serving. root binds the standing to a
	// specific plot: a root credits at most one identity, so a colluding
	// operator cannot amortise one plot across many identities. tick is a
	// monotonic clock for staleness.
	RecordBondChallenge(prover NodeID, root Hash, provenBytes int64, passed bool, tick uint64)
	// DecayStale retires bonded standing not re-proven within maxAge of
	// now, so consensus standing must be sustained by ongoing challenges.
	DecayStale(now, maxAge uint64)
	// SlashEquivocation records a PROVEN consensus double-sign (a validator
	// signed two different blocks at the same height; see chain.Equivocation),
	// burying the culprit's standing so it can no longer influence consensus.
	SlashEquivocation(id NodeID)
	// SlashFalseRepair records a PROVEN false repair claim against a caretaker
	// (H7 slice-2): the claimed rebuilt shard failed the public correctness
	// recompute, which is self-attributing (anyone can rerun it), so the
	// claimant's standing is docked. Like every slash it can only ever LOWER
	// standing, never mint it — see the Invariant-A guard.
	SlashFalseRepair(id NodeID)
	// FundEscrow moves amount credits from funder's balance into object root's
	// durability reserve — a publisher prepaying a repair budget (H7). It is a
	// BALANCE-economy move only; it never touches standing.
	FundEscrow(root Hash, funder NodeID, amount int64) error
	// PayBounty releases up to amount credits from object root's durability
	// reserve to a repairer's balance, returning the credits actually paid. The
	// caller must invoke it only on a verified proof-of-repair transcript.
	PayBounty(root Hash, repairer NodeID, amount int64) int64
	// EscrowBalance is the credit currently available to pay repair bounties for
	// object root — its remaining funded horizon. Reading it moves nothing.
	EscrowBalance(root Hash) int64
	// Reputation is a node's current consensus standing — the same number the
	// chain gates proposals and attestations on. Read so a validator can
	// narrate standing accrual/decay in the field (acceptance F7).
	Reputation(n NodeID) int64
	Balance(n NodeID) int64
	CanPublish(n NodeID) bool
	// ChargePublish deducts the publish fee, or returns
	// ErrInsufficientCredit without side effects.
	ChargePublish(n NodeID) error
}
