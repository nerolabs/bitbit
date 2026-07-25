package genesis_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/nerolabs/bitbit/adapters/memstore"
	"github.com/nerolabs/bitbit/core/chain"
	"github.com/nerolabs/bitbit/core/genesis"
	"github.com/nerolabs/bitbit/core/pipeline"
	"github.com/nerolabs/bitbit/ports"
)

// The load-bearing property: every node, building genesis independently
// from the embedded manifesto, gets the byte-identical block and link.
// That is what lets genesis be "declared, not agreed".
func TestGenesisIsDeterministic(t *testing.T) {
	b1, h1, _, err := genesis.Build(memstore.New())
	if err != nil {
		t.Fatal(err)
	}
	b2, h2, _, err := genesis.Build(memstore.New())
	if err != nil {
		t.Fatal(err)
	}
	if b1.Hash() != b2.Hash() {
		t.Fatal("genesis block hash differs between builds — not deterministic")
	}
	if h1 != h2 {
		t.Fatal("genesis link differs between builds")
	}
	if b1.Height != 0 {
		t.Fatalf("genesis height %d, want 0", b1.Height)
	}
}

// A fresh chain seeded with genesis has the manifesto at height 0 with
// ZERO reputation available — proving genesis bypasses the quorum gate —
// and real blocks then build at height 1.
func TestSeedsChainAtHeightZeroWithoutQuorum(t *testing.T) {
	block, _, _, err := genesis.Build(memstore.New())
	if err != nil {
		t.Fatal(err)
	}
	c := chain.New(chain.DefaultConfig(), func(ports.NodeID) int64 { return 0 })
	if err := c.AppendGenesis(block); err != nil {
		t.Fatalf("genesis rejected despite zero reputation: %v", err)
	}
	if c.Len() != 1 {
		t.Fatalf("chain length %d after genesis, want 1", c.Len())
	}
	_, nextHeight := c.Head()
	if nextHeight != 1 {
		t.Fatalf("next block height %d, want 1", nextHeight)
	}
	// A second genesis is refused.
	if err := c.AppendGenesis(block); err == nil {
		t.Fatal("second genesis must be rejected")
	}
	// A tampered genesis fails its signature check.
	bad := block
	bad.Entries = nil
	fresh := chain.New(chain.DefaultConfig(), func(ports.NodeID) int64 { return 0 })
	if err := fresh.AppendGenesis(bad); err == nil {
		t.Fatal("tampered genesis must be rejected")
	}
}

// The genesis file is retrievable: the manifesto comes back bit-perfect
// from the seeded store using the genesis link.
func TestGenesisFileRetrievable(t *testing.T) {
	store := memstore.New()
	block, h, _, err := genesis.Build(store)
	if err != nil {
		t.Fatal(err)
	}
	reg := oneEntryReg{entry: block.Entries[0]}
	var out bytes.Buffer
	if err := pipeline.Get(context.Background(), store, reg, h, &out); err != nil {
		t.Fatalf("retrieving genesis file: %v", err)
	}
	if !bytes.Equal(out.Bytes(), genesis.Manifesto) {
		t.Fatal("retrieved genesis file does not match the manifesto")
	}
}

// oneEntryReg is a minimal read-only registry holding just the genesis
// entry, so pipeline.Get can resolve the manifest.
type oneEntryReg struct{ entry ports.Entry }

func (r oneEntryReg) Publish(context.Context, ports.Entry) error { return nil }
func (r oneEntryReg) Lookup(_ context.Context, root ports.Hash) (ports.Entry, bool, error) {
	if root == r.entry.Root {
		return r.entry, true, nil
	}
	return ports.Entry{}, false, nil
}
func (r oneEntryReg) All(context.Context) ([]ports.Entry, error) {
	return []ports.Entry{r.entry}, nil
}
