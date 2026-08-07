package sim

import (
	"bytes"
	"testing"

	"github.com/nerolabs/silt/adapters/simnet"
	"github.com/nerolabs/silt/core/credit"
	"github.com/nerolabs/silt/core/crypto"
	"github.com/nerolabs/silt/core/erasure"
	"github.com/nerolabs/silt/core/node"
	"github.com/nerolabs/silt/core/pipeline"
	"github.com/nerolabs/silt/ports"
)

// TestServeAutoSkimFundsObjectEscrow is the H7 self-funding-durability wiring at
// the integration tier: when a coded object's shards are SERVED, a protocol-fixed
// slice of the serve revenue is diverted into THAT object's durability reserve —
// so popular data pays for its own repair, no operator top-up required. The serve
// path routes the skim by resolving each shard's object root from its storage
// proof; a retrieval of the whole file therefore leaves a non-empty reserve, the
// servers keep the net, and — the load-bearing invariant — no standing moves.
func TestServeAutoSkimFundsObjectEscrow(t *testing.T) {
	const seed = 20260808
	cfg := node.DefaultConfig()
	cfg.Replication = 3 // several honest holders per column to serve from
	cl := NewCluster(seed, 30, simnet.DefaultConfig(), cfg)

	ledger := credit.New(50_000, 0) // no grant: balances start at 0, so deltas are pure serve income
	baseline := map[ports.NodeID]int64{}
	for _, nd := range cl.Nodes {
		nd.SetLedger(ledger)
		ledger.Register(nd.ID())
		ledger.RecordBondChallenge(nd.ID(), nd.ID(), 64<<20, true, 1)
		baseline[nd.ID()] = ledger.Reputation(nd.ID())
	}

	publisher, retriever := cl.Nodes[0], cl.Nodes[len(cl.Nodes)-1]
	data := make([]byte, 256<<10)
	cl.rng.Read(data)
	h, err := pipeline.Add(bgCtx, publisher.Store(), cl.Registry, bytes.NewReader(data),
		pipeline.Options{ChunkSize: 4 << 10, Mode: crypto.Convergent, Erasure: erasure.DefaultParams})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	root := h.Root
	entry, _, _ := cl.Registry.Lookup(bgCtx, root)
	m, err := pipeline.LoadFull(bgCtx, publisher.Store(), entry, h)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	publisher.Distribute(entry, m, false, node.DerivePorKey(h.LayoutKey()), func(int, error) {})
	cl.Sched.Run()

	if pre := ledger.EscrowFunded(root); pre != 0 {
		t.Fatalf("escrow funded before any serve: %d (distribution should not skim)", pre)
	}

	// A survivor retrieves the whole file — every shard served skims into the
	// object's reserve.
	var out bytes.Buffer
	got := false
	var gerr error
	retriever.NetGet(cl.Registry, h, &out, func(err error) { gerr = err; got = true })
	cl.Sched.Run()
	if !got || gerr != nil || !bytes.Equal(out.Bytes(), data) {
		t.Fatalf("retrieval failed: got=%v err=%v match=%v", got, gerr, bytes.Equal(out.Bytes(), data))
	}

	funded := ledger.EscrowFunded(root)
	t.Logf("EscrowFunded from serving = %d (of %d file bytes) | reserve now %d", funded, len(data), ledger.EscrowBalance(root))
	if funded <= 0 {
		t.Fatal("serving the object skimmed nothing into its escrow — the auto-skim is not wired into the serve path")
	}
	if funded >= int64(len(data)) {
		t.Fatalf("skim %d >= file size %d — it must be a SLICE of serve revenue, not the whole thing", funded, len(data))
	}
	// Nothing has been paid out yet, so the reserve equals what was skimmed.
	if bal := ledger.EscrowBalance(root); bal != funded {
		t.Fatalf("reserve %d != funded %d with no bounties paid", bal, funded)
	}

	// THE INVARIANT: serve income funds the balance economy and the durability
	// reserve — never standing.
	for _, nd := range cl.Nodes {
		if r := ledger.Reputation(nd.ID()); r != baseline[nd.ID()] {
			t.Fatalf("node %s standing moved from %d to %d — the serve auto-skim must confer ZERO standing",
				nd.ID().String()[:8], baseline[nd.ID()], r)
		}
	}
}
