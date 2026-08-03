package node

import (
	"testing"

	"github.com/nerolabs/silt/adapters/identity"
	"github.com/nerolabs/silt/adapters/memstore"
	"github.com/nerolabs/silt/adapters/simclock"
	"github.com/nerolabs/silt/adapters/simnet"
	"github.com/nerolabs/silt/core/chain"
	"github.com/nerolabs/silt/core/credit"
	"github.com/nerolabs/silt/ports"
)

// TestRestartedValidatorCatchesUpOnceStandingReturns is the F1 catch-up
// regression: a restarted validator whose reputation view is EMPTY at boot must
// not be permanently stranded. It cannot adopt a peer's blocks until bond audits
// re-establish that peer's standing — so catch-up has to RETRY, and once
// standing returns the very same sync succeeds. (This is exactly why the daemon
// runs StartBondAudit before, and StartChainSync as a retrying loop after — a
// one-shot sync at boot, against an empty ledger, adopts nothing.)
func TestRestartedValidatorCatchesUpOnceStandingReturns(t *testing.T) {
	sched := simclock.New()
	net := simnet.New(sched, 1, simnet.DefaultConfig())

	cfg := chain.DefaultConfig() // MinProposerRep/MinAttesterRep = 100
	cfg.Quorum = 1

	// A genesis both replicas share by construction (Reconcile refuses a fork
	// that doesn't share our genesis).
	g := &chain.Block{Version: chain.BlockVersion, Height: 0, Entries: []ports.Entry{mkEntry("genesis")}}
	chain.Sign(g, identity.FromSeed(9).Signer())

	p := identity.FromSeed(2) // proposer of block 1
	v := identity.FromSeed(3) // its attester

	// Node A: the up-to-date source, chain = [genesis, block1]. Built with a
	// reputation view in which P and V are fully qualified (commit time).
	aRep := map[ports.NodeID]int64{p.NodeID(): 1000, v.NodeID(): 1000}
	aCh := chain.New(cfg, func(n ports.NodeID) int64 { return aRep[n] })
	if err := aCh.AppendGenesis(*g); err != nil {
		t.Fatalf("A genesis: %v", err)
	}
	b1 := &chain.Block{Version: chain.BlockVersion, Height: 1, Prev: g.Hash(), Entries: []ports.Entry{mkEntry("block1")}}
	chain.Sign(b1, p.Signer())
	b1.Atts = append(b1.Atts, chain.Attest(b1, v.Signer()))
	if err := aCh.Append(*b1); err != nil {
		t.Fatalf("A commit block1: %v", err)
	}
	ai := identity.FromSeed(1)
	aNode := New(ai.NodeID(), DefaultConfig(), sched, net.Endpoint(ai.NodeID()), memstore.New())
	aNode.EnableChain(aCh, ai.Signer()) // serves MsgGetChain

	// Node D: the restarted validator. Empty ledger (bond audits have not run),
	// chain = [genesis] only.
	dLedger := credit.New(50_000, 0)
	dCh := chain.New(cfg, func(n ports.NodeID) int64 { return dLedger.Reputation(n) })
	if err := dCh.AppendGenesis(*g); err != nil {
		t.Fatalf("D genesis: %v", err)
	}
	di := identity.FromSeed(4)
	dNode := New(di.NodeID(), DefaultConfig(), sched, net.Endpoint(di.NodeID()), memstore.New())
	dNode.SetLedger(dLedger)
	dNode.EnableChain(dCh, di.Signer())

	// 1) Empty standing: a sync must NOT adopt block1 — D cannot yet see that
	//    P/V carry real standing, so it correctly refuses the fork.
	added := -1
	dNode.SyncChain([]ports.NodeID{ai.NodeID()}, func(a int, _ error) { added = a })
	sched.Run()
	if dCh.Len() != 1 || added != 0 {
		t.Fatalf("empty-standing sync must adopt nothing: len=%d added=%d", dCh.Len(), added)
	}

	// 2) Bond audits land — D now sees P and V as bonded (>= 100 standing). A
	//    retry of the identical sync catches up.
	for _, id := range []ports.NodeID{p.NodeID(), v.NodeID()} {
		dLedger.RecordBondChallenge(id, ports.HashBytes(id[:]), 8<<20, true, 1)
	}
	dNode.SyncChain([]ports.NodeID{ai.NodeID()}, func(a int, _ error) { added = a })
	sched.Run()
	if dCh.Len() != 2 || added != 1 {
		t.Fatalf("catch-up after standing returns must adopt block1: len=%d added=%d", dCh.Len(), added)
	}
	wantH, wantN := aCh.Head()
	gotH, gotN := dCh.Head()
	if wantH != gotH || wantN != gotN {
		t.Fatalf("D did not converge to A's head: want %x@%d got %x@%d", wantH, wantN, gotH, gotN)
	}
}

// TestSyncTargetsUngatedByAttesters proves catch-up is not gated on -attesters:
// a validator learned only from a gossiped bond (peerBonds) is a sync target, so
// a node restarted with just -bootstrap still discovers the set and rejoins.
func TestSyncTargetsUngatedByAttesters(t *testing.T) {
	sched := simclock.New()
	net := simnet.New(sched, 1, simnet.DefaultConfig())
	di := identity.FromSeed(4)
	n := New(di.NodeID(), DefaultConfig(), sched, net.Endpoint(di.NodeID()), memstore.New())

	learned := identity.FromSeed(7).NodeID()
	n.peerBonds[learned] = bondInfo{root: ports.HashBytes(learned[:]), size: 8 << 20}
	n.chainSyncSeed = nil // no -attesters given

	targets := n.syncTargets()
	if len(targets) != 1 || targets[0] != learned {
		t.Fatalf("syncTargets should include a bond-learned validator even with no seed: %v", targets)
	}
	// And it never targets itself.
	n.peerBonds[n.id] = bondInfo{root: ports.HashBytes(n.id[:]), size: 8 << 20}
	for _, id := range n.syncTargets() {
		if id == n.id {
			t.Fatal("syncTargets must never include self")
		}
	}
}
