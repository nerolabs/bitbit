// The bond-standing scenario — the trust pivot's demo (Trust roadmap T1).
// Consensus standing is EARNED BY STORAGE, not bought with chatter: a
// validator that proves an identity-bound storage bond (core/bond, via
// credit.RecordBondChallenge) clears the chain's reputation bar and can
// commit; a Sybil ring that wash-serves each other a terabyte moves its
// balances but earns ZERO standing, so it can neither propose nor form a
// quorum. And standing must be SUSTAINED: once validators stop re-proving
// their bonds, DecayStale retires their standing and their votes stop
// counting. This is the deterministic sim-proof behind #78 / D1 / D3.
package sim

import (
	"bytes"
	"fmt"
	"math/rand"

	"github.com/nerolabs/silt/adapters/identity"
	"github.com/nerolabs/silt/adapters/memstore"
	"github.com/nerolabs/silt/adapters/simclock"
	"github.com/nerolabs/silt/adapters/simnet"
	"github.com/nerolabs/silt/core/chain"
	"github.com/nerolabs/silt/core/credit"
	"github.com/nerolabs/silt/core/crypto"
	"github.com/nerolabs/silt/core/genesis"
	"github.com/nerolabs/silt/core/node"
	"github.com/nerolabs/silt/core/pipeline"
	"github.com/nerolabs/silt/core/registry"
	"github.com/nerolabs/silt/ports"
)

type BondStandingOpts struct {
	Nodes     int // all run validator replicas
	Bonded    int // honest validators that prove real storage bonds
	Sybils    int // wash-serving ring with zero real storage
	BondBytes int64
	FileSize  int
	ChunkSize int
	DecayAge  uint64 // a bond unrefreshed for longer than this goes stale
	Chain     chain.Config
	Net       simnet.Config
	NodeCfg   node.Config
	Report    func(string)
}

func DefaultBondStandingOpts() BondStandingOpts {
	return BondStandingOpts{
		Nodes:     16,
		Bonded:    6,
		Sybils:    5,
		BondBytes: 64 << 20, // 64 MiB proven → rep ~1024, well over the 100 bar
		FileSize:  128 << 10,
		ChunkSize: 8 << 10,
		DecayAge:  100,
		Chain:     chain.DefaultConfig(), // rep ≥ 100, quorum 3
		Net:       simnet.DefaultConfig(),
		NodeCfg:   node.DefaultConfig(),
	}
}

type BondStandingResult struct {
	Seed                  int64
	Committed             int  // non-genesis blocks the bonded proposer committed
	ReplicasInSync        int  // replicas that converged on the committed head
	SybilProposalRejected bool // a wash-serving Sybil could not propose
	SybilQuorumDenied     bool // an all-Sybil attester set could not commit
	DecayedOut            bool // after decay, un-refreshed validators lost their vote
	Timeline              []string
	Net                   simnet.Stats
}

func (r BondStandingResult) String() string {
	return fmt.Sprintf(
		"bondstanding: seed %d | %d block committed by bonded validators | %d replicas in sync | sybil propose rejected: %v | sybil quorum denied: %v | decayed-out lose their vote: %v",
		r.Seed, r.Committed, r.ReplicasInSync, r.SybilProposalRejected, r.SybilQuorumDenied, r.DecayedOut)
}

func BondStanding(seed int64, o BondStandingOpts) (BondStandingResult, error) {
	res := BondStandingResult{Seed: seed}
	// Shared reputation view in the sim (each daemon consults its own
	// ledger in the field; see the chain package docs).
	ledger := credit.New(50_000, 0)
	repFn := func(n ports.NodeID) int64 { return ledger.Reputation(n) }

	sched := simclock.New()
	net := simnet.New(sched, seed, o.Net)
	cl := &Cluster{Seed: seed, Sched: sched, Net: net, Registry: registry.New()}
	idents := make([]*identity.Identity, o.Nodes)
	for i := 0; i < o.Nodes; i++ {
		idents[i] = identity.FromSeed(seed*1000 + int64(i))
		id := idents[i].NodeID()
		st := memstore.New()
		nd := node.New(id, o.NodeCfg, sched, net.Endpoint(id), st)
		ch := chain.New(o.Chain, repFn)
		if gb, _, _, gerr := genesis.Build(st); gerr == nil {
			ch.AppendGenesis(gb)
		}
		nd.EnableChain(ch, idents[i].Signer())
		cl.Nodes = append(cl.Nodes, nd)
	}
	for i, nd := range cl.Nodes {
		if i == 0 {
			continue
		}
		nd.Bootstrap([]ports.NodeID{cl.Nodes[0].ID()}, func() {})
	}
	sched.Run()

	say := func(line string) {
		res.Timeline = append(res.Timeline, line)
		if o.Report != nil {
			o.Report(line)
		}
	}

	// Standing is EARNED BY STORAGE: the first Bonded nodes prove an
	// identity-bound bond (tick 1). The next Sybils only wash-serve each
	// other — balances soar, standing stays zero.
	bonded := make([]ports.NodeID, 0, o.Bonded)
	for i := 0; i < o.Bonded; i++ {
		ledger.RecordBondChallenge(cl.Nodes[i].ID(), o.BondBytes, true, 1)
		bonded = append(bonded, cl.Nodes[i].ID())
	}
	sybils := make([]ports.NodeID, 0, o.Sybils)
	for i := o.Bonded; i < o.Bonded+o.Sybils && i < o.Nodes; i++ {
		sybils = append(sybils, cl.Nodes[i].ID())
	}
	washChunk := ports.HashBytes([]byte("wash"))
	for _, a := range sybils {
		for _, b := range sybils {
			if a != b {
				ledger.RecordServe(a, b, washChunk, 1<<40) // 1 TiB each way
			}
		}
	}
	everyone := make([]ports.NodeID, 0, o.Nodes)
	for _, nd := range cl.Nodes {
		everyone = append(everyone, nd.ID())
	}
	say(fmt.Sprintf("standing   | %d bonded validators (rep %d each, storage-earned); %d sybils wash-served %d TiB → rep %d each",
		o.Bonded, ledger.Reputation(bonded[0]), o.Sybils, int64(o.Sybils-1), ledger.Reputation(sybils[0])))

	// A bonded validator publishes a real file THROUGH consensus and the
	// bonded quorum commits it.
	publisher := cl.Nodes[0]
	data := make([]byte, o.FileSize)
	rand.New(rand.NewSource(seed)).Read(data)
	staging := registry.New()
	h, err := pipeline.Add(bgCtx, publisher.Store(), staging, bytes.NewReader(data),
		pipeline.Options{ChunkSize: o.ChunkSize, Mode: crypto.Convergent})
	if err != nil {
		return res, err
	}
	entry, _, _ := staging.Lookup(bgCtx, h.Root)
	m, err := pipeline.LoadFull(bgCtx, publisher.Store(), entry, h)
	if err != nil {
		return res, err
	}
	distributed := false
	publisher.Distribute(entry, m, false, node.DerivePorKey(h.LayoutKey()), func(int, error) { distributed = true })
	sched.Run()
	if !distributed {
		return res, fmt.Errorf("bondstanding(seed=%d): distribution never completed", seed)
	}
	var propErr error
	proposed := false
	publisher.ProposeEntry(entry, bonded, everyone, o.Chain.Quorum, func(err error) {
		propErr = err
		proposed = true
	})
	sched.Run()
	if !proposed || propErr != nil {
		return res, fmt.Errorf("bondstanding(seed=%d): bonded propose failed: done=%v err=%v", seed, proposed, propErr)
	}
	fullLen := publisher.Chain().Len()
	res.Committed = fullLen - 1
	head, _ := publisher.Chain().Head()
	for _, nd := range cl.Nodes {
		if hh, _ := nd.Chain().Head(); hh == head && nd.Chain().Len() == fullLen {
			res.ReplicasInSync++
		}
	}
	say(fmt.Sprintf("commit     | bonded quorum committed 1 block; %d/%d replicas at head %s…",
		res.ReplicasInSync, o.Nodes, head.String()[:12]))

	// A wash-serving Sybil tries to propose: refused at the reputation gate.
	sybilProposer := cl.Nodes[o.Bonded]
	sybilEntry := synthEntry("sybil's file")
	spDone := false
	var spErr error
	sybilProposer.ProposeEntry(sybilEntry, bonded, everyone, o.Chain.Quorum, func(err error) {
		spErr = err
		spDone = true
	})
	sched.Run()
	res.SybilProposalRejected = spDone && spErr != nil
	say(fmt.Sprintf("gatekeep-1 | wash-serving sybil cannot propose: %v (%v)", res.SybilProposalRejected, spErr))

	// The bonded proposer proposes, but only Sybils are asked to attest:
	// their signatures are valid yet carry zero standing, so no quorum forms.
	sqEntry := synthEntry("bonded root, sybil attesters")
	sqDone := false
	var sqErr error
	publisher.ProposeEntry(sqEntry, sybils, everyone, o.Chain.Quorum, func(err error) {
		sqErr = err
		sqDone = true
	})
	sched.Run()
	res.SybilQuorumDenied = sqDone && sqErr != nil
	say(fmt.Sprintf("gatekeep-2 | all-sybil attester set denied quorum: %v (%v)", res.SybilQuorumDenied, sqErr))

	// Standing must be SUSTAINED. Advance the clock past DecayAge and decay:
	// every bond proven at tick 1 goes stale. Re-prove only the proposer, so
	// it still has standing, but its bonded attesters have decayed — and the
	// quorum can no longer form. Whoever stops proving loses their vote.
	now := o.DecayAge + 2
	ledger.DecayStale(now, o.DecayAge)
	ledger.RecordBondChallenge(publisher.ID(), o.BondBytes, true, now) // proposer keeps proving
	decayEntry := synthEntry("after decay")
	dDone := false
	var dErr error
	publisher.ProposeEntry(decayEntry, bonded, everyone, o.Chain.Quorum, func(err error) {
		dErr = err
		dDone = true
	})
	sched.Run()
	res.DecayedOut = dDone && dErr != nil
	say(fmt.Sprintf("decay      | validators that stopped re-proving lose their vote: %v (%v)", res.DecayedOut, dErr))

	res.Net = net.Stats
	return res, nil
}

func synthEntry(name string) ports.Entry {
	return ports.Entry{
		Root:           ports.HashBytes([]byte(name)),
		ManifestChunks: []ports.ChunkID{ports.HashBytes([]byte(name + "/m"))},
	}
}
