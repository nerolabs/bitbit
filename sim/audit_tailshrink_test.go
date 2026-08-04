package sim

import (
	"bytes"
	"testing"

	"github.com/nerolabs/silt/adapters/simnet"
	"github.com/nerolabs/silt/core/credit"
	"github.com/nerolabs/silt/core/crypto"
	"github.com/nerolabs/silt/core/node"
	"github.com/nerolabs/silt/core/pipeline"
	"github.com/nerolabs/silt/core/registry"
	"github.com/nerolabs/silt/ports"
)

// Red-team F4 (integrity, S1) at the integration tier: a "shrink liar" keeps
// only the FIRST PoR block of each shard and answers challenges reporting
// PorBlocks=1. The old auditor applied a lenient "tail" branch to the file's
// last leaf (accept any 1..wantFull) even though that shard is full-size on the
// wire — so the shrink liar passed there while holding one block. The fix grades
// EVERY leaf against the auditor's own full block count, so the shrink liar is
// caught, while an honest holder still passes.
//
// The file is a SINGLE chunk: its sole data leaf is i==dataN-1, exactly the leaf
// the old code treated leniently, so this test isolates the F4 fix — under the
// old code the shrink liars pass (report.Failed==0) and the test fails.
func TestTailShrinkLiarCaughtOnLastLeaf(t *testing.T) {
	const seed = 20260804
	cl := NewCluster(seed, 24, simnet.DefaultConfig(), func() node.Config {
		c := node.DefaultConfig()
		c.Replication = 3 // shrink liars share replicas with honest holders
		return c
	}())
	ledger := credit.New(50_000, 0)
	reg := registry.New()

	shrink := make(map[ports.NodeID]bool)
	for i, nd := range cl.Nodes {
		nd.SetLedger(ledger)
		ledger.Register(nd.ID())
		if i >= 2 && i < 6 {
			nd.SetShrinkLiar(true)
			shrink[nd.ID()] = true
		}
	}

	publisher, auditor := cl.Nodes[0], cl.Nodes[1]

	// A single-chunk file: the lone data leaf (i==dataN-1) is the previously
	// lenient "tail" leaf. Make the chunk span many PoR blocks so reporting 1 is
	// a real shrink the auditor must reject.
	data := make([]byte, 200<<10)
	cl.rng.Read(data)
	h, err := pipeline.Add(bgCtx, publisher.Store(), reg, bytes.NewReader(data),
		pipeline.Options{ChunkSize: 256 << 10, Mode: crypto.Convergent})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	entry, _, _ := reg.Lookup(bgCtx, h.Root)
	m, err := pipeline.LoadFull(bgCtx, publisher.Store(), entry, h)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(m.Chunks) != 1 {
		t.Fatalf("setup: expected a single-chunk file, got %d chunks", len(m.Chunks))
	}
	publisher.Distribute(entry, m, false, node.DerivePorKey(h.LayoutKey()), func(int, error) {})
	cl.Sched.Run()

	var report node.AuditReport
	done := false
	auditor.Audit(reg, h.Care(), func(rep node.AuditReport) { report = rep; done = true })
	cl.Sched.Run()
	if !done {
		t.Fatal("audit sweep never completed")
	}
	if report.Failed == 0 {
		t.Fatal("F4: no audit failed — the shrink liars (holding 1 block, reporting 1) passed the last-leaf audit")
	}

	// Every shrink liar is slashed into debt; no honest host failed — demanding
	// the full block count must not false-positive an honest holder.
	caught, honestFailed := 0, 0
	for _, nd := range cl.Nodes {
		passed, failed := ledger.Audits(nd.ID())
		if shrink[nd.ID()] {
			if failed > 0 && ledger.Balance(nd.ID()) < 0 {
				caught++
			}
		} else if failed > 0 {
			t.Logf("honest node %s: passed=%d failed=%d", nd.ID(), passed, failed)
			honestFailed++
		}
	}
	if honestFailed != 0 {
		t.Fatalf("F4 regression: %d honest hosts failed an audit they should pass", honestFailed)
	}
	if caught == 0 {
		t.Fatal("F4: no shrink liar was slashed into debt despite holding one block of the shard")
	}
}
