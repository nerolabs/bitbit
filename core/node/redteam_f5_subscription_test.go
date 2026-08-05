package node

// RED-TEAM REGRESSION — accountability corner (red-team F5), node layer.
//
// The break: node.isDenied() unioned chain.Revoked() UNCONDITIONALLY, so every
// node following the canonical chain enforced every quorum-committed takedown
// with no opt-out — a global switch. The fix makes chain-revocation honoring a
// per-operator subscription (SetHonorChainRevocations), default OFF. The
// operator-local denylist is always honored. This white-box test drives
// isDenied directly.

import (
	"crypto/ed25519"
	"testing"

	"github.com/nerolabs/silt/adapters/identity"
	"github.com/nerolabs/silt/adapters/memstore"
	"github.com/nerolabs/silt/adapters/simclock"
	"github.com/nerolabs/silt/adapters/simnet"
	"github.com/nerolabs/silt/core/chain"
	"github.com/nerolabs/silt/core/credit"
	"github.com/nerolabs/silt/core/denylist"
	"github.com/nerolabs/silt/ports"
)

func TestF5_ChainRevocationHonoringIsOptIn(t *testing.T) {
	sched := simclock.New()
	net := simnet.New(sched, 1, simnet.DefaultConfig())
	ledger := credit.New(50_000, 0)
	repFn := func(n ports.NodeID) int64 { return ledger.Reputation(n) }

	ti := identity.FromSeed(1)
	tid := ti.NodeID()
	n := New(tid, DefaultConfig(), sched, net.Endpoint(tid), memstore.New())
	n.SetLedger(ledger)

	// A chain with a published-then-revoked root.
	prop := identity.FromSeed(2)
	ch := chain.New(chain.DefaultConfig(), repFn)
	root := ports.HashBytes([]byte("target-root"))
	g := &chain.Block{Version: chain.BlockVersion, Height: 0, Entries: []ports.Entry{{
		Root: root, ManifestChunks: []ports.ChunkID{ports.HashBytes([]byte("m"))},
	}}}
	chain.Sign(g, prop.Signer())
	if err := ch.AppendGenesis(*g); err != nil {
		t.Fatalf("genesis: %v", err)
	}
	n.EnableChain(ch, ti.Signer())
	commitRevocation(t, ch, prop.Signer(), repFn, ledger, prop.NodeID(), root)

	if !ch.Revoked(root) {
		t.Fatal("precondition: root should be revoked on the chain")
	}

	// Default: NOT subscribed → the node does not enforce the chain takedown.
	if n.isDenied(root) {
		t.Fatal("chain revocation must NOT be enforced by default (per-operator, not global)")
	}

	// Opt in → now enforced.
	n.SetHonorChainRevocations(true)
	if !n.isDenied(root) {
		t.Fatal("after subscribing, the chain revocation must be enforced")
	}

	// Opt back out → not enforced again (reversible operator choice).
	n.SetHonorChainRevocations(false)
	if n.isDenied(root) {
		t.Fatal("after unsubscribing, the chain revocation must no longer be enforced")
	}

	// The operator-local denylist is honored regardless of subscription.
	dl := denylist.New()
	dl.Add(root)
	n.SetDenylist(dl)
	if !n.isDenied(root) {
		t.Fatal("operator-local denylist must always be honored")
	}
}

// commitRevocation appends a quorum-attested revocation-only block. Kept local
// to this test so it exercises the real Append path.
func commitRevocation(t *testing.T, ch *chain.Chain, prop ed25519.PrivateKey, repFn func(ports.NodeID) int64, ledger *credit.Ledger, propID ports.NodeID, root ports.Hash) {
	t.Helper()
	// Give the proposer + a quorum of attesters standing via the BOND press —
	// PoR audits no longer mint standing (H1). Each identity proves a distinct
	// bond root (dedup-safe); 64 MiB ⇒ 1024 points, well over the 100 bar.
	atts := []*identity.Identity{identity.FromSeed(3), identity.FromSeed(4), identity.FromSeed(5)}
	ledger.RecordBondChallenge(propID, ports.HashBytes([]byte{9}), 64<<20, true, 1)
	for i, at := range atts {
		ledger.RecordBondChallenge(at.NodeID(), ports.HashBytes([]byte{byte(20 + i)}), 64<<20, true, 1)
	}
	prev, height := ch.Head()
	rev := &chain.Block{Version: chain.BlockVersion, Height: height, Prev: prev, Revocations: []ports.Hash{root}}
	chain.Sign(rev, prop)
	for _, at := range atts {
		rev.Atts = append(rev.Atts, chain.Attest(rev, at.Signer()))
	}
	if err := ch.Append(*rev); err != nil {
		t.Fatalf("revocation commit failed: %v", err)
	}
}
