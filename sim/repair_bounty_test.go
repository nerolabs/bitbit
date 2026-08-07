package sim

import (
	"bytes"
	"testing"

	"github.com/nerolabs/silt/adapters/simnet"
	"github.com/nerolabs/silt/core/credit"
	"github.com/nerolabs/silt/core/crypto"
	"github.com/nerolabs/silt/core/erasure"
	"github.com/nerolabs/silt/core/node"
	"github.com/nerolabs/silt/core/pipeline"
	"github.com/nerolabs/silt/ports"
)

// TestRepairBountyPaysHolderWithoutMovingStanding is the H7 slice-2 wiring at the
// integration tier: a churned stripe is repaired, the repairing caretaker emits a
// MsgRepairClaim, a peer caretaker independently verifies BOTH legs (recompute
// correctness + identity-bound retrievability against the fresh holder) and
// releases the object's durability bounty from escrow — and NO node's consensus
// standing moves. That last clause is the load-bearing invariant: durability
// credit funds the balance economy and confers zero standing, so the γ→1/N
// shared-content sealing hole stays shut through the bounty path.
func TestRepairBountyPaysHolderWithoutMovingStanding(t *testing.T) {
	const seed = 20260807
	cfg := node.DefaultConfig()
	cfg.Replication = 1 // parity is the only redundancy → churn bites and repair fires
	cfg.RepairBountyBase = 1000
	cl := NewCluster(seed, 48, simnet.DefaultConfig(), cfg)

	// One shared ledger, generously granted so a funder can prepay the reserve.
	ledger := credit.New(50_000, 100<<20)
	baseline := map[ports.NodeID]int64{}
	for _, nd := range cl.Nodes {
		nd.SetLedger(ledger)
		ledger.Register(nd.ID())
		// Seed every node with real bonded standing, so a spurious standing motion
		// anywhere (a bounty that minted reputation, an erroneous slash) is visible.
		ledger.RecordBondChallenge(nd.ID(), nd.ID(), 64<<20, true, 1)
		baseline[nd.ID()] = ledger.Reputation(nd.ID())
	}

	publisher := cl.Nodes[0]
	caretakers := cl.Nodes[1:4] // one will repair, the others judge
	eras := erasure.DefaultParams

	data := make([]byte, 512<<10)
	cl.rng.Read(data)
	h, err := pipeline.Add(bgCtx, publisher.Store(), cl.Registry, bytes.NewReader(data),
		pipeline.Options{ChunkSize: 4 << 10, Mode: crypto.Convergent, Erasure: eras})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	root := h.Root
	entry, _, _ := cl.Registry.Lookup(bgCtx, root)
	m, err := pipeline.LoadFull(bgCtx, publisher.Store(), entry, h)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	distributed := false
	publisher.Distribute(entry, m, false, node.DerivePorKey(h.LayoutKey()), func(int, error) { distributed = true })
	cl.Sched.Run()
	if !distributed {
		t.Fatal("distribution never completed")
	}

	// Prepay a durability reserve for the object.
	const reserve = 5_000_000
	if err := ledger.FundEscrow(root, publisher.ID(), reserve); err != nil {
		t.Fatalf("fund escrow: %v", err)
	}

	// The caretakers come on duty (and announce themselves under the careKey
	// rendezvous, so a repairing caretaker can find the quorum).
	protected := map[ports.NodeID]bool{publisher.ID(): true}
	for _, c := range caretakers {
		c.Care(cl.Registry, h.Care())
		protected[c.ID()] = true
	}
	cl.Sched.RunUntil(cl.Sched.Now().Add(30 * ports.Second))

	// A kill wave that drops a stripe past the repair slack, then repair windows.
	killed := cl.KillColumns(m, protected, eras, cfg.RepairSlack, 12)
	if killed == 0 {
		t.Fatal("kill wave removed no column-holders — scenario proves nothing")
	}
	cl.Sched.RunUntil(cl.Sched.Now().Add(12 * cfg.RepairInterval))

	// Outcome, not mechanism.
	claims := sumStat(caretakers, func(s node.Stats) int { return s.RepairClaims })
	released := sumStat(caretakers, func(s node.Stats) int { return s.BountiesReleased })
	falseSlashes := sumStat(caretakers, func(s node.Stats) int { return s.FalseRepairSlashes })
	rebuilt := sumStat(caretakers, func(s node.Stats) int { return s.ShardsRebuilt })
	t.Logf("killed %d | shards rebuilt %d | repair claims %d | bounties released %d | false-repair slashes %d | EscrowPaid %d",
		killed, rebuilt, claims, released, falseSlashes, ledger.EscrowPaid(root))

	if rebuilt == 0 {
		t.Fatal("no shard was rebuilt — repair never fired, so the bounty path was never exercised")
	}
	if claims == 0 {
		t.Fatal("a repair happened but no MsgRepairClaim was emitted")
	}
	if released == 0 {
		t.Fatal("a claim was emitted but no caretaker-judge released the bounty — the quorum/verify path failed")
	}
	if falseSlashes != 0 {
		t.Fatalf("%d honest repairs were slashed as false — the correctness leg is rejecting real repairs", falseSlashes)
	}
	if paid := ledger.EscrowPaid(root); paid <= 0 {
		t.Fatalf("EscrowPaid = %d, want > 0 (bounties should have drawn the reserve)", paid)
	}
	// The reserve is funded = prepay + serve auto-skim (serving shards during the
	// repair windows tops it up), drawn down by what bounties paid.
	if funded := ledger.EscrowFunded(root); funded < reserve {
		t.Fatalf("EscrowFunded %d < prepay %d — the prepay should be the floor", funded, reserve)
	}
	if bal := ledger.EscrowBalance(root); bal != ledger.EscrowFunded(root)-ledger.EscrowPaid(root) {
		t.Fatalf("escrow balance %d != funded %d - paid %d", bal, ledger.EscrowFunded(root), ledger.EscrowPaid(root))
	}

	// Slice-3 instrument: the real repair loop feeds the finite-but-renewable
	// accounting. The snapshot's repair count matches the bounties released, the
	// realised cost-per-repair is positive, and the funded horizon over the elapsed
	// sim window is finite (a real, re-endowable runway — never a silent "perpetual").
	snap := cl.Nodes[1].DurabilitySnapshot(root)
	if snap.Repairs != int64(released) {
		t.Fatalf("snapshot repairs %d != bounties released %d", snap.Repairs, released)
	}
	if cpr := credit.CostPerRepair(snap); cpr <= 0 {
		t.Fatalf("cost-per-repair = %d, want > 0 once repairs are funded", cpr)
	}
	if rem, finite := credit.Horizon(snap, ports.Duration(cl.Sched.Now())); !finite || rem <= 0 {
		t.Fatalf("funded horizon = (%d, %v), want a positive finite runway", rem, finite)
	}

	// THE INVARIANT: not one node's standing moved through the whole bounty path.
	for _, nd := range cl.Nodes {
		if got := ledger.Reputation(nd.ID()); got != baseline[nd.ID()] {
			t.Fatalf("node %s standing moved from %d to %d — the durability bounty must confer ZERO standing",
				nd.ID().String()[:8], baseline[nd.ID()], got)
		}
	}
}
