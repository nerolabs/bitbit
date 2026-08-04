package sim

import (
	"errors"
	"testing"

	"github.com/nerolabs/silt/adapters/identity"
	"github.com/nerolabs/silt/adapters/memstore"
	"github.com/nerolabs/silt/adapters/simclock"
	"github.com/nerolabs/silt/adapters/simnet"
	"github.com/nerolabs/silt/core/chain"
	"github.com/nerolabs/silt/core/credit"
	"github.com/nerolabs/silt/core/genesis"
	"github.com/nerolabs/silt/core/node"
	"github.com/nerolabs/silt/ports"
)

// Integration/sim tier for the M0 accountability fix (red-team F5). The unit and
// node-white-box tests prove the chain rules and the isDenied logic; this drives
// the OUTCOMES through the full node loop: a bonded quorum commits an on-chain
// revocation over the wire, and then — the load-bearing F5 property — the
// takedown is honored PER OPERATOR, never globally. A subscribing operator denies
// the root; an operator that follows the very same chain but did not subscribe
// does not. And a quorum cannot revoke a root the chain never committed.
func TestF5RevocationIsPerOperatorAndExistenceCheckedOverTheLoop(t *testing.T) {
	const (
		seed     = int64(13)
		V        = 4
		bondSize = 8 << 20
	)
	ledger := credit.New(50_000, 0)
	repFn := func(n ports.NodeID) int64 { return ledger.Reputation(n) }
	cfg := chain.DefaultConfig() // quorum 3, rep 100
	nodeCfg := node.DefaultConfig()

	sched := simclock.New()
	net := simnet.New(sched, seed, simnet.DefaultConfig())

	var vals []*node.Node
	var ids []ports.NodeID
	for i := 0; i < V; i++ {
		ident := identity.FromSeed(seed*1000 + int64(i))
		st := memstore.New()
		nd := node.New(ident.NodeID(), nodeCfg, sched, net.Endpoint(ident.NodeID()), st)
		nd.SetLedger(ledger)
		nd.EnableBond(ident.Signer(), bondSize)
		ch := chain.New(cfg, repFn)
		if gb, _, _, gerr := genesis.Build(st); gerr == nil {
			ch.AppendGenesis(gb)
		}
		nd.EnableChain(ch, ident.Signer())
		vals = append(vals, nd)
		ids = append(ids, ident.NodeID())
	}
	for i := 1; i < V; i++ {
		vals[i].Bootstrap([]ports.NodeID{ids[0]}, func() {})
	}
	sched.Run()
	for _, nd := range vals {
		nd.AuditBondsOnce() // earn standing so the quorum can propose/attest
	}
	sched.Run()

	// Publish a root over the loop (so it EXISTS on the chain).
	root := ports.HashBytes([]byte("target-root"))
	e := ports.Entry{Root: root, ManifestChunks: []ports.ChunkID{ports.HashBytes([]byte("m"))}}
	if err := proposeEntry(vals[0], e, ids[1:], ids, cfg.Quorum, sched); err != nil {
		t.Fatalf("publishing the target root should commit: %v", err)
	}

	// A quorum commits an on-chain revocation of that root, broadcast to all.
	if err := proposeRevocation(vals[0], []ports.Hash{root}, ids[1:], ids, cfg.Quorum, sched); err != nil {
		t.Fatalf("a bonded quorum should be able to revoke a committed root: %v", err)
	}
	// Every replica now carries the revocation on its chain...
	for i, nd := range vals {
		if !nd.Chain().Revoked(root) {
			t.Fatalf("replica %d should carry the committed revocation on-chain", i)
		}
	}

	// ...but honoring is PER OPERATOR. Two nodes on the identical chain:
	sub, unsub := vals[1], vals[2]
	sub.SetHonorChainRevocations(true) // this operator subscribed
	// unsub keeps the default (off)
	if !sub.WouldDeny(root) {
		t.Fatal("F5: a SUBSCRIBING operator must deny a quorum-revoked root")
	}
	if unsub.WouldDeny(root) {
		t.Fatal("F5 regression: an operator that did NOT subscribe must NOT deny the root — " +
			"chain revocation is not a global switch")
	}

	// Existence check: a quorum cannot revoke a root the chain never committed.
	unknown := ports.HashBytes([]byte("never-published"))
	err := proposeRevocation(vals[0], []ports.Hash{unknown}, ids[1:], ids, cfg.Quorum, sched)
	if err == nil || !errors.Is(err, chain.ErrRevokeUnknownRoot) {
		t.Fatalf("F5: revoking a root never committed must be refused, got %v", err)
	}
}

func proposeEntry(n *node.Node, e ports.Entry, attesters, broadcast []ports.NodeID, quorum int, sched *simclock.Scheduler) error {
	var err error
	done := false
	n.ProposeEntry(e, attesters, broadcast, quorum, func(e error) { err, done = e, true })
	sched.Run()
	if !done {
		return errNotDone
	}
	return err
}

func proposeRevocation(n *node.Node, roots []ports.Hash, attesters, broadcast []ports.NodeID, quorum int, sched *simclock.Scheduler) error {
	var err error
	done := false
	n.ProposeRevocation(roots, attesters, broadcast, quorum, func(e error) { err, done = e, true })
	sched.Run()
	if !done {
		return errNotDone
	}
	return err
}
