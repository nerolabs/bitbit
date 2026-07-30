// The audit scenario — M7's demo. A file is scattered across a swarm
// in which some nodes are LIARS: they accept chunk placements, keep the
// Merkle proof, throw away the data, and answer availability probes
// with a straight face. Retrieval doesn't unmask them (they just say
// "not found" and the fetcher moves on), and simple serve-counting
// economics never touches them. Storage AUDITS do: challenged with a
// fresh nonce, a liar can produce the proof but not the tag, and the
// ledger slashes it into debt. Honest hosts collect audit rent for the
// same challenge.
package sim

import (
	"bytes"
	"fmt"

	"github.com/nerolabs/silt/adapters/simnet"
	"github.com/nerolabs/silt/core/credit"
	"github.com/nerolabs/silt/core/crypto"
	"github.com/nerolabs/silt/core/node"
	"github.com/nerolabs/silt/core/pipeline"
	"github.com/nerolabs/silt/core/registry"
	"github.com/nerolabs/silt/ports"
)

type AuditOpts struct {
	Nodes     int
	Liars     int
	FileSize  int
	ChunkSize int
	Net       simnet.Config
	NodeCfg   node.Config
	Report    func(string)
}

func DefaultAuditOpts() AuditOpts {
	cfg := node.DefaultConfig()
	cfg.Replication = 3 // liars mixed in with honest replicas
	return AuditOpts{
		Nodes:     30,
		Liars:     6,
		FileSize:  128 << 10,
		ChunkSize: 4 << 10,
		Net:       simnet.DefaultConfig(),
		NodeCfg:   cfg,
	}
}

type AuditResult struct {
	Seed          int64
	Root          ports.Hash
	Report        node.AuditReport
	LiarsCaught   int // liars with at least one failed audit
	LiarsNegative int // liars slashed below zero balance
	HonestFailed  int // honest providers that failed any audit (want 0)
	Retrieved     bool
	Timeline      []string
	Net           simnet.Stats
}

func (r AuditResult) String() string {
	return fmt.Sprintf(
		"audit: seed %d | %d challenges: %d passed, %d failed | liars caught %d, in debt %d | honest failures %d | file retrievable: %v",
		r.Seed, r.Report.Challenges, r.Report.Passed, r.Report.Failed,
		r.LiarsCaught, r.LiarsNegative, r.HonestFailed, r.Retrieved)
}

func Audit(seed int64, o AuditOpts) (AuditResult, error) {
	res := AuditResult{Seed: seed}
	cl := NewCluster(seed, o.Nodes, o.Net, o.NodeCfg)
	ledger := credit.New(50_000, 0)
	reg := registry.New() // audits are about storage honesty, not publish gating

	say := func(line string) {
		res.Timeline = append(res.Timeline, line)
		if o.Report != nil {
			o.Report(line)
		}
	}

	liar := make(map[ports.NodeID]bool)
	for i, nd := range cl.Nodes {
		nd.SetLedger(ledger)
		ledger.Register(nd.ID())
		// Liars sit in the middle of the index range: never the
		// publisher (0), the auditor (1), or the retriever (last).
		if i >= 2 && i < 2+o.Liars {
			nd.SetLiar(true)
			liar[nd.ID()] = true
		}
	}
	say(fmt.Sprintf("swarm     | %d nodes, %d of them liars (store the proof, ditch the data)", o.Nodes, o.Liars))

	// Publish and scatter. Liars accept placements like anyone else.
	publisher, auditor, retriever := cl.Nodes[0], cl.Nodes[1], cl.Nodes[len(cl.Nodes)-1]
	data := make([]byte, o.FileSize)
	cl.rng.Read(data)
	h, err := pipeline.Add(bgCtx, publisher.Store(), reg, bytes.NewReader(data),
		pipeline.Options{ChunkSize: o.ChunkSize, Mode: crypto.Convergent})
	if err != nil {
		return res, err
	}
	res.Root = h.Root
	entry, _, _ := reg.Lookup(bgCtx, h.Root)
	m, err := pipeline.LoadFull(bgCtx, publisher.Store(), entry, h)
	if err != nil {
		return res, err
	}
	placed := 0
	publisher.Distribute(entry, m, false, func(p int, _ error) { placed = p })
	cl.Sched.Run()
	say(fmt.Sprintf("scatter   | %d chunk replica placements accepted (some by liars, who kept nothing)", placed))

	// The audit sweep: challenge every provider of every shard.
	done := false
	auditor.Audit(reg, h.Care(), func(rep node.AuditReport) {
		res.Report = rep
		done = true
	})
	cl.Sched.Run()
	if !done {
		return res, fmt.Errorf("audit(seed=%d): sweep never completed", seed)
	}
	say(fmt.Sprintf("audit     | %d challenges | %d passed (+%d credits each) | %d failed (-%d each) | %d unverifiable",
		res.Report.Challenges, res.Report.Passed, ledger.AuditReward,
		res.Report.Failed, ledger.AuditSlash, res.Report.NoTruth))

	for _, nd := range cl.Nodes {
		_, failed := ledger.Audits(nd.ID())
		if liar[nd.ID()] {
			if failed > 0 {
				res.LiarsCaught++
			}
			if ledger.Balance(nd.ID()) < 0 {
				res.LiarsNegative++
			}
		} else if failed > 0 {
			res.HonestFailed++
		}
	}
	var worstLiar, bestHonest *node.Node
	for _, nd := range cl.Nodes {
		b := ledger.Balance(nd.ID())
		if liar[nd.ID()] && (worstLiar == nil || b < ledger.Balance(worstLiar.ID())) {
			worstLiar = nd
		}
		if !liar[nd.ID()] && (bestHonest == nil || b > ledger.Balance(bestHonest.ID())) {
			bestHonest = nd
		}
	}
	if bestHonest != nil {
		p, f := ledger.Audits(bestHonest.ID())
		say(fmt.Sprintf("  honest host %s… audits %d/%d passed → %+d credits",
			bestHonest.ID().String()[:8], p, p+f, ledger.Balance(bestHonest.ID())))
	}
	if worstLiar != nil {
		p, f := ledger.Audits(worstLiar.ID())
		say(fmt.Sprintf("  liar        %s… audits %d/%d passed → %+d credits (slashed into debt)",
			worstLiar.ID().String()[:8], p, p+f, ledger.Balance(worstLiar.ID())))
	}

	// And the file itself never depended on the liars: honest replicas
	// plus parity carry it home.
	var out bytes.Buffer
	var gerr error
	got := false
	retriever.NetGet(reg, h, &out, func(err error) { gerr = err; got = true })
	cl.Sched.Run()
	res.Retrieved = got && gerr == nil && bytes.Equal(out.Bytes(), data)
	say(fmt.Sprintf("retrieve  | bit-perfect despite %d fake providers: %v", o.Liars, res.Retrieved))
	res.Net = cl.Net.Stats
	return res, nil
}
