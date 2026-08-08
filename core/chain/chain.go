// Package chain is the registry as an append-only block chain,
// maintained by the daemons themselves — the M12 replacement for the
// "single honest instance". The consensus is deliberately NOT
// proof-of-work: blocks commit by reputation-weighted quorum.
//
//   - A block is proposed by a node whose reputation clears
//     MinProposerRep, and commits only with attestations (Ed25519
//     signatures over the block hash) from at least Quorum DISTINCT
//     validators, each clearing MinAttesterRep, none of them the
//     proposer. No single node's say-so writes a block.
//   - Reputation is earned the M7/M9 way: passed storage audits and
//     bytes served (see credit.Reputation). A fresh identity starts at
//     zero and cannot propose or attest; and because NodeID is the hash
//     of the signing key (M10), reputation cannot be transplanted.
//   - Blocks carry registry entries only — root, manifest chunk
//     pointers, size. Manifests stay chunked and sealed off-chain, so
//     the chain stays small and content-blind.
//
// Every validator holds a full replica and validates everything: a
// block is accepted exactly when its hashes, signatures, reputations,
// quorum, and entries all check out against the local replica. Honest
// scope note: this is a quorum chain for a network with an honest
// validator majority, not a fork-choice consensus for adversarial
// partitions — there is no chain reorganization in v1; first valid
// block at a height wins.
package chain

import (
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"

	"github.com/fxamacker/cbor/v2"

	"github.com/nerolabs/silt/core/publishtoken"
	"github.com/nerolabs/silt/core/translog"
	"github.com/nerolabs/silt/ports"
)

// Config sets the consensus thresholds. Zero MinRep values make a
// permissive chain (useful for small trusted deployments and demos);
// the sim exercises the strict settings.
type Config struct {
	MinProposerRep int64
	MinAttesterRep int64
	Quorum         int // attestations required (a FLOOR; see ByzantineQuorum), excluding the proposer
	// ByzantineQuorum sizes the required quorum at the Byzantine threshold
	// (H4 / Memo 05): in OBJECTIVE mode a commit's support set (the proposer plus
	// its attesters) is raised to a supermajority n−f of the qualified bonded set
	// of size N (f = ⌊(N-1)/3⌋), so any two support sets intersect in ≥ f+1 ≥ 1
	// honest validator — the classic quorum-intersection safety, which a FIXED
	// Quorum loses as the set grows (a fixed 3 among 30 validators no longer
	// guarantees two quorums share an honest node). Config.Quorum stays a floor (for
	// tiny trusted deployments); the effective attestation requirement is
	// max(Quorum, N−f−1). It only ever RAISES the bar, so it is safe to leave off
	// for legacy/trusted configs (default) and default-on for an untrusted objective
	// validator. See RequiredQuorum. No effect in legacy (reputation) mode, where the
	// qualified set size is a local, divergent view.
	ByzantineQuorum bool
	// Launch-window "training wheels" (risk 15): while the network is immature —
	// its NAKAMOTO COEFFICIENT over non-anchor bonded weight is below
	// MatureValidators — a commit ALSO needs AnchorQuorum attestations from Anchors
	// (a declared seed set), so a Sybil quorum can't capture a young network before
	// it decentralizes. The wheels shed MECHANICALLY on measured decentralization,
	// never a flag day, and — unlike a one-way latch — RE-ENGAGE if decentralization
	// later drops (the post-shed escape hatch, H4). Anchors are plural and require a
	// threshold, so no single anchor is load-bearing (cf. R4) — and while they can
	// gate publication on the young network (a transparent, on-chain, time-limited
	// power), they can never do so once mature. Empty Anchors / zero AnchorQuorum =
	// no training wheels (the default; trusted/sim deployments).
	Anchors      map[ports.NodeID]bool
	AnchorQuorum int
	// MatureValidators is the required NAKAMOTO COEFFICIENT (H4 / Memo 05): the
	// minimum number of DISTINCT non-anchor bonds whose combined bonded weight must
	// EXCEED the Byzantine fraction (⌊total/3⌋) of the non-anchor bonded set before
	// the training wheels shed. This is cost-to-corrupt over bond-distinct operators,
	// not a head-count: a network whose weight is dominated by one big bond (Nakamoto
	// 1) is NOT mature no matter how many satellite keys an operator adds, so one
	// operator cannot cheaply trip the wheels off. 0 = no threshold (always mature;
	// the default). (Residual, documented: a determined operator that splits its
	// stake into many EQUAL bonds still inflates the coefficient — the on-chain limit
	// is that stake concentration is invisible — but it pays the full cost-to-corrupt
	// and ByzantineQuorum still bounds it to ≤ ⅓ of weight for safety.)
	MatureValidators int
	// OperatorMargin is M, the conservative keys-per-operator inflation factor the
	// C2 concentration metric (C2Metric / Mature) discounts the bond-distinct
	// Nakamoto coefficient by: since on-chain data carries no operator label, one
	// operator may split its stake across up to ~M keys, so the true operator count
	// is bounded k* ≥ k̂/M and the maturity shed demands k̂ ≥ MatureValidators × M
	// distinct bonds. HEURISTIC by theorem (Kwon, D-C2): only as tight as M is
	// honest. 0/unset = 1 (no discount — legacy/sim/single-operator default,
	// behavior unchanged); a real untrusted deployment sets it > 1 in genesis.
	OperatorMargin int
	// AllowPublisher permits an entry to carry a durable Publisher NodeID.
	// It is FALSE by default because a Publisher→root record is permanent
	// on an append-only chain — the M0 privacy corner silently surrendered
	// in the historical record (F1/#14, #97). The unlinkable path (a
	// blind-signed publish token, or no identity at all) is the default;
	// only an explicitly trusted deployment opts back into Publisher
	// entries. Genesis is exempt (it seeds via AppendGenesis, and its
	// proposer is public by design).
	AllowPublisher bool
	// MinBond turns on OBJECTIVE fork-choice (D2 / red-team F6). When > 0 (and a
	// bond verifier is wired, SetBondVerifier), proposer/attester eligibility,
	// the quorum count, and the fork-choice WEIGHT are all decided by ON-CHAIN
	// bond registrations (Block.BondRegs) — a quantity every replica recomputes
	// identically from the blocks — instead of the local, per-node reputation
	// view. That is what stops two honest replicas with different audited sets
	// from computing different winners and forking permanently. A validator
	// qualifies iff its committed bonded size ≥ MinBond. Zero (default) keeps the
	// legacy reputation-gated path unchanged, so existing deployments and the
	// permissive/sim configs are unaffected.
	MinBond int64
	// MinBondBytes is the OBJECTIVE anti-release floor (retest G4), mirroring the
	// node-side floor the credit ledger already enforces (core/node MinBondBytes,
	// bondaudit): a bond below it earns NO objective standing, because a plot that
	// small can be released and re-plotted inside a challenge window, so its
	// one-time proof does not evidence sustained held space. Distinct from MinBond
	// (the admission size): a deployment can admit at MinBond yet still deny
	// fork-choice standing to sub-floor bonds. Zero (default) = no floor.
	MinBondBytes int64
	// BondTTLBlocks is the OBJECTIVE re-challenge cadence (retest G4): a bonded
	// validator's standing LAPSES this many blocks after the block that carried
	// its latest registration, unless it renews with a FRESH space-time proof
	// (a new BondReg, bound to a recent parent nonce). This enforces the "time"
	// half of proof-of-space-TIME on the objective path — a validator that
	// registers once and then RELEASES its plot cannot answer the fresh challenge
	// to renew, so its vote decays to nothing instead of persisting forever off a
	// single one-time proof. Decay is a deterministic function of block height, so
	// every replica expires standing in lockstep. Zero (default) = no expiry.
	BondTTLBlocks uint64
	// WSCheckpoint is a WEAK-SUBJECTIVITY checkpoint (F-1): a recent trusted block
	// (height + hash) this replica refuses to reorg AT OR BEFORE, regardless of fork
	// weight. silt is weakly subjective — a node syncing from genesis (or long
	// offline) cannot distinguish the real matured chain from a forged long-range one
	// on chain data alone (Buterin WS; Gaži–Kiayias–Russell stake-bleeding) — so the
	// one-way maturity latch is only safe if a fresh node is ALSO pinned to a recent
	// trusted state out-of-band. Every production PoS chain does exactly this
	// (Ethereum checkpoint-sync, Cosmos ADR-044 unbonding, Casper finality); Bitcoin
	// removed its hardcoded checkpoints once cheaper protections existed. A reorg that
	// rewrites history at/before the checkpoint is rejected as a long-range attack.
	// The trusting window (how recent the checkpoint must be) is the weak-subjectivity
	// period, bounded by BondTTLBlocks + slashing depth (the eviction/unbonding
	// analogue). Zero Height = no checkpoint (genesis-trusting; safe only at launch,
	// on a trusted swarm, or before the network has matured). See docs/design/m0.md §10.
	WSCheckpoint WSCheckpoint
}

// WSCheckpoint is a recent trusted (height, hash) a replica will not reorg before.
// See Config.WSCheckpoint.
type WSCheckpoint struct {
	Height uint64
	Hash   ports.Hash
}

func DefaultConfig() Config {
	return Config{MinProposerRep: 100, MinAttesterRep: 100, Quorum: 3}
}

// BlockVersion is the schema/rule era a block is minted under. It is
// committed by Hash and checked at decode, so a block from one era can
// never be silently mis-validated under another era's rules — the
// hard-fork guard the chain needs BEFORE any change to what a block hash
// commits to or how a block validates (real-bond commitments, mandatory
// tokens; #98, prerequisite for #90/#91/#92). Additive field changes stay
// version-compatible via the keyasint tags (the Token addition proved
// this); a version bump is reserved for a change that would otherwise be a
// silent flag-day. There is deliberately no v2 yet: this lands the guard
// while the chain is still throwaway.
const BlockVersion = 1

// Block is one link of the registry chain.
type Block struct {
	Height      uint64        `cbor:"1,keyasint"`
	Prev        ports.Hash    `cbor:"2,keyasint"`
	Entries     []ports.Entry `cbor:"3,keyasint"`
	Proposer    []byte        `cbor:"4,keyasint"` // Ed25519 public key
	ProposerSig []byte        `cbor:"5,keyasint,omitempty"`
	Atts        []Attestation `cbor:"6,keyasint,omitempty"`
	// Revocations are append-only takedown records: opaque roots that a
	// SUBSCRIBING node no-ops on (honoring is per-operator — see
	// node.SetHonorChainRevocations / ReplicaRegistry.HonorRevocations — not a
	// global switch). Each named root must already be committed on this chain
	// (ValidateProposal enforces existence), so a quorum cannot revoke content
	// it never published. Deletion is impossible on an immutable chain, so a
	// takedown is an ADDITION — a tombstone — that replicates and is
	// tamper-evident like any other block.
	Revocations []ports.Hash `cbor:"7,keyasint,omitempty"`
	// Version is the block's rule era (see BlockVersion). Committed by Hash
	// and required at decode; every minted block sets it.
	Version uint64 `cbor:"8,keyasint"`
	// Unrevocations reverse a prior takedown: each names a currently-revoked
	// root and clears it on commit (apply). This is the governance undo the
	// tenets require — takedown must not be a one-way, permanent asymmetry —
	// and, like a revocation, it is quorum-gated and replicated.
	Unrevocations []ports.Hash `cbor:"9,keyasint,omitempty"`
	// BondRegs are on-chain PoST-bond registrations that make fork-choice
	// OBJECTIVE (D2 / red-team F6): each records a validator's bonded size with a
	// fresh space-time proof any replica re-verifies, so "who is a qualified
	// validator, and how heavy is their attestation" is a function of the chain,
	// not of the local reputation view. Only meaningful when Config.MinBond > 0;
	// omitempty keeps this additive (a block with no registrations hashes exactly
	// as before, so no BlockVersion bump). Committed by Hash so attesters sign
	// over them.
	BondRegs []BondReg `cbor:"10,keyasint,omitempty"`
	// Slashes are on-chain equivocation records (red-team F2): a self-verifying
	// proof that a validator double-signed. On commit, the culprit is EVICTED from
	// the objective bonded set (its `c.bonded` weight → 0) and permanently barred
	// from re-earning it — so a proven double-sign costs standing in objective
	// mode, not only in the reputation ledger the objective set never reads.
	// Committed by Hash (attesters sign over them); omitempty keeps it additive.
	Slashes []Equivocation `cbor:"11,keyasint,omitempty"`
}

// Attestation is a validator's signature over the block hash. The
// public key rides along because a NodeID (its hash) can't be inverted.
type Attestation struct {
	PubKey []byte `cbor:"1,keyasint"`
	Sig    []byte `cbor:"2,keyasint"`
}

// BondReg registers (or renews) a validator's on-chain PoST bond for objective
// fork-choice (F6). Validator is the ed25519 public key; Root/Size are the bond
// commitment; Answer is a CBOR-encoded bond space-time answer for the fresh
// nonce derived from the block's parent (BondRegNonce), so it cannot be replayed
// to another height or fork; Sig is the validator's signature binding the claim
// to its identity. A non-genesis registration is accepted only if Sig verifies
// and the injected bond verifier accepts (Root, Size, nonce, Answer) — i.e. the
// validator PROVED it holds the bond NOW. Genesis registrations are declared
// (like the genesis block itself), seeding the launch validator set.
type BondReg struct {
	Validator []byte     `cbor:"1,keyasint"`
	Root      ports.Hash `cbor:"2,keyasint"`
	Size      int64      `cbor:"3,keyasint"`
	Answer    []byte     `cbor:"4,keyasint,omitempty"`
	Sig       []byte     `cbor:"5,keyasint,omitempty"`
	// Domain is the validator's committed failure-domain label (A axis, D-C2): a
	// self-declared AS/rack/geo hash (the same domainID gossiped for DHT diversity,
	// H5-B), now COMMITTED in the bond so the concentration metric can count
	// address-diverse participants deterministically (C2Metric NakamotoDomains).
	// Signed (see signingBytes) so it binds to the validator. 0 = unset (treated as
	// independent — behavior identical to pre-A-axis chains). HONESTLY WEAK: a
	// declared domain is free to claim; it only costs a distinct real network
	// position insofar as the transport layer (H5-B) refuses to route to a validator
	// whose declared domain does not match its observed /24. It PRICES concentration
	// (a splitter must declare — and be routable from — distinct domains), it does
	// not CLOSE it (Kwon; the honest whale remains — m0.md §10).
	Domain uint64 `cbor:"6,keyasint,omitempty"`
}

// ValidatorID is the NodeID (hash of the public key) that a registration bonds.
func (r BondReg) ValidatorID() ports.NodeID { return sha256.Sum256(r.Validator) }

// signingBytes is the message a validator signs to claim a registration: the
// root, size, and fresh nonce, domain-separated. Binding the nonce stops a
// signature made for one position being replayed at another.
func (r BondReg) signingBytes(nonce uint64) []byte {
	b := make([]byte, 0, 32+len(r.Root)+8+8)
	b = append(b, []byte("silt/chain/bondreg/v1")...)
	b = append(b, r.Root[:]...)
	var sz [16]byte
	binary.BigEndian.PutUint64(sz[:8], uint64(r.Size))
	binary.BigEndian.PutUint64(sz[8:], nonce)
	b = append(b, sz[:]...)
	// Bind the committed domain (A axis) — but ONLY when set, so a domain-0 bond
	// signs the exact pre-A-axis message and every existing/genesis signature still
	// verifies (backward-compatible; no BlockVersion bump needed).
	if r.Domain != 0 {
		var d [8]byte
		binary.BigEndian.PutUint64(d[:], r.Domain)
		b = append(b, []byte("silt/chain/bondreg/domain/v1")...)
		b = append(b, d[:]...)
	}
	return b
}

var encMode cbor.EncMode

func init() {
	var err error
	encMode, err = cbor.CanonicalEncOptions().EncMode()
	if err != nil {
		panic(err)
	}
}

// Hash covers everything except signatures: height, ancestry, entries,
// proposer, and both takedown and undo records. Signing the hash therefore
// signs the block's entire content and its place in history.
func (b *Block) Hash() ports.Hash {
	unsigned := Block{Version: b.Version, Height: b.Height, Prev: b.Prev, Entries: b.Entries, Proposer: b.Proposer, Revocations: b.Revocations, Unrevocations: b.Unrevocations, BondRegs: b.BondRegs, Slashes: b.Slashes}
	raw, err := encMode.Marshal(&unsigned)
	if err != nil {
		panic(err) // canonical encoding of our own struct cannot fail
	}
	return sha256.Sum256(raw)
}

// ProposerID is the proposer's NodeID: the hash of its key (M10).
func (b *Block) ProposerID() ports.NodeID { return sha256.Sum256(b.Proposer) }

func (a Attestation) AttesterID() ports.NodeID { return sha256.Sum256(a.PubKey) }

// Sign fills in the proposer key and signature.
func Sign(b *Block, priv ed25519.PrivateKey) {
	b.Proposer = append([]byte(nil), priv.Public().(ed25519.PublicKey)...)
	h := b.Hash()
	b.ProposerSig = ed25519.Sign(priv, h[:])
}

// Attest produces a validator's attestation for b.
func Attest(b *Block, priv ed25519.PrivateKey) Attestation {
	h := b.Hash()
	return Attestation{
		PubKey: append([]byte(nil), priv.Public().(ed25519.PublicKey)...),
		Sig:    ed25519.Sign(priv, h[:]),
	}
}

func Encode(b *Block) []byte {
	raw, err := encMode.Marshal(b)
	if err != nil {
		panic(err)
	}
	return raw
}

func Decode(raw []byte) (*Block, error) {
	var b Block
	if err := cbor.Unmarshal(raw, &b); err != nil {
		return nil, fmt.Errorf("chain: decode block: %w", err)
	}
	if b.Version != BlockVersion {
		return nil, fmt.Errorf("%w: got %d, want %d", ErrBlockVersion, b.Version, BlockVersion)
	}
	return &b, nil
}

func EncodeBlocks(bs []Block) []byte {
	raw, err := encMode.Marshal(bs)
	if err != nil {
		panic(err)
	}
	return raw
}

func DecodeBlocks(raw []byte) ([]Block, error) {
	var bs []Block
	if err := cbor.Unmarshal(raw, &bs); err != nil {
		return nil, fmt.Errorf("chain: decode blocks: %w", err)
	}
	for i := range bs {
		if bs[i].Version != BlockVersion {
			return nil, fmt.Errorf("%w: block %d got %d, want %d", ErrBlockVersion, i, bs[i].Version, BlockVersion)
		}
	}
	return bs, nil
}

var (
	ErrLowReputation      = errors.New("chain: reputation below threshold")
	ErrNoQuorum           = errors.New("chain: insufficient valid attestations")
	ErrBadSignature       = errors.New("chain: bad signature")
	ErrWrongParent        = errors.New("chain: block does not extend the local head")
	ErrDupRoot            = errors.New("chain: root already registered")
	ErrUseConsensus       = errors.New("chain: replica is read-only; entries are committed via consensus")
	ErrAnchorRequired     = errors.New("chain: immature network requires anchor attestations (training wheels)")
	ErrDeMatureQuorum     = errors.New("chain: de-matured network requires a real-bond super-quorum (≥⅔ of live bonded weight)")
	ErrPreCheckpointReorg = errors.New("chain: fork rewrites history at or before the weak-subjectivity checkpoint (long-range reorg refused)")
	ErrTokenRequired      = errors.New("chain: entry has no publish token (required)")
	ErrTokenSpent         = errors.New("chain: publish token serial already spent (double-spend)")
	ErrBlockVersion       = errors.New("chain: unsupported block version")
	ErrPublisherEntry     = errors.New("chain: entry carries a durable Publisher (records permanent linkage; publish unlinkably or run an explicitly trusted deployment)")
	ErrEmptyFork          = errors.New("chain: cannot reconcile an empty fork")
	ErrNoGenesis          = errors.New("chain: local replica has no genesis to anchor a reconcile")
	ErrForeignGenesis     = errors.New("chain: fork does not share our genesis (refusing to swap chains)")
	// ErrRevokeUnknownRoot rejects a takedown that names a root the chain has
	// never committed. Without this a quorum could revoke a competitor's
	// unpublished hash, or a hash that never existed — arbitrary censorship of
	// content that isn't on the ledger (red-team F5). A revocation must point
	// at a real prior publication record.
	ErrRevokeUnknownRoot = errors.New("chain: revocation names a root never published on this chain")
	// ErrUnrevokeNotRevoked rejects an un-revoke of a root that is not
	// currently revoked — the reversibility record only clears a live takedown.
	ErrUnrevokeNotRevoked = errors.New("chain: un-revocation names a root that is not currently revoked")
	// ErrBadBondReg rejects an on-chain bond registration whose validator
	// signature or space-time proof does not verify (objective fork-choice, F6):
	// a forged registration cannot buy objective weight.
	ErrBadBondReg = errors.New("chain: invalid on-chain bond registration")
	// ErrBadSlash rejects an on-chain equivocation record that is not a valid,
	// self-verifying double-sign proof — so a forged slash cannot evict an honest
	// validator (F2; forged-slash griefing stays denied).
	ErrBadSlash = errors.New("chain: invalid equivocation slash proof")
	// ErrGenesisTakedown rejects a genesis block carrying revocation/un-revocation
	// records — a genesis has no prior history to check a takedown against, so
	// allowing one would be a pre-emptive takedown of never-published content (F3).
	ErrGenesisTakedown = errors.New("chain: genesis must not carry takedowns (pre-emptive revocation forbidden)")
)

// Chain is a validator's replica plus the rules for growing it.
type Chain struct {
	cfg     Config
	rep     func(ports.NodeID) int64
	blocks  []Block
	byRoot  map[ports.Hash]ports.Entry
	revoked map[ports.Hash]bool
	// revLog is the CT-style transparency log (core/translog): every honored
	// revocation and un-revocation is appended as an event, in commit order, so a
	// takedown can be PROVEN recorded (inclusion) and history can't be silently
	// rewritten (consistency). A pure, deterministic function of the committed
	// blocks — rebuilt identically on replay — so it needs no separate persistence.
	revLog *translog.Log
	// validatorsSeen is the set of distinct qualified attesters that have
	// ever committed a block — the monotonic decentralization signal the
	// training wheels shed on (see Mature).
	validatorsSeen map[ports.NodeID]bool
	// everMature is the ONE-WAY MATURITY LATCH (F-1): true once Mature() has held
	// at ANY committed height. The launch anchors are load-bearing only while this
	// is FALSE, so once a network is first certified decentralized the zero-bond
	// anchors NEVER become load-bearing again — the one-way ratchet immutable #3
	// promises. Like validatorsSeen/bonded it is a pure, monotonic function of the
	// committed block sequence (latched in apply, re-derived on Reload, carried
	// across a reorg by adopt), so every replica agrees on it as a CONSENSUS fact.
	// It is never reset. The old code gated anchors on the LIVE Mature(), which
	// re-armed the wheels whenever concentration rose — even from one honest whale
	// growing REAL bond — handing a zero-bond anchor permanent power, or halting the
	// chain if the anchors were gone. De-maturation liveness AFTER the latch is
	// carried by the real-bond super-quorum fallback (RequiredQuorum), never by
	// re-arming anchors. See docs/design/m0.md §10 (F-1).
	everMature bool
	// Publisher-privacy publish tokens (F1): when tokenQuorum > 0 every entry
	// must carry a PublishToken blind-signed by that many distinct qualified
	// validators (issuer keys from issuerKey), and spent records each serial so
	// it can be spent only once (double-spend rejected chain-wide).
	tokenQuorum int
	issuerKey   func(ports.NodeID) *rsa.PublicKey
	spent       map[string]bool
	// bonded is the OBJECTIVE validator set for fork-choice (F6): NodeID → the
	// bonded size from its latest on-chain BondReg. A pure function of the blocks,
	// so every replica computes it identically — which is the whole point.
	// Populated only when Config.MinBond > 0. verifyBond re-checks a registration's
	// space-time proof (injected so core/chain stays decoupled from core/bond).
	bonded     map[ports.NodeID]int64
	verifyBond func(pk []byte, root ports.Hash, size int64, nonce uint64, answer []byte) bool
	// bondRootOwner enforces, in the OBJECTIVE set, the same per-root dedup the
	// credit ledger has (credit.rootOwner): a bond Root builds standing for AT
	// MOST ONE identity, so a colluding operator pointing N identities at one
	// shared plot earns one bond's standing, not N (red-team F1). The space-time
	// proof is not identity-bound, so without this a single plot's answer would
	// verify — and be credited — for every identity that copies it.
	bondRootOwner map[ports.Hash]ports.NodeID
	// bondRootProven records whether a root's current owner claimed it with a
	// VERIFIED space-time proof (a height>0 registration, gated by
	// validateBondRegs) rather than a mere DECLARED genesis registration
	// (applied without a proof). A proof of possession displaces a declaration,
	// so a malicious genesis cannot pre-squat an honest validator's real plot
	// root and, via the F1 first-owner dedup, lock the true holder out when it
	// later registers with a genuine proof (retest G3). Once proven, a root
	// stays proven, so first-proven-owner still wins among real bonds (F1).
	bondRootProven map[ports.Hash]bool
	// bondRegHeight records the height of the block carrying each validator's
	// LATEST bond registration, so objective standing can expire on a cadence
	// (Config.BondTTLBlocks, retest G4): a bond not renewed with a fresh proof
	// within the TTL window is pruned from `bonded`. Deterministic (a function of
	// block height), so every replica decays standing identically.
	bondRegHeight map[ports.NodeID]uint64
	// bondDomain records the committed A-axis failure-domain label from each
	// validator's LATEST bond registration (0 = unset). A pure function of the
	// committed blocks, so C2Metric can count address-diverse participants
	// deterministically (NakamotoDomains). See BondReg.Domain.
	bondDomain map[ports.NodeID]uint64
	// slashed is the set of validators evicted for a proven equivocation (F2). A
	// slashed id is disqualified and cannot re-earn bonded standing, so a proven
	// double-sign costs standing in the OBJECTIVE set, not only the rep ledger.
	slashed map[ports.NodeID]bool
}

// New starts an empty replica. rep is the local reputation view —
// validators judge proposals by what THEY have observed (audits run,
// serves seen), which is the sense in which trust here is earned, not
// declared.
func New(cfg Config, rep func(ports.NodeID) int64) *Chain {
	return &Chain{cfg: cfg, rep: rep,
		byRoot:         make(map[ports.Hash]ports.Entry),
		revoked:        make(map[ports.Hash]bool),
		revLog:         translog.New(),
		validatorsSeen: make(map[ports.NodeID]bool),
		spent:          make(map[string]bool),
		bonded:         make(map[ports.NodeID]int64),
		bondRootOwner:  make(map[ports.Hash]ports.NodeID),
		bondRootProven: make(map[ports.Hash]bool),
		bondRegHeight:  make(map[ports.NodeID]uint64),
		bondDomain:     make(map[ports.NodeID]uint64),
		slashed:        make(map[ports.NodeID]bool)}
}

// SetBondVerifier wires the objective-fork-choice bond check (F6): given a
// registration's (pk, root, size, nonce, answer), it re-verifies the space-time
// proof (typically bond.VerifySpaceTime with the node's VDF params). pk is the
// registrant's ed25519 public key (BondReg.Validator) — the labeling check (G2)
// recomputes labels from H(pk, n), so the plot is bound to this identity and
// size, not to an attacker-chosen root. Required for Config.MinBond > 0 to take
// effect; injected so core/chain does not depend on core/bond or core/vdf.
func (c *Chain) SetBondVerifier(f func(pk []byte, root ports.Hash, size int64, nonce uint64, answer []byte) bool) {
	c.verifyBond = f
}

// objective reports whether fork-choice runs on on-chain bonds (F6) rather than
// the local reputation view. It needs both a positive MinBond and a wired
// verifier — without the verifier we could not re-check a registration, so we
// fall back to the legacy rep path rather than trust an unproven bond.
func (c *Chain) objective() bool { return c.cfg.MinBond > 0 && c.verifyBond != nil }

// Objective reports whether this replica runs objective (on-chain bond)
// fork-choice (F6) rather than the local reputation view — so a proposer knows
// to attach its live bond registration.
func (c *Chain) Objective() bool { return c.objective() }

// launchAnchor reports whether id bootstraps the objective validator set: a
// declared training-wheels anchor, but ONLY while the network is immature. It
// breaks the objective-mode cold-start chicken-and-egg (you must be bonded on
// chain to propose/attest, but the first block that records bonds must itself be
// proposed and attested) by letting the declared launch set commit the early
// blocks — the same plural, threshold-gated set the training wheels already
// trust. It sheds MECHANICALLY at maturity (Mature()), after which only real
// on-chain bonds qualify. Anchors are expected to register their OWN real bonds
// early (live self-registration), so this is a launch crutch, not a standing
// exemption: it grants ELIGIBILITY, never fork-choice WEIGHT (weight is always
// summed real bond), so a declared anchor cannot outweigh a real bond.
func (c *Chain) launchAnchor(id ports.NodeID) bool {
	// Gated on the one-way latch, not the live Mature(): once the network has ever
	// matured, anchors lose bond-free eligibility FOREVER (F-1). An anchor that
	// registered its own real bond stays a normal validator on that real weight.
	return len(c.cfg.Anchors) > 0 && c.cfg.Anchors[id] && !c.everMature
}

// attesterQualified reports whether id may have its attestation counted toward
// quorum (and, if it has a real bond, weight). Objective mode: its committed
// bonded size clears MinBond, OR it is a launch anchor bootstrapping an immature
// network. Legacy mode: the local reputation view.
func (c *Chain) attesterQualified(id ports.NodeID) bool {
	if c.slashed[id] {
		return false // evicted for a proven equivocation (F2)
	}
	if c.objective() {
		return c.bonded[id] >= c.cfg.MinBond || c.launchAnchor(id)
	}
	return c.rep(id) >= c.cfg.MinAttesterRep
}

// proposerQualified reports whether id may propose. Objective mode: a bonded
// validator, or a launch anchor while the network is immature. Legacy mode uses
// MinProposerRep.
func (c *Chain) proposerQualified(id ports.NodeID) bool {
	if c.slashed[id] {
		return false // evicted for a proven equivocation (F2)
	}
	if c.objective() {
		return c.bonded[id] >= c.cfg.MinBond || c.launchAnchor(id)
	}
	return c.rep(id) >= c.cfg.MinProposerRep
}

// qualifiedCount is the number of distinct on-chain validators currently eligible
// to attest in objective mode: a committed bond ≥ MinBond, not slashed. It is the
// N the Byzantine quorum is sized against, recomputed identically by every replica
// from the chain (so RequiredQuorum never diverges). Anchors that also hold a real
// bond are counted; a pure training-wheels anchor with no bond is not (it is a
// bootstrap backstop, not part of the decentralized fault budget).
func (c *Chain) qualifiedCount() int {
	n := 0
	for id, sz := range c.bonded {
		if sz >= c.cfg.MinBond && !c.slashed[id] {
			n++
		}
	}
	return n
}

// bftThreshold is the number of NON-PROPOSER attestations a commit needs for
// Byzantine quorum-intersection safety over n validators. The tolerated fault
// count is f = ⌊(n-1)/3⌋, and the total support set (the proposer, always a
// qualified signer, PLUS its attesters) must be a supermajority of n−f. So the
// attestation count returned is (n−f)−1. Any two such support sets of size n−f
// intersect in 2(n−f)−n = n−2f ≥ f+1 validators (since n ≥ 3f+1), so — with ≤ f
// faulty — at least one HONEST validator is in both, which stops two conflicting
// blocks from each gathering a quorum (safety). n ≤ 0 → 0.
func bftThreshold(n int) int {
	if n <= 0 {
		return 0
	}
	f := (n - 1) / 3
	q := n - f - 1
	if q < 0 {
		return 0
	}
	return q
}

// RequiredQuorum is the number of distinct qualified non-proposer attestations a
// commit needs. It is Config.Quorum by default, but with ByzantineQuorum set in
// objective mode it rises to the Byzantine threshold 2f+1 over the qualified set —
// max(Quorum, bftThreshold(N)) — so quorum-intersection safety is preserved as the
// validator set grows. Exposed so a proposer gathers enough attestations to match
// what ValidateCommit will demand.
func (c *Chain) RequiredQuorum() int {
	q := c.cfg.Quorum
	if c.cfg.ByzantineQuorum && c.objective() {
		if bq := bftThreshold(c.qualifiedCount()); bq > q {
			q = bq
		}
	}
	return q
}

// BondRegNonce is the fresh challenge a non-genesis bond registration must
// answer, derived from the parent hash the block extends — so a registration
// proves possession AT this position and cannot be replayed to another height
// or fork. A registrant (see NewBondReg) computes its space-time answer for this
// nonce; the chain re-derives it identically at validation.
func BondRegNonce(prev ports.Hash) uint64 {
	h := sha256.Sum256(append([]byte("silt/chain/bondreg/nonce/v1"), prev[:]...))
	return binary.BigEndian.Uint64(h[:8])
}

// NewBondReg builds a signed registration for a validator's bond at the position
// following prev. answer is the CBOR-encoded space-time proof (bond.EncodeAnswer)
// for BondRegNonce(prev); the chain re-verifies it via the injected bond verifier
// (SetBondVerifier). The signature binds the (root, size, nonce) claim to the
// validator's key so a non-holder cannot register a bond it does not own.
func NewBondReg(signer ed25519.PrivateKey, root ports.Hash, size int64, answer []byte, prev ports.Hash, domain uint64) BondReg {
	r := BondReg{
		Validator: append([]byte(nil), signer.Public().(ed25519.PublicKey)...),
		Root:      root,
		Size:      size,
		Answer:    answer,
		Domain:    domain, // committed A-axis label (0 = unset); signed via signingBytes
	}
	r.Sig = ed25519.Sign(signer, r.signingBytes(BondRegNonce(prev)))
	return r
}

// validateBondRegs verifies a non-genesis block's bond registrations: each must
// carry a validator signature over its (root, size, nonce) and a space-time
// proof the injected verifier accepts for the fresh per-position nonce. Only
// enforced in objective mode; a legacy chain ignores BondRegs entirely.
func (c *Chain) validateBondRegs(b *Block) error {
	if !c.objective() {
		return nil
	}
	nonce := BondRegNonce(b.Prev)
	for _, r := range b.BondRegs {
		if err := c.validateBondReg(r, nonce); err != nil {
			return err
		}
	}
	return nil
}

// validateBondReg runs the per-registration objective checks for a given
// per-position nonce: a valid validator key, a signature over (root,size,nonce)
// binding the claim to that identity, a size clearing MinBond and the anti-
// release floor, and a space-time proof the injected verifier accepts. Shared by
// validateBondRegs (whole block) and ValidateBondReg (one peer-submitted renewal).
func (c *Chain) validateBondReg(r BondReg, nonce uint64) error {
	if len(r.Validator) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: bond registration has no valid validator key", ErrBadBondReg)
	}
	if !ed25519.Verify(ed25519.PublicKey(r.Validator), r.signingBytes(nonce), r.Sig) {
		return fmt.Errorf("%w: validator %s signature", ErrBadBondReg, r.ValidatorID())
	}
	if r.Size < c.cfg.MinBond {
		return fmt.Errorf("%w: validator %s size %d below MinBond %d", ErrBadBondReg, r.ValidatorID(), r.Size, c.cfg.MinBond)
	}
	if r.Size < c.cfg.MinBondBytes {
		return fmt.Errorf("%w: validator %s size %d below anti-release floor %d", ErrBadBondReg, r.ValidatorID(), r.Size, c.cfg.MinBondBytes)
	}
	if !c.verifyBond(r.Validator, r.Root, r.Size, nonce, r.Answer) {
		return fmt.Errorf("%w: validator %s space-time proof", ErrBadBondReg, r.ValidatorID())
	}
	return nil
}

// ValidateBondReg reports whether a single peer-submitted bond renewal would be
// accepted in a block extending the CURRENT head (H2 non-proposer renewal path).
// A proposer filters the renewals it received through this before including them,
// so one stale or forged submission can't poison the whole block; a receiver uses
// it to drop junk on arrival. False off the objective path (legacy ignores regs).
func (c *Chain) ValidateBondReg(r BondReg) bool {
	if !c.objective() {
		return false
	}
	head, _ := c.Head()
	return c.validateBondReg(r, BondRegNonce(head)) == nil
}

// validateSlashes verifies a block's on-chain equivocation records (F2): each
// must be a self-verifying double-sign proof — the culprit's own signatures over
// two DIFFERENT blocks at the SAME height. A forged accusation fails
// VerifyEquivocation, so an honest validator cannot be evicted (forged-slash
// griefing stays denied). Enforced on every write path.
func (c *Chain) validateSlashes(b *Block) error {
	for i := range b.Slashes {
		if !VerifyEquivocation(&b.Slashes[i]) {
			return fmt.Errorf("%w: proof %d", ErrBadSlash, i)
		}
	}
	return nil
}

// BondedSize reports the objective on-chain bonded size for id (0 if none).
// Exposed for observability and tests; it is the fork-choice weight of one of
// id's attestations in objective mode.
func (c *Chain) BondedSize(id ports.NodeID) int64 { return c.bonded[id] }

// IsBonded reports whether id is a qualified bond-distinct identity in the COMMITTED
// on-chain bond ledger: its bonded size clears MinBond and it has not been slashed.
// This is the admission bar attesterQualified uses in objective mode, exposed so the
// demand bank's P3b bonded-fetcher credential prices fake demand onto exactly the
// Sybil-priced identity supply C2 measures. Always false in legacy mode (MinBond 0).
func (c *Chain) IsBonded(id ports.NodeID) bool {
	return c.cfg.MinBond > 0 && !c.slashed[id] && c.bonded[id] >= c.cfg.MinBond
}

// IsSlashed reports whether id has been evicted for a proven equivocation (F2).
func (c *Chain) IsSlashed(id ports.NodeID) bool { return c.slashed[id] }

// CanonicalIssuers returns the objective issuer set for privacy-preserving token
// acquisition (M0 privacy D3 / F4 §2c): the on-chain bonded validators in a
// DETERMINISTIC order — bonded size descending, then NodeID ascending — so every
// publisher asks the SAME validators, and the subset it chose leaks nothing to a
// colluding issuer minority correlating who-asked-whom. Because it reads the
// on-chain bond (not the local reputation view), every replica computes the
// identical set — the same objectivity that heals fork-choice (F6). Returns at
// most max entries (all if max <= 0). Empty when no on-chain bonds are recorded
// (objective mode off, or an immature chain): the caller then falls back to its
// own peer list, which is the pre-D3 behavior.
func (c *Chain) CanonicalIssuers(max int) []ports.NodeID {
	type bonded struct {
		id   ports.NodeID
		size int64
	}
	list := make([]bonded, 0, len(c.bonded))
	for id, size := range c.bonded {
		if size >= c.cfg.MinBond && size > 0 {
			list = append(list, bonded{id, size})
		}
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].size != list[j].size {
			return list[i].size > list[j].size // heavier bond first
		}
		return bytesLess(list[i].id[:], list[j].id[:]) // deterministic tiebreak
	})
	if max > 0 && len(list) > max {
		list = list[:max]
	}
	out := make([]ports.NodeID, len(list))
	for i, e := range list {
		out[i] = e.id
	}
	return out
}

// RequireTokens turns on publisher-privacy publish tokens (F1): every entry
// must carry a PublishToken blind-signed by `quorum` distinct qualified
// validators (their issuer keys via issuerKey), and each serial spends exactly
// once (double-spend rejected across the whole chain). Off by default (quorum
// 0) — existing behavior is unchanged, so a Publisher-NodeID entry still works.
func (c *Chain) RequireTokens(quorum int, issuerKey func(ports.NodeID) *rsa.PublicKey) {
	c.tokenQuorum = quorum
	c.issuerKey = issuerKey
}

// Mature reports whether the network has decentralized enough for the launch-
// window anchors to no longer be required: its NAKAMOTO COEFFICIENT over the
// non-anchor bonded set is at least MatureValidators (H4 / Memo 05). Unlike the
// old head-count of distinct validators — which one operator could trip by
// spinning up many minimum bonds, then capture consensus once the wheels shed —
// this is cost-to-corrupt over bond-distinct operators: a set whose weight is
// dominated by a few bonds has a LOW coefficient and stays immature no matter how
// many satellite keys are added. It reads the CURRENT bonded set — the LIVE metric.
// It does NOT gate the anchors: that is the one-way latch EverMature() (F-1), so a
// later drop in decentralization can never re-arm the wheels. Mature() vs
// EverMature() diverge only in the de-maturation window, which the real-bond
// super-quorum handles. Legacy mode has no on-chain bonded set, so it falls back to
// the head count of distinct qualified validators seen.
func (c *Chain) Mature() bool {
	if c.cfg.MatureValidators <= 0 {
		return true
	}
	return c.matureNow()
}

// EverMature reports the one-way maturity LATCH (F-1): whether the network has
// been certified mature at ANY committed height. This — not the live Mature() — is
// what gates the launch anchors, so once a network first decentralizes the anchors
// never re-arm. A pure function of the committed blocks (see the everMature field).
func (c *Chain) EverMature() bool { return c.everMature }

// matureNow is the LIVE maturity metric over the CURRENT bonded set. Mature()
// wraps it; the latch (everMature) is what gates anchors. The two diverge exactly
// in the de-maturation window (everMature && !matureNow): the network matured, then
// concentration/attrition dropped it back below the bar — handled by the real-bond
// super-quorum, never by re-arming anchors (F-1).
func (c *Chain) matureNow() bool {
	if !c.objective() {
		n := 0
		for id := range c.validatorsSeen {
			if !c.cfg.Anchors[id] {
				n++
			}
		}
		return n >= c.cfg.MatureValidators
	}
	// Objective maturity gates on the OPERATOR-discounted coefficient AND the
	// address-diverse coefficient (A axis, D-C2), whichever is smaller — so a stake
	// split across many keys must clear MatureValidators × M distinct bonds AND
	// MatureValidators distinct declared domains. At M=1 with no domains set this is
	// the plain bond-distinct coefficient (unchanged behavior). min() only ever RAISES
	// the bar to shed, so it can never weaken an existing config.
	m := c.C2Metric()
	k := m.NakamotoOperators
	if m.NakamotoDomains < k {
		k = m.NakamotoDomains
	}
	return k >= c.cfg.MatureValidators
}

// C2 is the concentration measurement behind the "no quiet capture" axis (D-C2):
// cost-to-corrupt over bond-distinct participants, computed from the COMMITTED
// on-chain bond ledger (c.bonded) — never gossip, which kills the "lie about your
// size" skew half outright — plus the conservative operator-margin discount that
// stands in for the (impossible-on-chain) key→operator clustering, bounding the
// split half. It is ONE measurement, consumed by the maturity shed (Mature) and
// published for observability (chain-status); the private-lookup committee
// certification (H8/#179) is the third intended consumer.
type C2 struct {
	// NakamotoBonds is the fewest bond-distinct participants whose combined
	// committed weight EXCEEDS the Byzantine fraction (⌊total/3⌋) of the
	// participating weight — the raw cost-to-corrupt coefficient over distinct
	// BONDS (k̂). Weight-aware: a set dominated by one bond yields 1 no matter how
	// many satellite keys are added, so one operator can't cheaply inflate it by size.
	NakamotoBonds int
	// NakamotoOperators is the CONSERVATIVE lower bound on distinct OPERATORS,
	// ⌊NakamotoBonds / M⌋ for operator margin M: since one operator may split a
	// stake across up to ~M keys and on-chain data carries no operator label, the
	// true operator count k* ≥ k̂/M. This is the number the shed actually gates on,
	// so a splitter must clear k·M distinct bonds — the split-half defense. At M=1
	// (default) it equals NakamotoBonds. HEURISTIC by theorem (Kwon): only as tight
	// as M is honest; M_est under adversarial NodeID placement is unquantified
	// (carry margin, D-C2 / #182).
	NakamotoOperators int
	// CostToCorruptBytes is the bonded weight an attacker must control to reach the
	// fault threshold: ⌊total/3⌋ + 1 (one byte past the Byzantine fraction).
	CostToCorruptBytes int64
	// TotalBondedBytes is the participating committed bonded weight the metric is over.
	TotalBondedBytes int64
	// Participants is the number of distinct qualified bonds counted.
	Participants int
	// Margin is the operator margin M the coefficient was discounted by (≥1).
	Margin int
	// NakamotoDomains is the Nakamoto coefficient over ADDRESS-DIVERSE groups (A axis,
	// D-C2): the fewest DISTINCT declared failure-domains whose combined weight exceeds
	// ⌊total/3⌋. Bonds sharing a declared domain aggregate into one group, so a stake
	// split across many keys in one domain does not inflate it — only distinct domains
	// do. With no domains set it equals NakamotoBonds (unchanged). The maturity shed
	// gates on min(NakamotoOperators, NakamotoDomains), so a splitter must clear BOTH
	// k·M distinct bonds AND k distinct domains. Weak signal (a domain is declared,
	// H5-B-cross-checked at the transport layer, not proven) — pricing, not proof.
	NakamotoDomains int
	// DistinctDomains is the number of address-diversity groups counted (distinct
	// non-zero declared domains + each unset-domain bond as its own group).
	DistinctDomains int
	// HHI is the Herfindahl–Hirschman concentration index over the participating
	// bonds: Σ(share²) ∈ [1/n, 1]. 1 = one bond holds everything; 1/n = perfectly
	// even. A high HHI is the **honest-whale** signal C2 cannot close on-chain (Kwon):
	// surfaced as an out-of-band observability veto — measurement that makes
	// concentration LOUD, never consensus enforcement (D-C2 / F-1 follow-up).
	HHI float64
	// Gini is the Gini coefficient of the bonded-weight distribution ∈ [0,1]:
	// 0 = perfectly even, →1 = one holder. A companion inequality signal to HHI.
	Gini float64
	// TopShare is the largest single bond's fraction of participating weight ∈ [0,1].
	// The most interpretable capture signal: a bond approaching ⅓ is one step from
	// the Byzantine capture fraction (⌊total/3⌋) — the honest-whale alarm threshold.
	TopShare float64
}

// C2Metric computes the concentration measurement over the participating,
// qualified, committed bonds — validators that have ATTESTED a committed block
// (validatorsSeen, so a malicious genesis cannot DECLARE a fake-decentralized set)
// AND still hold a live bond ≥ MinBond, minus anchors and slashed keys. It reads
// CURRENT bonds, so a lapsed (TTL) bond drops out and the coefficient can fall —
// re-engaging the wheels (the post-shed escape hatch). Pure function of the
// committed chain state.
func (c *Chain) C2Metric() C2 {
	sizes := make([]int64, 0, len(c.validatorsSeen))
	domainWeight := make(map[uint64]int64) // A axis: non-zero domains aggregated
	var zeroDomainWeights []int64          // domain 0 (unset) → each its own group
	var total int64
	for id := range c.validatorsSeen {
		if c.cfg.Anchors[id] || c.slashed[id] {
			continue
		}
		if sz := c.bonded[id]; sz >= c.cfg.MinBond {
			sizes = append(sizes, sz)
			total += sz
			if d := c.bondDomain[id]; d != 0 {
				domainWeight[d] += sz // same declared domain → one group (no split inflation)
			} else {
				zeroDomainWeights = append(zeroDomainWeights, sz) // unset → independent
			}
		}
	}
	m := C2{TotalBondedBytes: total, Participants: len(sizes), Margin: c.operatorMargin()}
	if total == 0 {
		return m
	}
	sort.Slice(sizes, func(i, j int) bool { return sizes[i] > sizes[j] }) // largest first
	threshold := total / 3
	m.CostToCorruptBytes = threshold + 1
	var cum int64
	m.NakamotoBonds = len(sizes) // all needed unless the loop finds fewer
	for i, sz := range sizes {
		cum += sz
		if cum > threshold {
			m.NakamotoBonds = i + 1
			break
		}
	}
	m.NakamotoOperators = m.NakamotoBonds / m.Margin // ⌊k̂/M⌋, conservative
	// A axis (D-C2): NakamotoDomains is the Nakamoto coefficient over ADDRESS-DIVERSE
	// groups — bonds sharing a declared domain aggregate into one group, so splitting
	// a stake across many keys in ONE domain does NOT inflate the count; only distinct
	// declared domains do (the earned-per-network-position cost the flat margin M only
	// assumes). Domain 0 (unset) is independent, so a chain with no domains set yields
	// NakamotoDomains == NakamotoBonds — behavior identical to a pre-A-axis chain.
	groups := make([]int64, 0, len(domainWeight)+len(zeroDomainWeights))
	for _, w := range domainWeight {
		groups = append(groups, w)
	}
	groups = append(groups, zeroDomainWeights...)
	m.DistinctDomains = len(groups)
	sort.Slice(groups, func(i, j int) bool { return groups[i] > groups[j] })
	var gcum int64
	m.NakamotoDomains = len(groups)
	for i, w := range groups {
		gcum += w
		if gcum > threshold {
			m.NakamotoDomains = i + 1
			break
		}
	}
	// Concentration observability (D-C2 / F-1 follow-up): HHI, Gini, and the top
	// bond's share. C2 cannot CLOSE the honest-whale residue on-chain (Kwon), so this
	// is an out-of-band veto that makes a concentration event LOUD — measurement, not
	// enforcement. sizes is sorted largest-first; ftotal > 0 here.
	n := len(sizes)
	ftotal := float64(total)
	m.TopShare = float64(sizes[0]) / ftotal
	var hhi, giniNum float64
	for i, sz := range sizes {
		share := float64(sz) / ftotal
		hhi += share * share
		// Gini numerator for a DESCENDING-sorted distribution: Σ (n−1−2i)·xᵢ.
		giniNum += float64(n-1-2*i) * float64(sz)
	}
	m.HHI = hhi
	m.Gini = giniNum / (float64(n) * ftotal)
	return m
}

// operatorMargin is M, the conservative keys-per-operator inflation factor the C2
// coefficient is discounted by. 0/unset resolves to 1 (no discount — the legacy /
// sim / single-operator default), so existing deployments are unaffected; a real
// untrusted deployment sets it > 1 in genesis to demand a splitter clear k·M bonds.
func (c *Chain) operatorMargin() int {
	if c.cfg.OperatorMargin < 1 {
		return 1
	}
	return c.cfg.OperatorMargin
}

// Revoked reports whether root has been taken down by a committed
// revocation record.
func (c *Chain) Revoked(root ports.Hash) bool { return c.revoked[root] }

// The takedown-transparency operations logged as CT events (D-TAKEDOWN / #180).
const (
	RevOp   byte = 'R' // a root was revoked (taken down)
	UnrevOp byte = 'U' // a prior revocation was reversed
)

// RevocationLeaf is the canonical transparency-log entry for one takedown event:
// H("silt/revlog/v1" ‖ op ‖ root ‖ height). Exported so an independent auditor can
// reconstruct the leaf from public block data and check an inclusion proof.
func RevocationLeaf(op byte, root ports.Hash, height uint64) ports.Hash {
	b := make([]byte, 0, 16+1+len(root)+8)
	b = append(b, []byte("silt/revlog/v1")...)
	b = append(b, op)
	b = append(b, root[:]...)
	for i := 0; i < 8; i++ {
		b = append(b, byte(height>>(8*(7-i))))
	}
	return ports.HashBytes(b)
}

// RevocationLogRoot is the current Merkle Tree Head over every honored
// revocation/un-revocation, in commit order — the log's tamper-evident commitment.
func (c *Chain) RevocationLogRoot() ports.Hash { return c.revLog.Root() }

// RevocationLogSize is the number of takedown events logged so far.
func (c *Chain) RevocationLogSize() int { return c.revLog.Size() }

// RevocationLogRootAt is the historical log root at a given event count — what a
// consistency proof is checked against.
func (c *Chain) RevocationLogRootAt(size int) (ports.Hash, error) { return c.revLog.RootAt(size) }

// RevocationInclusionProof proves takedown event `index` is in the log at `size`.
func (c *Chain) RevocationInclusionProof(index, size int) ([]ports.Hash, error) {
	return c.revLog.InclusionProof(index, size)
}

// RevocationConsistencyProof proves the log at size m is a prefix of size n — that
// no logged takedown was ever silently removed or reordered.
func (c *Chain) RevocationConsistencyProof(m, n int) ([]ports.Hash, error) {
	return c.revLog.ConsistencyProof(m, n)
}

// Head returns the current tip hash and the height the NEXT block must
// carry. An empty chain expects height 0 with a zero Prev.
func (c *Chain) Head() (ports.Hash, uint64) {
	if len(c.blocks) == 0 {
		return ports.Hash{}, 0
	}
	last := c.blocks[len(c.blocks)-1]
	return last.Hash(), last.Height + 1
}

func (c *Chain) Len() int { return len(c.blocks) }

// Blocks returns the suffix of the chain starting at height from.
func (c *Chain) Blocks(from uint64) []Block {
	if from >= uint64(len(c.blocks)) {
		return nil
	}
	out := make([]Block, len(c.blocks)-int(from))
	copy(out, c.blocks[from:])
	return out
}

// ValidateProposal checks everything an attester must believe before
// signing: ancestry, proposer signature and reputation, and that every
// entry is well-formed and new.
func (c *Chain) ValidateProposal(b *Block) error {
	prev, height := c.Head()
	if b.Height != height || b.Prev != prev {
		return fmt.Errorf("%w: got height %d prev %s, want height %d prev %s",
			ErrWrongParent, b.Height, b.Prev, height, prev)
	}
	if len(b.Proposer) != ed25519.PublicKeySize {
		return ErrBadSignature
	}
	h := b.Hash()
	if !ed25519.Verify(ed25519.PublicKey(b.Proposer), h[:], b.ProposerSig) {
		return fmt.Errorf("%w: proposer", ErrBadSignature)
	}
	if !c.proposerQualified(b.ProposerID()) {
		if c.objective() {
			return fmt.Errorf("%w: proposer %s bonded %d, needs %d",
				ErrLowReputation, b.ProposerID(), c.bonded[b.ProposerID()], c.cfg.MinBond)
		}
		return fmt.Errorf("%w: proposer %s has %d, needs %d",
			ErrLowReputation, b.ProposerID(), c.rep(b.ProposerID()), c.cfg.MinProposerRep)
	}
	if len(b.Entries) == 0 && len(b.Revocations) == 0 && len(b.Unrevocations) == 0 && len(b.BondRegs) == 0 && len(b.Slashes) == 0 {
		return errors.New("chain: empty block")
	}
	if err := c.validateTakedowns(b); err != nil {
		return err
	}
	if err := c.validateBondRegs(b); err != nil {
		return err
	}
	if err := c.validateSlashes(b); err != nil {
		return err
	}
	seen := make(map[ports.Hash]bool)
	seenSerial := make(map[string]bool)
	for _, e := range b.Entries {
		if _, exists := c.byRoot[e.Root]; exists || seen[e.Root] {
			return fmt.Errorf("%w: %s", ErrDupRoot, e.Root)
		}
		if len(e.ManifestChunks) == 0 {
			return fmt.Errorf("chain: entry %s has no manifest pointers", e.Root)
		}
		// M0 privacy (#97): a Publisher→root record is permanent on this
		// append-only chain, so the default refuses it. Publish carries no
		// durable identity — a blind-signed token, or nothing — unless the
		// deployment is explicitly trusted (AllowPublisher).
		if !c.cfg.AllowPublisher && e.Publisher != (ports.NodeID{}) {
			return fmt.Errorf("%w: entry %s", ErrPublisherEntry, e.Root)
		}
		if c.tokenQuorum > 0 {
			if e.Token == nil {
				return fmt.Errorf("%w: entry %s", ErrTokenRequired, e.Root)
			}
			qualified := func(v ports.NodeID) bool { return c.attesterQualified(v) }
			if err := publishtoken.Verify(*e.Token, c.tokenQuorum, c.issuerKey, qualified); err != nil {
				return fmt.Errorf("chain: entry %s: %w", e.Root, err)
			}
			s := string(e.Token.Serial)
			if c.spent[s] || seenSerial[s] {
				return fmt.Errorf("%w: %x", ErrTokenSpent, e.Token.Serial)
			}
			seenSerial[s] = true
		}
		seen[e.Root] = true
	}
	return nil
}

// validateTakedowns enforces the accountability tenet on a block's revocation
// and un-revocation records (red-team F5): a revocation may only name a root
// this chain has already committed (no censoring content that isn't on the
// ledger), and an un-revocation may only name a root that is currently
// revoked. Called from both the attester pre-check (ValidateProposal) and the
// commit path (validateStructural) so a malicious quorum cannot slip either
// past. Roots published within the SAME block are not yet committed, so
// same-block revoke-what-you-publish is (correctly) refused as nonsensical.
func (c *Chain) validateTakedowns(b *Block) error {
	for _, r := range b.Revocations {
		if _, ok := c.byRoot[r]; !ok {
			return fmt.Errorf("%w: %s", ErrRevokeUnknownRoot, r)
		}
	}
	for _, r := range b.Unrevocations {
		if !c.revoked[r] {
			return fmt.Errorf("%w: %s", ErrUnrevokeNotRevoked, r)
		}
	}
	return nil
}

// ValidateCommit checks a full block: the proposal rules plus a quorum
// of distinct, qualified, non-proposer attestations.
func (c *Chain) ValidateCommit(b *Block) error {
	if err := c.ValidateProposal(b); err != nil {
		return err
	}
	h := b.Hash()
	seen := make(map[ports.NodeID]bool)
	valid := 0
	for _, a := range b.Atts {
		if len(a.PubKey) != ed25519.PublicKeySize {
			continue
		}
		id := a.AttesterID()
		if seen[id] || id == b.ProposerID() {
			continue // duplicates and self-attestation don't count
		}
		if !ed25519.Verify(ed25519.PublicKey(a.PubKey), h[:], a.Sig) {
			return fmt.Errorf("%w: attester %s", ErrBadSignature, id)
		}
		if !c.attesterQualified(id) {
			continue // unqualified signatures are ignored, not fatal
		}
		seen[id] = true
		valid++
	}
	if req := c.RequiredQuorum(); valid < req {
		return fmt.Errorf("%w: %d qualified, need %d", ErrNoQuorum, valid, req)
	}
	// Training wheels: while the network has NEVER YET matured, the quorum must
	// ALSO carry anchor sign-off, so a Sybil quorum can't capture a young network
	// before it has decentralized. Gated on the one-way latch (everMature), NOT the
	// live Mature() — so a later drop in decentralization (e.g. an honest whale
	// concentrating real bond) can never re-arm the anchors (F-1). Once matured,
	// de-maturation liveness is the real-bond super-quorum (RequiredQuorum), not this.
	if len(c.cfg.Anchors) > 0 && c.cfg.AnchorQuorum > 0 && !c.everMature {
		anchors := 0
		for id := range seen { // seen = the distinct qualified attesters
			if c.cfg.Anchors[id] {
				anchors++
			}
		}
		if anchors < c.cfg.AnchorQuorum {
			return fmt.Errorf("%w: %d of required %d", ErrAnchorRequired, anchors, c.cfg.AnchorQuorum)
		}
	}
	// De-maturation super-quorum (F-1, ships WITH the latch): once matured, the
	// anchors never re-arm — but if live decentralization has since dropped below the
	// bar (everMature && !matureNow, e.g. an honest whale concentrated real bond or
	// small bonds lapsed), a commit instead needs a real-bond SUPER-MAJORITY: ≥⅔ of
	// the LIVE bonded weight, no anchor sign-off. This is the center-less replacement
	// for the retired anchor net — it keeps liveness for a genuinely-willing real
	// quorum (the HALT horn stays dead) and preserves accountable safety (any two ⅔
	// super-quorums share > ⅓ of the weight, so they intersect in honest bond). In
	// the normal mature-and-still-decentralized case this is a no-op.
	if c.everMature && c.objective() && !c.matureNow() {
		if err := c.requireDeMatureSuperQuorum(b, seen); err != nil {
			return err
		}
	}
	return nil
}

// requireDeMatureSuperQuorum enforces the F-1 de-maturation rule: the committing
// coalition (proposer + the distinct qualified attesters `seen`) must control ≥⅔ of
// the live bonded weight. Only real committed bond counts (in the de-maturation
// window launchAnchor is false, so `seen` is bonded validators only). A pure function
// of the committed bonded set, so every replica agrees.
func (c *Chain) requireDeMatureSuperQuorum(b *Block, seen map[ports.NodeID]bool) error {
	var total int64
	for _, w := range c.bonded {
		total += w
	}
	if total <= 0 {
		return nil // no bonded weight to measure against (nothing to protect)
	}
	committed := c.bonded[b.ProposerID()]
	for id := range seen {
		committed += c.bonded[id]
	}
	need := (2*total + 2) / 3 // ⌈2·total/3⌉
	if committed < need {
		return fmt.Errorf("%w: coalition holds %d MiB of %d MiB bonded (need ≥%d MiB)",
			ErrDeMatureQuorum, committed>>20, total>>20, need>>20)
	}
	return nil
}

// Append validates and applies a committed block.
func (c *Chain) Append(b Block) error {
	if err := c.ValidateCommit(&b); err != nil {
		return err
	}
	c.apply(b)
	return nil
}

// Reload rebuilds this replica from THIS node's OWN persisted history — the
// genesis first, then each committed block — re-verifying every block's
// cryptographic integrity but NOT the live reputation gate (see
// appendStructural for why). It is how a restarted validator rejoins at its
// persisted height instead of being stranded at genesis by an empty reputation
// view (F1). Returns how many blocks were restored. A PEER's chain is a
// different trust class and still goes through Reconcile, which re-validates
// reputation in full — Reload is only ever fed our own disk.
func (c *Chain) Reload(blocks []Block) (int, error) {
	for i, b := range blocks {
		var err error
		if i == 0 && b.Height == 0 {
			err = c.AppendGenesis(b)
		} else {
			err = c.appendStructural(b)
		}
		if err != nil {
			return i, err
		}
	}
	return len(blocks), nil
}

// appendStructural re-applies a block from our own persisted history, verifying
// ancestry, the proposer signature, and a quorum of distinct, verifying,
// non-proposer attester signatures — everything a corrupt disk could break —
// but deliberately NOT the reputation gate. Reputation is a live, local,
// time-varying view that is EMPTY at boot (bond audits have not run yet); it is
// not an integrity property of the block, and re-gating our own already-
// committed history on it would strand a restarted validator at genesis (F1).
// Because the proposer and attester signatures cover the whole block hash, any
// tampering, bit-rot, or truncation is still caught (B7 — persisted state is
// re-verified on load, not trusted). What we skip is re-litigating a policy
// decision — proposer/attester qualification, publish-token and Publisher
// policy — that the quorum already made when this node committed the block.
func (c *Chain) appendStructural(b Block) error {
	if err := c.validateStructural(&b); err != nil {
		return err
	}
	c.apply(b)
	return nil
}

func (c *Chain) validateStructural(b *Block) error {
	prev, height := c.Head()
	if b.Height != height || b.Prev != prev {
		return fmt.Errorf("%w: got height %d prev %s, want height %d prev %s",
			ErrWrongParent, b.Height, b.Prev, height, prev)
	}
	if len(b.Proposer) != ed25519.PublicKeySize {
		return ErrBadSignature
	}
	h := b.Hash()
	if !ed25519.Verify(ed25519.PublicKey(b.Proposer), h[:], b.ProposerSig) {
		return fmt.Errorf("%w: proposer", ErrBadSignature)
	}
	if len(b.Entries) == 0 && len(b.Revocations) == 0 && len(b.Unrevocations) == 0 && len(b.BondRegs) == 0 && len(b.Slashes) == 0 {
		return errors.New("chain: empty block")
	}
	if err := c.validateTakedowns(b); err != nil {
		return err
	}
	if err := c.validateSlashes(b); err != nil {
		return err
	}
	seen := make(map[ports.NodeID]bool)
	valid := 0
	for _, a := range b.Atts {
		if len(a.PubKey) != ed25519.PublicKeySize {
			continue
		}
		id := a.AttesterID()
		if seen[id] || id == b.ProposerID() {
			continue // duplicates and self-attestation don't count
		}
		if !ed25519.Verify(ed25519.PublicKey(a.PubKey), h[:], a.Sig) {
			return fmt.Errorf("%w: attester %s", ErrBadSignature, id)
		}
		seen[id] = true
		valid++
	}
	if valid < c.cfg.Quorum {
		return fmt.Errorf("%w: %d valid, need %d", ErrNoQuorum, valid, c.cfg.Quorum)
	}
	return nil
}

// AppendGenesis seeds the height-0 founding block. Unlike every later
// block it needs NO quorum and NO proposer reputation — a genesis is
// accepted because it is identical on every node (declared, not agreed),
// exactly as Bitcoin's genesis has no predecessor to prove work against.
// It must be the first block, at height 0 with a zero parent, and its
// proposer signature must check out (so a corrupted genesis is caught).
func (c *Chain) AppendGenesis(b Block) error {
	if len(c.blocks) != 0 {
		return fmt.Errorf("chain: genesis must be the first block")
	}
	if b.Height != 0 || b.Prev != (ports.Hash{}) {
		return fmt.Errorf("chain: malformed genesis (height %d, non-zero prev?)", b.Height)
	}
	if len(b.Proposer) != ed25519.PublicKeySize {
		return ErrBadSignature
	}
	h := b.Hash()
	if !ed25519.Verify(ed25519.PublicKey(b.Proposer), h[:], b.ProposerSig) {
		return fmt.Errorf("%w: genesis proposer", ErrBadSignature)
	}
	if len(b.Entries) == 0 {
		return fmt.Errorf("chain: empty genesis")
	}
	// A genesis seeds entries (and declared launch bonds) — never takedowns.
	// AppendGenesis skips validateTakedowns (there is no prior history to check a
	// revocation against), so allowing Revocations here would let whoever controls
	// genesis PRE-EMPTIVELY revoke a never-published root, exactly what immutable
	// #5 forbids (red-team F3). Reject them outright; a takedown must go through
	// the governed normal path, where ErrRevokeUnknownRoot enforces existence.
	if len(b.Revocations) > 0 || len(b.Unrevocations) > 0 {
		return ErrGenesisTakedown
	}
	// The same door, for the STRONGER lever (retest G1). AppendGenesis skips
	// validateSlashes, and apply() unconditionally evicts every Slashes culprit
	// (slashed[id]=true, deleted from bonded, barred from re-earning, carried
	// through adopt()). A genesis carrying an UNVERIFIED Slash would therefore be
	// a proof-free, pre-emptive, identity-level kill switch — a fortiori what
	// immutable #5 forbids. A slash is only meaningful against equivocation
	// WITHIN this chain's history, of which a genesis has none, so genesis may
	// never carry one. Reject outright; a slash must go through the normal path,
	// where validateSlashes → VerifyEquivocation gates it on a real proof.
	if len(b.Slashes) > 0 {
		return ErrGenesisTakedown
	}
	c.apply(b)
	return nil
}

func (c *Chain) apply(b Block) {
	c.blocks = append(c.blocks, b)
	for _, e := range b.Entries {
		c.byRoot[e.Root] = e
		if e.Token != nil {
			c.spent[string(e.Token.Serial)] = true // serial is now spent chain-wide
		}
	}
	for _, r := range b.Revocations {
		c.revoked[r] = true
		c.revLog.Append(RevocationLeaf(RevOp, r, b.Height)) // append-only transparency record
	}
	// Un-revocations clear a prior takedown (validated as currently-revoked).
	// delete rather than set-false so the map stays a clean set and adopt()'s
	// pure-replay rebuild yields identical state.
	for _, r := range b.Unrevocations {
		delete(c.revoked, r)
		c.revLog.Append(RevocationLeaf(UnrevOp, r, b.Height)) // the reversal is logged too
	}
	// Record on-chain bond registrations (objective validator set, F6). A
	// height>0 registration was already VERIFIED by validateBondRegs (real
	// space-time proof); a genesis (height 0) registration is merely DECLARED.
	// The latest registration wins, so a validator can renew or resize.
	// PER-ROOT DEDUP (red-team F1): a bond Root credits AT MOST ONE identity — the
	// first to claim it. A later registration on an already-claimed root by a
	// DIFFERENT identity earns nothing, so a colluding operator cannot back N
	// Sybil standings off one shared plot. The first owner may re-register (renew
	// or resize) its own root freely.
	proven := b.Height > 0 // genesis regs are declared; height>0 went through validateBondRegs
	for _, r := range b.BondRegs {
		if len(r.Validator) != ed25519.PublicKeySize {
			continue
		}
		if r.Size < c.cfg.MinBondBytes {
			continue // below the objective anti-release floor → no standing (retest G4)
		}
		id := r.ValidatorID()
		if c.slashed[id] {
			continue // a slashed equivocator cannot re-earn bonded standing (F2)
		}
		if owner, claimed := c.bondRootOwner[r.Root]; claimed && owner != id {
			// PROOF BEATS DECLARATION (retest G3): a verified registration displaces
			// an unproven genesis-DECLARED claim, so a malicious genesis cannot
			// pre-squat an honest validator's real root and lock the true holder out
			// via the dedup above. Any other collision (proven-vs-proven, or an
			// unproven challenger) earns nothing, preserving F1.
			if !(proven && !c.bondRootProven[r.Root]) {
				continue // shared root already backs another identity → no standing
			}
			delete(c.bonded, owner) // strip the displaced squatter's unproven standing
		}
		c.bondRootOwner[r.Root] = id
		if proven {
			c.bondRootProven[r.Root] = true
		}
		c.bonded[id] = r.Size
		c.bondRegHeight[id] = b.Height // reset the TTL clock on every (re)registration (G4)
		c.bondDomain[id] = r.Domain    // committed A-axis label (0 = unset); latest wins
	}
	// OBJECTIVE RE-CHALLENGE (retest G4): standing lapses if not renewed with a
	// fresh proof within BondTTLBlocks. A validator that registers once and then
	// releases its plot cannot answer the fresh challenge to renew, so its vote
	// decays to nothing instead of persisting forever off a single one-time proof.
	// Height-driven, so every replica expires standing in lockstep.
	if ttl := c.cfg.BondTTLBlocks; ttl > 0 {
		for id, regH := range c.bondRegHeight {
			if b.Height-regH > ttl {
				delete(c.bonded, id)
				delete(c.bondRegHeight, id)
			}
		}
	}
	// Apply on-chain equivocation slashes (F2): evict the culprit from the
	// objective bonded set and bar it from re-earning standing. Verified already
	// (validateSlashes) on the write paths; genesis slashes are declared.
	for i := range b.Slashes {
		culprit := b.Slashes[i].CulpritID()
		c.slashed[culprit] = true
		delete(c.bonded, culprit)
	}
	// Track distinct qualified validators for the maturity metric — a
	// monotonic, chain-internal, auditable measure of decentralization.
	for _, a := range b.Atts {
		id := a.AttesterID()
		if id != b.ProposerID() && c.attesterQualified(id) {
			c.validatorsSeen[id] = true
		}
	}
	// Latch maturity (F-1): once the network is first certified mature, record it
	// permanently, so the launch anchors never re-arm. Checked AFTER this block's
	// bonds/slashes/TTL are applied, so it reflects the post-block bonded set.
	// Monotonic — only ever set, never cleared.
	if !c.everMature && c.Mature() {
		c.everMature = true
	}
}

// Weight is the chain's fork-choice weight: the cumulative count, over every
// block, of DISTINCT qualified non-proposer attestations. More real
// validators standing behind a history makes it heavier — so the heaviest
// chain is the one the most earned standing has committed to, not merely the
// longest (which a fast Sybil could extend). Signatures are objective; the
// qualification bar is the local reputation view, which converges among
// honest replicas. (Making the weight fully partition-independent — objective
// on-chain PoST-bond weight — is the recorded D2 hardening; see §3e.)
func (c *Chain) Weight() int64 {
	var w int64
	for i := range c.blocks {
		w += c.blockWeight(&c.blocks[i])
	}
	return w
}

// blockWeight is the fork-choice weight this block contributes. In OBJECTIVE
// mode (F6) it sums the on-chain bonded SIZE of each distinct, non-proposer
// attester whose signature verifies — a quantity every replica recomputes
// identically from the chain, so honest replicas can never disagree on which
// fork is heavier. In legacy mode it COUNTS those attesters, gated by the local
// reputation view (which is what could diverge under a partition). Either way it
// is the same rule ValidateCommit counts support by.
func (c *Chain) blockWeight(b *Block) int64 {
	h := b.Hash()
	seen := make(map[ports.NodeID]bool)
	var n int64
	for _, a := range b.Atts {
		if len(a.PubKey) != ed25519.PublicKeySize {
			continue
		}
		id := a.AttesterID()
		if seen[id] || id == b.ProposerID() {
			continue
		}
		if !ed25519.Verify(ed25519.PublicKey(a.PubKey), h[:], a.Sig) {
			continue
		}
		if !c.attesterQualified(id) {
			continue
		}
		seen[id] = true
		if c.objective() {
			n += c.bonded[id] // objective weight = summed on-chain bond
		} else {
			n++ // legacy weight = count of qualified attesters
		}
	}
	return n
}

// Reconcile heals a fork (D2): given a peer's full chain from genesis, it
// re-validates the whole thing in a throwaway replica and, iff that chain is
// strictly heavier than ours (ties broken by the lower head hash), ADOPTS it —
// rolling our replica back to the common genesis and forward onto the heavier
// history. A diverged node therefore stops being forked forever (the old
// SyncChain just `break`ed). The fork must share OUR genesis, so a peer cannot
// swap the chain out from under us with a heavier foreign history; and every
// block is fully re-validated, so a lying peer wastes our time but cannot feed
// us an invalid chain. Returns whether we adopted the fork.
func (c *Chain) Reconcile(fork []Block) (bool, error) {
	if len(fork) == 0 {
		return false, ErrEmptyFork
	}
	if len(c.blocks) == 0 {
		return false, ErrNoGenesis
	}
	if fork[0].Height != 0 || fork[0].Hash() != c.blocks[0].Hash() {
		return false, ErrForeignGenesis // must branch from our own genesis
	}
	// Weak-subjectivity guard (F-1): refuse — regardless of weight — any fork that does
	// not contain the trusted checkpoint block, i.e. that rewrites finalized history at
	// or before it. This is the long-range-attack defense that makes the maturity latch
	// safe for a fresh/long-offline node. Cheap and positional (fork blocks are
	// contiguous from genesis, so index == height; the block hash covers the height, so
	// a match pins the exact checkpoint block). Checked before the replay so a
	// long-range fork is rejected without doing the work.
	if cp := c.cfg.WSCheckpoint; cp.Height > 0 {
		if uint64(len(fork)) <= cp.Height || fork[cp.Height].Hash() != cp.Hash {
			return false, ErrPreCheckpointReorg
		}
	}
	// Re-validate the candidate history end to end in a fresh replica.
	tmp := New(c.cfg, c.rep)
	tmp.tokenQuorum, tmp.issuerKey = c.tokenQuorum, c.issuerKey
	tmp.verifyBond = c.verifyBond // so the fork's bond registrations re-verify (F6)
	if err := tmp.AppendGenesis(fork[0]); err != nil {
		return false, err
	}
	for i := 1; i < len(fork); i++ {
		if err := tmp.Append(fork[i]); err != nil {
			return false, fmt.Errorf("reconcile: fork block %d (height %d): %w", i, fork[i].Height, err)
		}
	}
	if !heavier(tmp, c) {
		return false, nil
	}
	c.adopt(tmp)
	return true, nil
}

// heavier reports whether chain a should win fork-choice over b: strictly more
// weight, or equal weight with a deterministic lower-head-hash tiebreak so
// every honest node picks the same winner.
func heavier(a, b *Chain) bool {
	wa, wb := a.Weight(), b.Weight()
	if wa != wb {
		return wa > wb
	}
	ha, hb := a.blocks[len(a.blocks)-1].Hash(), b.blocks[len(b.blocks)-1].Hash()
	return bytesLess(ha[:], hb[:])
}

func bytesLess(a, b []byte) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

// adopt replaces this replica's state with a reconciled fork's. Because all
// derived state (byRoot, spent, revoked, validatorsSeen) is a pure function of
// the blocks, swapping the whole precomputed set is the reorg — no fragile
// per-record undo.
func (c *Chain) adopt(t *Chain) {
	c.blocks = t.blocks
	c.byRoot = t.byRoot
	c.revoked = t.revoked
	c.revLog = t.revLog
	c.validatorsSeen = t.validatorsSeen
	c.spent = t.spent
	c.bonded = t.bonded
	c.bondRootOwner = t.bondRootOwner
	c.bondRootProven = t.bondRootProven
	c.bondRegHeight = t.bondRegHeight
	c.bondDomain = t.bondDomain
	c.slashed = t.slashed
	c.everMature = t.everMature // the maturity latch is a function of the adopted history (F-1)
}

func (c *Chain) LookupRoot(root ports.Hash) (ports.Entry, bool) {
	e, ok := c.byRoot[root]
	return e, ok
}

func (c *Chain) AllEntries() []ports.Entry {
	var out []ports.Entry
	for _, b := range c.blocks {
		out = append(out, b.Entries...)
	}
	return out
}
