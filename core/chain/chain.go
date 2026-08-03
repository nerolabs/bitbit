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
	"errors"
	"fmt"

	"github.com/fxamacker/cbor/v2"

	"github.com/nerolabs/silt/core/publishtoken"
	"github.com/nerolabs/silt/ports"
)

// Config sets the consensus thresholds. Zero MinRep values make a
// permissive chain (useful for small trusted deployments and demos);
// the sim exercises the strict settings.
type Config struct {
	MinProposerRep int64
	MinAttesterRep int64
	Quorum         int // attestations required, excluding the proposer
	// Launch-window "training wheels" (risk 15): while the network is
	// immature — fewer than MatureValidators DISTINCT non-anchor qualified
	// validators have ever attested a committed block — a commit ALSO needs
	// AnchorQuorum attestations from Anchors (a declared seed set), so a
	// Sybil quorum can't capture a young network before it decentralizes.
	// The wheels shed MECHANICALLY on that measured decentralization, never a
	// flag day. Anchors are plural and require a threshold, so no single
	// anchor is load-bearing (cf. R4) — and while they can gate publication
	// on the young network (a transparent, on-chain, time-limited power),
	// they can never do so once mature. Empty Anchors / zero AnchorQuorum =
	// no training wheels (the default; trusted/sim deployments).
	Anchors          map[ports.NodeID]bool
	AnchorQuorum     int
	MatureValidators int
	// AllowPublisher permits an entry to carry a durable Publisher NodeID.
	// It is FALSE by default because a Publisher→root record is permanent
	// on an append-only chain — the M0 privacy corner silently surrendered
	// in the historical record (F1/#14, #97). The unlinkable path (a
	// blind-signed publish token, or no identity at all) is the default;
	// only an explicitly trusted deployment opts back into Publisher
	// entries. Genesis is exempt (it seeds via AppendGenesis, and its
	// proposer is public by design).
	AllowPublisher bool
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
	// Revocations are append-only takedown records: opaque roots that
	// compliant nodes must no-op on. Deletion is impossible on an
	// immutable chain, so takedown is an ADDITION — a tombstone — that
	// replicates and is tamper-evident like any other block.
	Revocations []ports.Hash `cbor:"7,keyasint,omitempty"`
	// Version is the block's rule era (see BlockVersion). Committed by Hash
	// and required at decode; every minted block sets it.
	Version uint64 `cbor:"8,keyasint"`
}

// Attestation is a validator's signature over the block hash. The
// public key rides along because a NodeID (its hash) can't be inverted.
type Attestation struct {
	PubKey []byte `cbor:"1,keyasint"`
	Sig    []byte `cbor:"2,keyasint"`
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
// proposer. Signing the hash therefore signs the block's entire
// content and its place in history.
func (b *Block) Hash() ports.Hash {
	unsigned := Block{Version: b.Version, Height: b.Height, Prev: b.Prev, Entries: b.Entries, Proposer: b.Proposer, Revocations: b.Revocations}
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
	ErrLowReputation  = errors.New("chain: reputation below threshold")
	ErrNoQuorum       = errors.New("chain: insufficient valid attestations")
	ErrBadSignature   = errors.New("chain: bad signature")
	ErrWrongParent    = errors.New("chain: block does not extend the local head")
	ErrDupRoot        = errors.New("chain: root already registered")
	ErrUseConsensus   = errors.New("chain: replica is read-only; entries are committed via consensus")
	ErrAnchorRequired = errors.New("chain: immature network requires anchor attestations (training wheels)")
	ErrTokenRequired  = errors.New("chain: entry has no publish token (required)")
	ErrTokenSpent     = errors.New("chain: publish token serial already spent (double-spend)")
	ErrBlockVersion   = errors.New("chain: unsupported block version")
	ErrPublisherEntry = errors.New("chain: entry carries a durable Publisher (records permanent linkage; publish unlinkably or run an explicitly trusted deployment)")
	ErrEmptyFork      = errors.New("chain: cannot reconcile an empty fork")
	ErrNoGenesis      = errors.New("chain: local replica has no genesis to anchor a reconcile")
	ErrForeignGenesis = errors.New("chain: fork does not share our genesis (refusing to swap chains)")
)

// Chain is a validator's replica plus the rules for growing it.
type Chain struct {
	cfg     Config
	rep     func(ports.NodeID) int64
	blocks  []Block
	byRoot  map[ports.Hash]ports.Entry
	revoked map[ports.Hash]bool
	// validatorsSeen is the set of distinct qualified attesters that have
	// ever committed a block — the monotonic decentralization signal the
	// training wheels shed on (see Mature).
	validatorsSeen map[ports.NodeID]bool
	// Publisher-privacy publish tokens (F1): when tokenQuorum > 0 every entry
	// must carry a PublishToken blind-signed by that many distinct qualified
	// validators (issuer keys from issuerKey), and spent records each serial so
	// it can be spent only once (double-spend rejected chain-wide).
	tokenQuorum int
	issuerKey   func(ports.NodeID) *rsa.PublicKey
	spent       map[string]bool
}

// New starts an empty replica. rep is the local reputation view —
// validators judge proposals by what THEY have observed (audits run,
// serves seen), which is the sense in which trust here is earned, not
// declared.
func New(cfg Config, rep func(ports.NodeID) int64) *Chain {
	return &Chain{cfg: cfg, rep: rep,
		byRoot:         make(map[ports.Hash]ports.Entry),
		revoked:        make(map[ports.Hash]bool),
		validatorsSeen: make(map[ports.NodeID]bool),
		spent:          make(map[string]bool)}
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

// Mature reports whether the network has decentralized enough for the
// launch-window anchors to no longer be required: at least MatureValidators
// DISTINCT non-anchor qualified validators have attested a committed block.
// Because attesting requires earned standing (a storage bond, #78), this
// count can't be cheaply inflated by Sybils — maturing early costs real bonds.
func (c *Chain) Mature() bool {
	if c.cfg.MatureValidators <= 0 {
		return true
	}
	n := 0
	for id := range c.validatorsSeen {
		if !c.cfg.Anchors[id] {
			n++
		}
	}
	return n >= c.cfg.MatureValidators
}

// Revoked reports whether root has been taken down by a committed
// revocation record.
func (c *Chain) Revoked(root ports.Hash) bool { return c.revoked[root] }

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
	if got := c.rep(b.ProposerID()); got < c.cfg.MinProposerRep {
		return fmt.Errorf("%w: proposer %s has %d, needs %d",
			ErrLowReputation, b.ProposerID(), got, c.cfg.MinProposerRep)
	}
	if len(b.Entries) == 0 && len(b.Revocations) == 0 {
		return errors.New("chain: empty block")
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
			qualified := func(v ports.NodeID) bool { return c.rep(v) >= c.cfg.MinAttesterRep }
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
		if c.rep(id) < c.cfg.MinAttesterRep {
			continue // unqualified signatures are ignored, not fatal
		}
		seen[id] = true
		valid++
	}
	if valid < c.cfg.Quorum {
		return fmt.Errorf("%w: %d qualified, need %d", ErrNoQuorum, valid, c.cfg.Quorum)
	}
	// Training wheels: while immature, the quorum must ALSO carry anchor
	// sign-off, so a Sybil quorum can't capture a young network before it has
	// decentralized. Sheds automatically once the network is Mature.
	if len(c.cfg.Anchors) > 0 && c.cfg.AnchorQuorum > 0 && !c.Mature() {
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
	if len(b.Entries) == 0 && len(b.Revocations) == 0 {
		return errors.New("chain: empty block")
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
	}
	// Track distinct qualified validators for the maturity metric — a
	// monotonic, chain-internal, auditable measure of decentralization.
	for _, a := range b.Atts {
		id := a.AttesterID()
		if id != b.ProposerID() && c.rep(id) >= c.cfg.MinAttesterRep {
			c.validatorsSeen[id] = true
		}
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

// blockWeight counts the distinct, qualified, non-proposer attesters whose
// signatures verify over this block — the same rule ValidateCommit counts a
// quorum by, so a committed block's weight is exactly its qualified support.
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
		if c.rep(id) < c.cfg.MinAttesterRep {
			continue
		}
		seen[id] = true
		n++
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
	// Re-validate the candidate history end to end in a fresh replica.
	tmp := New(c.cfg, c.rep)
	tmp.tokenQuorum, tmp.issuerKey = c.tokenQuorum, c.issuerKey
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
	c.validatorsSeen = t.validatorsSeen
	c.spent = t.spent
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
