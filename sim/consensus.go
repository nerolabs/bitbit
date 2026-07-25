// The consensus scenario — M12's demo. The registry becomes a block
// chain kept by the daemons themselves. Validators with earned
// reputation attest an established node's block into every replica; a
// fresh nobody's proposal is refused by everyone; a latecomer syncs the
// whole history and independently re-validates every signature; and a
// file published THROUGH the chain is retrieved by a node reading only
// its own local replica — no registry server anywhere.
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

type ConsensusOpts struct {
	Nodes       int // all run validator replicas
	Established int // nodes with earned reputation (audit history)
	FileSize    int
	ChunkSize   int
	Chain       chain.Config
	Net         simnet.Config
	NodeCfg     node.Config
	Report      func(string)
}

func DefaultConsensusOpts() ConsensusOpts {
	return ConsensusOpts{
		Nodes:       20,
		Established: 8,
		FileSize:    128 << 10,
		ChunkSize:   8 << 10,
		Chain:       chain.DefaultConfig(), // rep ≥ 100, quorum 3
		Net:         simnet.DefaultConfig(),
		NodeCfg:     node.DefaultConfig(),
	}
}

type ConsensusResult struct {
	Seed            int64
	Committed       int  // blocks on the proposer's replica
	ReplicasInSync  int  // nodes whose head matches the proposer's
	NobodyRejected  bool // the zero-reputation proposal failed
	LatecomerSynced int  // blocks the latecomer pulled and re-validated
	Retrieved       bool // file fetched using only a local replica
	Timeline        []string
	Net             simnet.Stats
}

func (r ConsensusResult) String() string {
	return fmt.Sprintf(
		"consensus: seed %d | %d blocks committed | %d replicas in sync | nobody rejected: %v | latecomer synced %d blocks | retrieval via own replica: %v",
		r.Seed, r.Committed, r.ReplicasInSync, r.NobodyRejected, r.LatecomerSynced, r.Retrieved)
}

func Consensus(seed int64, o ConsensusOpts) (ConsensusResult, error) {
	res := ConsensusResult{Seed: seed}
	// One shared reputation view in the sim (in the daemon world each
	// validator consults its own ledger; see the chain package docs).
	ledger := credit.New(50_000, 0)
	repFn := func(n ports.NodeID) int64 { return ledger.Reputation(n) }

	// Identity-keyed cluster: chain signatures must be made by the keys
	// the NodeIDs are hashes of.
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
		gb, _, _, gerr := genesis.Build(st)
		if gerr == nil {
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

	// Reputation is history: the established nodes have passed audits
	// on record; everyone else is a nobody the chain won't listen to.
	for i := 0; i < o.Established; i++ {
		for a := 0; a < 8; a++ {
			ledger.RecordAudit(cl.Nodes[i].ID(), ports.HashBytes([]byte{byte(i), byte(a)}), true)
		}
	}
	validators := make([]ports.NodeID, 0, o.Established)
	for i := 0; i < o.Established; i++ {
		validators = append(validators, cl.Nodes[i].ID())
	}
	everyone := make([]ports.NodeID, 0, o.Nodes)
	for _, nd := range cl.Nodes {
		everyone = append(everyone, nd.ID())
	}
	say(fmt.Sprintf("reputation | %d of %d nodes established (rep %d each); chain needs rep ≥ %d, quorum %d",
		o.Established, o.Nodes, ledger.Reputation(cl.Nodes[0].ID()), o.Chain.MinProposerRep, o.Chain.Quorum))

	// An established node publishes a real file THROUGH consensus:
	// stage + scatter, then propose the entry; validators attest.
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
	publisher.Distribute(entry, m, false, func(int) { distributed = true })
	sched.Run()
	if !distributed {
		return res, fmt.Errorf("consensus(seed=%d): distribution never completed", seed)
	}

	var propErr error
	proposed := false
	publisher.ProposeEntry(entry, validators, everyone, o.Chain.Quorum, func(err error) {
		propErr = err
		proposed = true
	})
	sched.Run()
	if !proposed || propErr != nil {
		return res, fmt.Errorf("consensus(seed=%d): propose: done=%v err=%v", seed, proposed, propErr)
	}
	fullLen := publisher.Chain().Len()
	res.Committed = fullLen - 1 // report non-genesis blocks
	head, _ := publisher.Chain().Head()
	for _, nd := range cl.Nodes {
		if hh, _ := nd.Chain().Head(); hh == head && nd.Chain().Len() == fullLen {
			res.ReplicasInSync++
		}
	}
	say(fmt.Sprintf("commit     | block 0 committed with quorum; %d/%d replicas at head %s…",
		res.ReplicasInSync, o.Nodes, head.String()[:12]))

	// A nobody tries to publish: every validator refuses to attest.
	nobody := cl.Nodes[o.Nodes-1]
	nobodyEntry := ports.Entry{Root: ports.HashBytes([]byte("nobody's file")),
		ManifestChunks: []ports.ChunkID{ports.HashBytes([]byte("m"))}}
	nobodyDone := false
	var nobodyErr error
	nobody.ProposeEntry(nobodyEntry, validators, everyone, o.Chain.Quorum, func(err error) {
		nobodyErr = err
		nobodyDone = true
	})
	sched.Run()
	res.NobodyRejected = nobodyDone && nobodyErr != nil
	say(fmt.Sprintf("gatekeeping| zero-reputation proposal rejected: %v (%v)", res.NobodyRejected, nobodyErr))

	// A latecomer joins with an empty replica and syncs — re-validating
	// every signature and quorum itself. Trust nothing, verify all.
	lateIdent := identity.FromSeed(seed*1000 + int64(o.Nodes) + 7)
	lateID := lateIdent.NodeID()
	lateStore := memstore.New()
	lateNode := node.New(lateID, o.NodeCfg, sched, net.Endpoint(lateID), lateStore)
	lateCh := chain.New(o.Chain, repFn)
	if gb, _, _, gerr := genesis.Build(lateStore); gerr == nil {
		lateCh.AppendGenesis(gb)
	}
	lateNode.EnableChain(lateCh, lateIdent.Signer())
	lateNode.Bootstrap([]ports.NodeID{publisher.ID()}, func() {})
	sched.Run()
	syncDone := false
	lateNode.SyncChain(validators, func(added int, err error) {
		res.LatecomerSynced = added
		syncDone = true
	})
	sched.Run()
	if !syncDone {
		return res, fmt.Errorf("consensus(seed=%d): sync never completed", seed)
	}
	say(fmt.Sprintf("sync       | latecomer pulled %d block(s), re-validated every signature", res.LatecomerSynced))

	// Retrieval reads the retriever's OWN replica — there is no registry
	// server in this scenario at all.
	retriever := cl.Nodes[o.Nodes/2]
	var out bytes.Buffer
	var gerr error
	got := false
	retriever.NetGet(chain.ReplicaRegistry{C: retriever.Chain()}, h, &out, func(err error) {
		gerr = err
		got = true
	})
	sched.Run()
	res.Retrieved = got && gerr == nil && bytes.Equal(out.Bytes(), data)
	say(fmt.Sprintf("retrieve   | via local chain replica, bit-perfect: %v", res.Retrieved))
	res.Net = net.Stats
	return res, nil
}
