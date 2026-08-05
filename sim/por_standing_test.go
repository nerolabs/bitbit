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

// M0 hardening H1 / red-team RT-1, integration tier over the real audit wire:
// PoR audits prove POSSESSION of shards but must grant NO Sybil-resistant
// STANDING. A node that holds real shards and passes audit after audit — the
// disk it would take a data-less Sybil to fake by relaying — earns audit credit
// and balance, but its consensus Reputation stays 0 because it holds no bond.
// Standing rests on the bond press alone (docs/design/m0-hardening-strategy.md
// §4 S2). Before H1, `+ auditsPassed*25` lifted such a node to eligibility.
func TestPorAuditsGrantNoStandingWithoutBondOverTheWire(t *testing.T) {
	const seed = 20260805
	cl := NewCluster(seed, 12, simnet.DefaultConfig(), func() node.Config {
		c := node.DefaultConfig()
		c.Replication = 3
		return c
	}())
	ledger := credit.New(50_000, 0)
	reg := registry.New()
	for _, nd := range cl.Nodes {
		nd.SetLedger(ledger)
		ledger.Register(nd.ID())
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
	for _, id := range entry.ManifestChunks {
		auditor.FetchChunk(id, func(error) {})
	}
	cl.Sched.Run()

	// Run several honest audit sweeps: real holders pass over the wire.
	for i := 0; i < 4; i++ {
		done := false
		auditor.Audit(reg, h.Care(), func(node.AuditReport) { done = true })
		cl.Sched.Run()
		if !done {
			t.Fatal("audit sweep never completed")
		}
	}

	// Some honest holder must have passed audits (the press fired)...
	var passer ports.NodeID
	for _, nd := range cl.Nodes {
		if nd.ID() == auditor.ID() {
			continue
		}
		if passed, _ := ledger.Audits(nd.ID()); passed > 0 {
			passer = nd.ID()
			break
		}
	}
	if passer == (ports.NodeID{}) {
		t.Fatal("setup: no honest holder passed a PoR audit — the press never fired")
	}
	// ...yet its consensus standing is ZERO: PoR passes mint no standing (RT-1).
	if got := ledger.Reputation(passer); got > 0 {
		t.Fatalf("RT-1 regression: a holder that passed PoR audits but holds NO bond has standing %d > 0 over the wire — a data-less relay farm would earn consensus weight", got)
	}

	// Positive control: the bond press DOES grant standing on the same ledger.
	ledger.RecordBondChallenge(passer, ports.HashBytes(passer[:]), 64<<20, true, 1)
	if ledger.Reputation(passer) <= 0 {
		t.Fatal("the bond press must grant standing — audits removed, bond remains the sole press")
	}
}
