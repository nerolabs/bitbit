package sim

import (
	"crypto/ed25519"
	"testing"

	"github.com/nerolabs/silt/adapters/identity"
	"github.com/nerolabs/silt/adapters/memstore"
	"github.com/nerolabs/silt/adapters/simclock"
	"github.com/nerolabs/silt/adapters/simnet"
	"github.com/nerolabs/silt/core/chain"
	"github.com/nerolabs/silt/core/credit"
	"github.com/nerolabs/silt/core/node"
	"github.com/nerolabs/silt/ports"
)

// Integration proof for M0 hardening H2 / red-team RT-2 (release-and-coast),
// over the REAL wire. A bond TTL that decays un-renewed standing is only safe if
// an honest validator can renew WITHOUT proposing — otherwise defaulting it on is
// a liveness trap (an attest-only validator would lapse and drop the quorum's
// weight; strategy doc §3). This drives the automatic non-proposer renewal path:
// each round every validator SUBMITS a fresh bond proof (MsgSubmitBondReg) to its
// peers, and the single proposer folds the queued peer renewals into its block —
// so an attest-only validator sustains standing across many TTL windows while
// never once proposing. The paired control: a validator that RELEASES its plot
// (stops submitting) is pruned within the TTL, which is the whole point of the
// default. Both halves run with TTL ON.
func TestObjectiveBondRenewalSustainsAttestOnlyValidator(t *testing.T) {
	const (
		seed     = int64(41)
		N        = 4
		bondSize = int64(1) << 20
		ttl      = uint64(3) // tight: a validator missing renewals lapses fast
		rounds   = 8         // spans multiple TTL windows
	)
	// OBJECTIVE mode with the TTL on. Quorum 2 stays reachable even after the
	// released validator lapses (two honest attesters remain).
	cfg := chain.Config{Quorum: 2, MinBond: 1 << 20, BondTTLBlocks: ttl}

	sched := simclock.New()
	net := simnet.New(sched, seed, simnet.DefaultConfig())
	// A fast bond config for the test: prover and verifier read the SAME node
	// config (EnableObjectiveChain / RegisterBondReg), so lowering the VDF delay
	// and label samples uniformly keeps them in agreement while cutting the cost
	// of the dozens of real space-time proofs this many-round scenario runs.
	ncfg := node.DefaultConfig()
	ncfg.BondVDFDelay = 32
	ncfg.BondLabelSamples = 8

	// Genesis declares every validator's launch bond, so all four start in the
	// objective set and can attest from block 1.
	var ids []ports.NodeID
	var regs []chain.BondReg
	for i := 0; i < N; i++ {
		id := identity.FromSeed(seed*1000 + int64(i))
		ids = append(ids, id.NodeID())
		pub := id.Signer().Public().(ed25519.PublicKey)
		regs = append(regs, chain.BondReg{Validator: append([]byte(nil), pub...), Root: ports.HashBytes(pub), Size: bondSize})
	}
	g := &chain.Block{Version: chain.BlockVersion, Height: 0, Entries: []ports.Entry{simEntry("genesis")}, BondRegs: regs}
	chain.Sign(g, identity.FromSeed(1).Signer())

	var nodes []*node.Node
	for i := 0; i < N; i++ {
		id := identity.FromSeed(seed*1000 + int64(i))
		nd := node.New(id.NodeID(), ncfg, sched, net.Endpoint(id.NodeID()), memstore.New())
		nd.SetLedger(credit.New(50_000, 0))
		perNode := credit.New(50_000, 0)
		ch := chain.New(cfg, func(n ports.NodeID) int64 { return perNode.Reputation(n) })
		if err := ch.AppendGenesis(*g); err != nil {
			t.Fatalf("genesis: %v", err)
		}
		nd.EnableChain(ch, id.Signer())
		nd.EnableObjectiveChain()
		nd.EnableBond(id.Signer(), bondSize) // hold a REAL plot so renewals verify
		nodes = append(nodes, nd)
	}
	for i := 1; i < N; i++ {
		nodes[i].Bootstrap([]ports.NodeID{ids[0]}, func() {})
	}
	sched.Run()

	proposer := nodes[0] // the ONLY node that ever proposes
	// The attest-only validators: indices 1..N-1. nodes[1] is the one we assert
	// sustains standing; nodes[N-1] is the "released plot" control (stops renewing).
	attestOnly := ids[1:]
	releasedIdx := N - 1

	// Each round: every still-honest validator submits a fresh renewal to all
	// peers, then the proposer commits a block folding in the queued renewals.
	for r := 0; r < rounds; r++ {
		for i := 0; i < N; i++ {
			if i == releasedIdx && r >= 1 {
				continue // the released validator stops renewing after round 0
			}
			nodes[i].SubmitBondRenewal(ids)
		}
		sched.Run() // deliver the submissions so the proposer queues them
		if err := propose(proposer, "r"+string(rune('a'+r)), attestOnly, ids, cfg.Quorum, sched); err != nil {
			t.Fatalf("round %d propose: %v", r, err)
		}
	}

	ch := proposer.Chain()
	// The attest-only validator that kept renewing must still hold full standing,
	// though it NEVER proposed — the renewal reached the chain via a peer's block.
	if got := ch.BondedSize(attestOnly[0]); got != bondSize {
		t.Fatalf("RT-2 liveness regression: an attest-only validator that kept renewing lost standing: got %d, want %d — the non-proposer renewal path is not sustaining it", got, bondSize)
	}
	// The released validator (stopped renewing after round 0) must be pruned — the
	// release-and-coast attack denied by the default TTL.
	if got := ch.BondedSize(ids[releasedIdx]); got != 0 {
		t.Fatalf("RT-2 regression: a validator that released its plot kept %d standing after %d rounds with TTL %d — release-and-coast survived", got, rounds, ttl)
	}
	// The proposer itself renews as it proposes, so it never lapses (sanity).
	if got := ch.BondedSize(ids[0]); got != bondSize {
		t.Fatalf("the proposer should renew itself while proposing: got %d, want %d", got, bondSize)
	}
}
