package chainstore

import (
	"crypto/ed25519"
	"crypto/sha256"
	"errors"
	"math/rand"
	"path/filepath"
	"testing"

	"github.com/nerolabs/silt/adapters/memstore"
	"github.com/nerolabs/silt/core/chain"
	"github.com/nerolabs/silt/core/genesis"
	"github.com/nerolabs/silt/ports"
)

func testKey(seed int64) ed25519.PrivateKey {
	var b [ed25519.SeedSize]byte
	rand.New(rand.NewSource(seed)).Read(b[:])
	return ed25519.NewKeyFromSeed(b[:])
}

func idOf(priv ed25519.PrivateKey) ports.NodeID {
	return sha256.Sum256(priv.Public().(ed25519.PublicKey))
}

// committedChain builds a real [genesis, block1] history using a reputation
// view in which the proposer and attesters are fully qualified — i.e. the
// state a validator holds at the moment it commits and persists a block.
func committedChain(t *testing.T, rep func(ports.NodeID) int64) *chain.Chain {
	t.Helper()
	cfg := chain.DefaultConfig() // MinProposerRep/MinAttesterRep = 100, Quorum = 3
	c := chain.New(cfg, rep)

	gb, _, _, err := genesis.Build(memstore.New())
	if err != nil {
		t.Fatalf("genesis build: %v", err)
	}
	if err := c.AppendGenesis(gb); err != nil {
		t.Fatalf("append genesis: %v", err)
	}

	prop := testKey(1)
	prev, height := c.Head()
	b := &chain.Block{
		Version: chain.BlockVersion, Height: height, Prev: prev,
		Entries: []ports.Entry{{
			Root:           ports.HashBytes([]byte{7}),
			ManifestChunks: []ports.ChunkID{ports.HashBytes([]byte{7, 7})},
			FileSize:       700,
		}},
	}
	chain.Sign(b, prop)
	for i := int64(2); i <= 5; i++ { // four distinct attesters clear a quorum of 3
		b.Atts = append(b.Atts, chain.Attest(b, testKey(i)))
	}
	if err := c.Append(*b); err != nil {
		t.Fatalf("commit block 1: %v", err)
	}
	return c
}

// TestReplayReloadsOwnChainWithEmptyLedger is the F1 regression test: a
// restarted validator reloads its OWN persisted chain before any bond audit
// has re-established reputation, so its reputation view is empty. Reload must
// still rejoin at the persisted height (the "rejoin at its height, not from
// genesis" promise) — reputation is a live, time-varying view, NOT a
// corruption check, so re-gating our own committed history on it strands the
// node at genesis. Structural integrity (hashes + signatures) is still
// verified; only the reputation gate is skipped for our own disk.
func TestReplayReloadsOwnChainWithEmptyLedger(t *testing.T) {
	// Commit-time reputation: everyone the block needs is qualified.
	fullRep := map[ports.NodeID]int64{idOf(genesis.Key()): 0} // genesis earns none, by design
	for i := int64(1); i <= 5; i++ {
		fullRep[idOf(testKey(i))] = 1000
	}
	c := committedChain(t, func(n ports.NodeID) int64 { return fullRep[n] })
	if c.Len() != 2 {
		t.Fatalf("setup: want 2 blocks, got %d", c.Len())
	}

	path := filepath.Join(t.TempDir(), "chain.cbor")
	if err := Save(path, c.Blocks(0)); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Restart: a brand-new replica whose reputation view is EMPTY (bond audits
	// have not run yet). This is the exact daemon boot state.
	fresh := chain.New(chain.DefaultConfig(), func(ports.NodeID) int64 { return 0 })
	n, err := Replay(path, fresh)
	if err != nil {
		t.Fatalf("replay of own chain with empty ledger failed: %v", err)
	}
	if n != 2 || fresh.Len() != 2 {
		t.Fatalf("want 2 blocks reloaded, got n=%d len=%d", n, fresh.Len())
	}
	wantHash, wantHeight := c.Head()
	gotHash, gotHeight := fresh.Head()
	if wantHash != gotHash || wantHeight != gotHeight {
		t.Fatalf("reloaded head diverged: want %x@%d, got %x@%d", wantHash, wantHeight, gotHash, gotHeight)
	}
}

// TestReplayStillDetectsCorruption proves the reload does NOT blindly trust the
// disk: a tampered block (any content change breaks the proposer signature over
// the block hash) must still be rejected, even with the reputation gate skipped.
// This is B7's real intent — catch bit-rot/truncation/tampering, not re-litigate
// a policy decision the quorum already made.
func TestReplayStillDetectsCorruption(t *testing.T) {
	fullRep := map[ports.NodeID]int64{}
	for i := int64(1); i <= 5; i++ {
		fullRep[idOf(testKey(i))] = 1000
	}
	c := committedChain(t, func(n ports.NodeID) int64 { return fullRep[n] })
	blocks := c.Blocks(0)

	// Corrupt block 1's payload without re-signing: the proposer signature no
	// longer covers these bytes.
	blocks[1].Entries[0].FileSize = 999999

	path := filepath.Join(t.TempDir(), "chain.cbor")
	if err := Save(path, blocks); err != nil {
		t.Fatalf("save: %v", err)
	}
	fresh := chain.New(chain.DefaultConfig(), func(ports.NodeID) int64 { return 0 })
	if _, err := Replay(path, fresh); err == nil {
		t.Fatal("replay accepted a tampered block; corruption must be rejected")
	} else if !errors.Is(err, chain.ErrBadSignature) {
		t.Fatalf("want ErrBadSignature on tamper, got %v", err)
	}
}
