package node

import (
	"crypto/ed25519"
	"testing"

	"github.com/nerolabs/silt/adapters/identity"
	"github.com/nerolabs/silt/adapters/memstore"
	"github.com/nerolabs/silt/adapters/simclock"
	"github.com/nerolabs/silt/adapters/simnet"
	"github.com/nerolabs/silt/core/chain"
	"github.com/nerolabs/silt/core/credit"
	"github.com/nerolabs/silt/ports"
)

func mkEntry(name string) ports.Entry {
	return ports.Entry{
		Root:           ports.HashBytes([]byte(name)),
		ManifestChunks: []ports.ChunkID{ports.HashBytes([]byte(name + "/m"))},
	}
}

// When a node reconciles across a fork, any validator who signed a different
// block at the same height on BOTH sides is a proven equivocator and is
// slashed in the local ledger — a double-sign costs standing (D2).
func TestReconcileSlashesEquivocator(t *testing.T) {
	sched := simclock.New()
	net := simnet.New(sched, 1, simnet.DefaultConfig())
	ledger := credit.New(50_000, 0)

	ndi := identity.FromSeed(1)
	nd := New(ndi.NodeID(), DefaultConfig(), sched, net.Endpoint(ndi.NodeID()), memstore.New())
	nd.SetLedger(ledger)

	prop := identity.FromSeed(2).Signer()
	v := identity.FromSeed(3)     // the double-signer we assert on
	other := identity.FromSeed(4) // also equivocates in this synthetic setup

	g := &chain.Block{Version: chain.BlockVersion, Height: 0, Entries: []ports.Entry{mkEntry("g")}}
	chain.Sign(g, prop)
	fork := func(name string, atts ...ed25519.PrivateKey) *chain.Block {
		b := &chain.Block{Version: chain.BlockVersion, Height: 1, Prev: g.Hash(), Entries: []ports.Entry{mkEntry(name)}}
		chain.Sign(b, prop)
		for _, a := range atts {
			b.Atts = append(b.Atts, chain.Attest(b, a))
		}
		return b
	}
	a := fork("A", v.Signer(), other.Signer())
	b := fork("B", v.Signer(), other.Signer())

	// Give V real standing, then reconcile-detect its double-sign.
	vid := v.NodeID()
	ledger.RecordBondChallenge(vid, ports.HashBytes(vid[:]), 64<<20, true, 1)
	if ledger.Reputation(v.NodeID()) <= 0 {
		t.Fatal("setup: V should have earned standing")
	}

	nd.slashEquivocators([]chain.Block{*g, *a}, []chain.Block{*g, *b})

	if ledger.Reputation(v.NodeID()) >= 0 {
		t.Fatal("a cross-fork double-signer must be slashed below zero")
	}
}

// An honest validator that already attested a block at a height REFUSES to
// attest a different block at that height — it never equivocates, even when two
// competing proposals reach it before either commits.
func TestValidatorRefusesToEquivocate(t *testing.T) {
	sched := simclock.New()
	net := simnet.New(sched, 1, simnet.DefaultConfig())
	ledger := credit.New(50_000, 0)
	repFn := func(n ports.NodeID) int64 { return ledger.Reputation(n) }

	ti := identity.FromSeed(1)
	tid := ti.NodeID()
	tn := New(tid, DefaultConfig(), sched, net.Endpoint(tid), memstore.New())
	tn.SetLedger(ledger)

	ch := chain.New(chain.DefaultConfig(), repFn)
	g := &chain.Block{Version: chain.BlockVersion, Height: 0, Entries: []ports.Entry{mkEntry("g")}}
	prop := identity.FromSeed(2)
	chain.Sign(g, prop.Signer())
	if err := ch.AppendGenesis(*g); err != nil {
		t.Fatalf("genesis: %v", err)
	}
	tn.EnableChain(ch, ti.Signer())

	// Give the proposer and target enough standing to propose/attest.
	for a := 0; a < 8; a++ {
		ledger.RecordAudit(tid, ports.HashBytes([]byte{1, byte(a)}), true)
		ledger.RecordAudit(prop.NodeID(), ports.HashBytes([]byte{2, byte(a)}), true)
	}

	mkProposal := func(name string) *chain.Block {
		b := &chain.Block{Version: chain.BlockVersion, Height: 1, Prev: g.Hash(), Entries: []ports.Entry{mkEntry(name)}}
		chain.Sign(b, prop.Signer())
		return b
	}
	first, second := mkProposal("first"), mkProposal("second")

	// A prober endpoint sends the two competing proposals and records the
	// validator's attest/refuse replies.
	prid := identity.FromSeed(3).NodeID()
	pr := net.Endpoint(prid)
	var replies []bool
	pr.SetHandler(func(_ ports.NodeID, msg ports.Message) {
		if msg.Kind == ports.MsgAttestReply {
			replies = append(replies, msg.OK)
		}
	})
	propose := func(b *chain.Block) {
		_ = pr.Send(tid, ports.Message{Kind: ports.MsgProposeBlock, Data: chain.Encode(b)})
		sched.Run()
	}
	propose(first)
	propose(second)

	if len(replies) != 2 || !replies[0] {
		t.Fatalf("validator should attest the first proposal at a height (replies=%v)", replies)
	}
	if replies[1] {
		t.Fatal("validator must REFUSE a second, different block at the same height — no equivocation")
	}
}
