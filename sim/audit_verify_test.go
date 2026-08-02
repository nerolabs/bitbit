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

// TestAuditVerifiesWithoutFetching is the 4a falsifiable claim, at the
// integration-within-a-peer tier: an auditor catches a liar that kept its
// PoR tags but threw away the bytes, and does so WITHOUT ever fetching the
// ground truth — the property the old toy scheme (which graded against bytes
// it fetched itself) could not provide. We assert two things over the audit
// sweep: zero MsgFetchChunk crossed the wire, and every liar was slashed
// while no honest host failed.
func TestAuditVerifiesWithoutFetching(t *testing.T) {
	const seed = 20260802
	cl := NewCluster(seed, 24, simnet.DefaultConfig(), func() node.Config {
		c := node.DefaultConfig()
		c.Replication = 3 // liars share replicas with honest holders
		return c
	}())
	ledger := credit.New(50_000, 0)
	reg := registry.New()

	liar := make(map[ports.NodeID]bool)
	for i, nd := range cl.Nodes {
		nd.SetLedger(ledger)
		ledger.Register(nd.ID())
		// Liars sit between the publisher (0), auditor (1), and retriever
		// (last) — they accept placements, keep proof + tags, ditch the data.
		if i >= 2 && i < 6 {
			nd.SetLiar(true)
			liar[nd.ID()] = true
		}
	}

	publisher, auditor := cl.Nodes[0], cl.Nodes[1]
	data := make([]byte, 128<<10)
	cl.rng.Read(data)
	h, err := pipeline.Add(bgCtx, publisher.Store(), reg, bytes.NewReader(data),
		pipeline.Options{ChunkSize: 4 << 10, Mode: crypto.Convergent})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	entry, _, _ := reg.Lookup(bgCtx, h.Root)
	m, err := pipeline.LoadFull(bgCtx, publisher.Store(), entry, h)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	publisher.Distribute(entry, m, false, node.DerivePorKey(h.LayoutKey()), func(int, error) {})
	cl.Sched.Run()

	// Warm the auditor's copy of the (public) manifest chunks, so the audit's
	// own manifest load is all-local and the measured window contains only
	// what the audit does to grade SHARDS. Fetching the manifest is legitimate
	// — it is structure, not ground-truth bytes; fetching a shard to grade it
	// is the thing a real PoR must never do.
	for _, id := range entry.ManifestChunks {
		auditor.FetchChunk(id, func(error) {})
	}
	cl.Sched.Run()

	// Snapshot fetch traffic, then run ONLY the audit sweep and measure the
	// delta — so the count is the audit's alone, not publish/retrieve.
	fetchBefore := cl.Net.Stats.Kinds[ports.MsgFetchChunk]
	var report node.AuditReport
	done := false
	auditor.Audit(reg, h.Care(), func(rep node.AuditReport) { report = rep; done = true })
	cl.Sched.Run()
	if !done {
		t.Fatal("audit sweep never completed")
	}
	fetchDuringAudit := cl.Net.Stats.Kinds[ports.MsgFetchChunk] - fetchBefore

	if fetchDuringAudit != 0 {
		t.Fatalf("audit fetched shard ground truth %d times — it must verify WITHOUT fetching (that is the whole point of a real PoR)", fetchDuringAudit)
	}
	if report.Failed == 0 {
		t.Fatal("no audit failed — the liars (tags kept, bytes dropped) went uncaught")
	}

	// Outcome, not mechanism: every liar is slashed into debt; no honest
	// host failed an audit.
	caught, honestFailed := 0, 0
	for _, nd := range cl.Nodes {
		_, failed := ledger.Audits(nd.ID())
		if liar[nd.ID()] {
			if failed > 0 && ledger.Balance(nd.ID()) < 0 {
				caught++
			}
		} else if failed > 0 {
			honestFailed++
		}
	}
	if honestFailed != 0 {
		t.Fatalf("%d honest hosts failed an audit they should have passed", honestFailed)
	}
	if caught == 0 {
		t.Fatal("no liar was slashed into debt despite dropping the bytes")
	}
}
