package chain

import (
	"crypto/ed25519"
	"crypto/sha256"
	"errors"
	"math/rand"
	"testing"

	"github.com/nerolabs/silt/ports"
)

func key(seed int64) ed25519.PrivateKey {
	var b [ed25519.SeedSize]byte
	rand.New(rand.NewSource(seed)).Read(b[:])
	return ed25519.NewKeyFromSeed(b[:])
}

func idOf(priv ed25519.PrivateKey) ports.NodeID {
	return sha256.Sum256(priv.Public().(ed25519.PublicKey))
}

func entry(b byte) ports.Entry {
	return ports.Entry{
		Root:           ports.HashBytes([]byte{b}),
		ManifestChunks: []ports.ChunkID{ports.HashBytes([]byte{b, b})},
		FileSize:       int64(b) * 100,
	}
}

// A world with 1 proposer and 4 validators, reputations settable.
type world struct {
	c    *Chain
	reps map[ports.NodeID]int64
	prop ed25519.PrivateKey
	vals []ed25519.PrivateKey
}

func newWorld(cfg Config) *world {
	w := &world{reps: make(map[ports.NodeID]int64), prop: key(1)}
	for i := int64(2); i <= 5; i++ {
		w.vals = append(w.vals, key(i))
	}
	w.c = New(cfg, func(n ports.NodeID) int64 { return w.reps[n] })
	w.reps[idOf(w.prop)] = 1000
	for _, v := range w.vals {
		w.reps[idOf(v)] = 1000
	}
	return w
}

func (w *world) block(entries ...ports.Entry) *Block {
	prev, height := w.c.Head()
	b := &Block{Version: BlockVersion, Height: height, Prev: prev, Entries: entries}
	Sign(b, w.prop)
	return b
}

func (w *world) attestAll(b *Block) {
	for _, v := range w.vals {
		b.Atts = append(b.Atts, Attest(b, v))
	}
}

func TestCommitAndReplay(t *testing.T) {
	w := newWorld(DefaultConfig())
	b1 := w.block(entry(1), entry(2))
	w.attestAll(b1)
	if err := w.c.Append(*b1); err != nil {
		t.Fatal(err)
	}
	b2 := w.block(entry(3))
	w.attestAll(b2)
	if err := w.c.Append(*b2); err != nil {
		t.Fatal(err)
	}
	if e, ok := w.c.LookupRoot(entry(2).Root); !ok || e.FileSize != 200 {
		t.Fatal("committed entry not found")
	}
	if len(w.c.AllEntries()) != 3 || w.c.Len() != 2 {
		t.Fatalf("entries=%d blocks=%d", len(w.c.AllEntries()), w.c.Len())
	}

	// A fresh replica replays the same blocks to the same state —
	// replication is just re-validation.
	w2 := newWorld(DefaultConfig())
	w2.reps = w.reps // same reputation view
	w2.c = New(DefaultConfig(), func(n ports.NodeID) int64 { return w.reps[n] })
	for _, blk := range w.c.Blocks(0) {
		if err := w2.c.Append(blk); err != nil {
			t.Fatalf("replay: %v", err)
		}
	}
	h1, n1 := w.c.Head()
	h2, n2 := w2.c.Head()
	if h1 != h2 || n1 != n2 {
		t.Fatal("replicas diverged after replaying identical blocks")
	}
}

func TestLowReputationProposerRejected(t *testing.T) {
	w := newWorld(DefaultConfig())
	w.reps[idOf(w.prop)] = 5 // a nobody
	b := w.block(entry(1))
	if err := w.c.ValidateProposal(b); !errors.Is(err, ErrLowReputation) {
		t.Fatalf("want ErrLowReputation, got %v", err)
	}
}

func TestQuorumRules(t *testing.T) {
	w := newWorld(DefaultConfig()) // quorum 3

	// Too few attestations.
	b := w.block(entry(1))
	b.Atts = []Attestation{Attest(b, w.vals[0]), Attest(b, w.vals[1])}
	if err := w.c.Append(*b); !errors.Is(err, ErrNoQuorum) {
		t.Fatalf("2 of 3: want ErrNoQuorum, got %v", err)
	}
	// Duplicates don't stack.
	b.Atts = []Attestation{Attest(b, w.vals[0]), Attest(b, w.vals[0]), Attest(b, w.vals[0])}
	if err := w.c.Append(*b); !errors.Is(err, ErrNoQuorum) {
		t.Fatalf("duplicates: want ErrNoQuorum, got %v", err)
	}
	// Self-attestation doesn't count.
	b.Atts = []Attestation{Attest(b, w.prop), Attest(b, w.vals[0]), Attest(b, w.vals[1])}
	if err := w.c.Append(*b); !errors.Is(err, ErrNoQuorum) {
		t.Fatalf("self-attest: want ErrNoQuorum, got %v", err)
	}
	// Low-rep attesters don't count.
	w.reps[idOf(w.vals[2])] = 0
	b.Atts = []Attestation{Attest(b, w.vals[0]), Attest(b, w.vals[1]), Attest(b, w.vals[2])}
	if err := w.c.Append(*b); !errors.Is(err, ErrNoQuorum) {
		t.Fatalf("low-rep attester: want ErrNoQuorum, got %v", err)
	}
	// Exactly quorum commits.
	w.reps[idOf(w.vals[2])] = 1000
	b.Atts = []Attestation{Attest(b, w.vals[0]), Attest(b, w.vals[1]), Attest(b, w.vals[2])}
	if err := w.c.Append(*b); err != nil {
		t.Fatalf("exact quorum: %v", err)
	}
}

func TestTamperAndForgery(t *testing.T) {
	w := newWorld(DefaultConfig())
	b := w.block(entry(1))
	w.attestAll(b)

	// Tampering with entries after signing breaks everything.
	tampered := *b
	tampered.Entries = []ports.Entry{entry(9)}
	if err := w.c.Append(tampered); err == nil {
		t.Fatal("tampered block must not commit")
	}
	// A forged attestation (sig from a different block) is fatal.
	other := w.block(entry(7))
	forged := *b
	forged.Atts = append([]Attestation(nil), b.Atts[:2]...)
	forged.Atts = append(forged.Atts, Attest(other, w.vals[3]))
	if err := w.c.Append(forged); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("forged attestation: want ErrBadSignature, got %v", err)
	}
}

func TestDupRootAndWrongParent(t *testing.T) {
	w := newWorld(DefaultConfig())
	b := w.block(entry(1))
	w.attestAll(b)
	if err := w.c.Append(*b); err != nil {
		t.Fatal(err)
	}
	dup := w.block(entry(1))
	if err := w.c.ValidateProposal(dup); !errors.Is(err, ErrDupRoot) {
		t.Fatalf("want ErrDupRoot, got %v", err)
	}
	stale := &Block{Version: BlockVersion, Height: 0, Prev: ports.Hash{}, Entries: []ports.Entry{entry(2)}}
	Sign(stale, w.prop)
	if err := w.c.ValidateProposal(stale); !errors.Is(err, ErrWrongParent) {
		t.Fatalf("want ErrWrongParent, got %v", err)
	}
}

// M0 privacy (#97): a default chain refuses an entry that carries a durable
// Publisher — a permanent Publisher→root link on an append-only chain is the
// privacy corner silently surrendered. An unlinkable entry (no Publisher)
// commits; only an explicitly trusted deployment (AllowPublisher) accepts
// Publisher-bearing entries.
func TestDefaultChainRefusesPublisherEntry(t *testing.T) {
	w := newWorld(DefaultConfig())

	linked := entry(1)
	linked.Publisher = ports.NodeID{9, 9, 9} // a durable identity
	b := w.block(linked)
	w.attestAll(b)
	if err := w.c.ValidateCommit(b); !errors.Is(err, ErrPublisherEntry) {
		t.Fatalf("default chain accepted a Publisher-bearing entry, want ErrPublisherEntry, got %v", err)
	}

	// The unlinkable entry (no Publisher) commits on the same default chain.
	clean := w.block(entry(2))
	w.attestAll(clean)
	if err := w.c.Append(*clean); err != nil {
		t.Fatalf("default chain refused an unlinkable entry: %v", err)
	}
}

// A trusted deployment may opt back into Publisher entries.
func TestTrustedChainAllowsPublisherEntry(t *testing.T) {
	cfg := DefaultConfig()
	cfg.AllowPublisher = true
	w := newWorld(cfg)

	linked := entry(1)
	linked.Publisher = ports.NodeID{9, 9, 9}
	b := w.block(linked)
	w.attestAll(b)
	if err := w.c.Append(*b); err != nil {
		t.Fatalf("trusted chain refused a Publisher entry it should allow: %v", err)
	}
}

func TestEncodeDecodeRoundtrip(t *testing.T) {
	w := newWorld(DefaultConfig())
	b := w.block(entry(1), entry(2))
	w.attestAll(b)
	got, err := Decode(Encode(b))
	if err != nil {
		t.Fatal(err)
	}
	if got.Hash() != b.Hash() || len(got.Atts) != len(b.Atts) {
		t.Fatal("block mangled by encode/decode")
	}
	blocks, err := DecodeBlocks(EncodeBlocks([]Block{*b}))
	if err != nil || len(blocks) != 1 || blocks[0].Hash() != b.Hash() {
		t.Fatalf("blocks roundtrip: %v", err)
	}
}

// The hard-fork guard (#98): a block minted under a different rule era must
// be refused at decode, not silently decoded and mis-validated under this
// era's rules. This is the whole point of the version field.
func TestDecodeRefusesForeignBlockVersion(t *testing.T) {
	w := newWorld(DefaultConfig())
	b := w.block(entry(1))
	w.attestAll(b)

	// A block from a future era (same bytes otherwise) round-trips through
	// Encode but must be rejected by Decode.
	future := *b
	future.Version = BlockVersion + 1
	if _, err := Decode(Encode(&future)); !errors.Is(err, ErrBlockVersion) {
		t.Fatalf("Decode accepted a foreign version, want ErrBlockVersion, got %v", err)
	}
	// A pre-version block (Version 0, as legacy bytes would decode) is
	// equally refused — absence is not "current".
	legacy := *b
	legacy.Version = 0
	if _, err := Decode(Encode(&legacy)); !errors.Is(err, ErrBlockVersion) {
		t.Fatalf("Decode accepted a version-0 block, want ErrBlockVersion, got %v", err)
	}
	// DecodeBlocks refuses a batch if any block is a foreign version.
	if _, err := DecodeBlocks(EncodeBlocks([]Block{*b, future})); !errors.Is(err, ErrBlockVersion) {
		t.Fatalf("DecodeBlocks accepted a batch with a foreign version, got %v", err)
	}

	// Version is committed by Hash: two blocks differing only in Version
	// have different hashes, so an attacker can't swap the era under a
	// valid signature.
	if b.Hash() == future.Hash() {
		t.Fatal("Hash does not cover Version — the era could be swapped under a signature")
	}
}

// A committed revocation is append-only takedown: it commits only by
// quorum (like any block), marks the root revoked, and makes the chain's
// registry no-op on it — while the immutable record of the original
// publication remains.
func TestRevocationByQuorum(t *testing.T) {
	w := newWorld(DefaultConfig())
	b1 := w.block(entry(1), entry(2))
	w.attestAll(b1)
	if err := w.c.Append(*b1); err != nil {
		t.Fatal(err)
	}
	if w.c.Revoked(entry(1).Root) {
		t.Fatal("nothing revoked yet")
	}

	// A revocation-only block at the next height takes down entry(1).
	prev, height := w.c.Head()
	rev := &Block{Version: BlockVersion, Height: height, Prev: prev, Revocations: []ports.Hash{entry(1).Root}}
	Sign(rev, w.prop)

	// Too few attestations: a lone actor cannot take content down.
	rev.Atts = []Attestation{Attest(rev, w.vals[0])}
	if err := w.c.Append(*rev); err == nil {
		t.Fatal("revocation must need quorum, not one signer")
	}
	// With quorum it commits.
	w.attestAll(rev)
	if err := w.c.Append(*rev); err != nil {
		t.Fatal(err)
	}
	if !w.c.Revoked(entry(1).Root) {
		t.Fatal("root should be revoked after quorum revocation")
	}
	// The original publication record still exists (append-only): the
	// ledger remembers, the availability layer forgets.
	if _, ok := w.c.LookupRoot(entry(1).Root); !ok {
		t.Fatal("the immutable publication record must persist")
	}
	// But the registry view no-ops on it.
	reg := ReplicaRegistry{C: w.c}
	if _, ok, _ := reg.Lookup(nil, entry(1).Root); ok {
		t.Fatal("revoked root must be unresolvable via the registry")
	}
	// A non-revoked entry is unaffected.
	if _, ok, _ := reg.Lookup(nil, entry(2).Root); !ok {
		t.Fatal("non-revoked entry must still resolve")
	}
}
