package node

import (
	"testing"

	"github.com/nerolabs/silt/adapters/identity"
	"github.com/nerolabs/silt/adapters/memstore"
	"github.com/nerolabs/silt/adapters/simclock"
	"github.com/nerolabs/silt/adapters/simnet"
	"github.com/nerolabs/silt/core/credit"
	"github.com/nerolabs/silt/core/erasure"
	"github.com/nerolabs/silt/core/repairproof"
	"github.com/nerolabs/silt/ports"
)

// mkJudge builds a minimal caretaker-judge node with a ledger — enough to
// exercise settleRepairVerdict, which touches only the ledger, config, and stats.
func mkJudge(t *testing.T, idSeed int64) (*Node, *credit.Ledger) {
	t.Helper()
	sched := simclock.New()
	net := simnet.New(sched, 1, simnet.DefaultConfig())
	ident := identity.FromSeed(idSeed)
	cfg := DefaultConfig()
	cfg.RepairBountyBase = 1000
	nd := New(ident.NodeID(), cfg, sched, net.Endpoint(ident.NodeID()), memstore.New())
	ledger := credit.New(50_000, 1_000_000) // grant funders enough to prepay a reserve
	nd.SetLedger(ledger)
	return nd, ledger
}

// bondedStanding gives id a comfortable bonded reputation and returns it — the
// baseline a slash must lower and a bounty payment must leave untouched.
func bondedStanding(l *credit.Ledger, id ports.NodeID) int64 {
	const bond = 128 << 20
	l.RecordBondChallenge(id, id, bond, true, 1)
	return l.Reputation(id)
}

// TestSettleRepairVerdict_ReleasePaysHolderNeverStanding is THE load-bearing
// invariant at the wiring tier: a released bounty moves BALANCE from the object's
// escrow to the holder and touches NO standing — not the holder's, not the
// claimant's. If a bounty could mint reputation, the γ→1/N shared-content sealing
// hole would reopen (one physical replica buying N nodes' standing). The judge
// pays the new holder (paramedic split, §8b), draws it from the object reserve,
// and reputation stays frozen.
func TestSettleRepairVerdict_ReleasePaysHolderNeverStanding(t *testing.T) {
	nd, l := mkJudge(t, 1)
	holder := identity.FromSeed(10).NodeID()
	claimant := identity.FromSeed(11).NodeID()

	var root ports.Hash
	root[0] = 0xC0
	funder := identity.FromSeed(12).NodeID()
	l.Register(funder)
	if err := l.FundEscrow(root, funder, 20_000); err != nil {
		t.Fatalf("fund escrow: %v", err)
	}

	// Both holder and claimant carry real bonded standing, so a spurious motion
	// would be visible.
	holderStanding := bondedStanding(l, holder)
	claimantStanding := bondedStanding(l, claimant)
	holderBalance := l.Balance(holder)
	fundedBefore := l.EscrowFunded(root)

	// A healthy stripe (8 of 10 reachable → lost 2 → 3× multiplier).
	p := erasure.Params{K: 6, N: 10}
	claim := repairproof.RepairClaim{Root: root, Stripe: 0, ShardPos: 7, Holder: holder}
	nd.settleRepairVerdict(claimant, claim, p, 8, repairproof.Decision{Release: true})

	wantBounty := credit.BountyFor(nd.cfg.RepairBountyBase, p.K, p.N, 8)
	if wantBounty <= 0 {
		t.Fatal("test setup: bounty should be positive")
	}
	if got := l.Balance(holder) - holderBalance; got != wantBounty {
		t.Fatalf("holder balance moved by %d, want the bounty %d", got, wantBounty)
	}
	if got := l.EscrowPaid(root); got != wantBounty {
		t.Fatalf("EscrowPaid = %d, want %d", got, wantBounty)
	}
	if got := l.EscrowBalance(root); got != fundedBefore-wantBounty {
		t.Fatalf("escrow balance = %d, want %d (reserve drawn down by the bounty)", got, fundedBefore-wantBounty)
	}
	// THE INVARIANT: no standing moved for anyone.
	if got := l.Reputation(holder); got != holderStanding {
		t.Fatalf("holder standing moved from %d to %d — a bounty must NEVER mint standing", holderStanding, got)
	}
	if got := l.Reputation(claimant); got != claimantStanding {
		t.Fatalf("claimant standing moved from %d to %d on a release", claimantStanding, got)
	}
	if nd.Stats.BountiesReleased != 1 {
		t.Fatalf("BountiesReleased = %d, want 1", nd.Stats.BountiesReleased)
	}
}

// TestSettleRepairVerdict_SlashDocksClaimantOnly: a proven false-correctness claim
// docks the CLAIMANT's standing (reduces-class, self-attributing) and pays no
// bounty — the holder is untouched and the escrow is not drawn.
func TestSettleRepairVerdict_SlashDocksClaimantOnly(t *testing.T) {
	nd, l := mkJudge(t, 2)
	holder := identity.FromSeed(20).NodeID()
	claimant := identity.FromSeed(21).NodeID()

	var root ports.Hash
	root[0] = 0xD0
	funder := identity.FromSeed(22).NodeID()
	l.Register(funder)
	_ = l.FundEscrow(root, funder, 20_000)

	claimantStanding := bondedStanding(l, claimant)
	holderStanding := bondedStanding(l, holder)
	holderBalance := l.Balance(holder)

	p := erasure.Params{K: 6, N: 10}
	claim := repairproof.RepairClaim{Root: root, Stripe: 0, ShardPos: 7, Holder: holder}
	nd.settleRepairVerdict(claimant, claim, p, 8, repairproof.Decision{Slash: true})

	if got := l.Reputation(claimant); got >= claimantStanding {
		t.Fatalf("claimant standing did not drop: %d >= %d", got, claimantStanding)
	}
	if got := l.Reputation(holder); got != holderStanding {
		t.Fatalf("holder standing moved on a slash of the CLAIMANT: %d != %d", got, holderStanding)
	}
	if got := l.Balance(holder); got != holderBalance {
		t.Fatalf("holder was paid on a slash verdict: balance %d != %d", got, holderBalance)
	}
	if got := l.EscrowPaid(root); got != 0 {
		t.Fatalf("escrow was drawn on a slash verdict: EscrowPaid %d", got)
	}
	if nd.Stats.FalseRepairSlashes != 1 {
		t.Fatalf("FalseRepairSlashes = %d, want 1", nd.Stats.FalseRepairSlashes)
	}
}

// TestSettleRepairVerdict_DenyMovesNothing: a retrievability shortfall (deny, no
// slash) is the safe transient failure mode — it pays nothing and slashes nobody.
func TestSettleRepairVerdict_DenyMovesNothing(t *testing.T) {
	nd, l := mkJudge(t, 3)
	holder := identity.FromSeed(30).NodeID()
	claimant := identity.FromSeed(31).NodeID()

	var root ports.Hash
	root[0] = 0xE0
	funder := identity.FromSeed(32).NodeID()
	l.Register(funder)
	_ = l.FundEscrow(root, funder, 20_000)
	claimantStanding := bondedStanding(l, claimant)
	holderBalance := l.Balance(holder)

	p := erasure.Params{K: 6, N: 10}
	claim := repairproof.RepairClaim{Root: root, Stripe: 0, ShardPos: 7, Holder: holder}
	nd.settleRepairVerdict(claimant, claim, p, 8, repairproof.Decision{}) // deny

	if got := l.Reputation(claimant); got != claimantStanding {
		t.Fatalf("deny moved the claimant's standing: %d != %d", got, claimantStanding)
	}
	if l.EscrowPaid(root) != 0 || l.Balance(holder) != holderBalance {
		t.Fatal("deny paid a bounty")
	}
	if nd.Stats.BountiesReleased != 0 || nd.Stats.FalseRepairSlashes != 0 {
		t.Fatal("deny recorded a settlement")
	}
}

// TestEmitRepairClaim_GuardsAreNoOps: the emit hook stays silent (no claim, no
// lookup) when the bounty economy is off, the shard carries no PoR tags, or no
// holder accepted the shard — a claim no one could verify or pay is never sent.
func TestEmitRepairClaim_GuardsAreNoOps(t *testing.T) {
	nd, _ := mkJudge(t, 4)
	var root ports.Hash
	root[0] = 0xAA
	r := shardRef{id: ports.HashBytes([]byte("shard")), stripe: 0, pos: 7}
	holder := identity.FromSeed(40).NodeID()

	// No PoR tags: the holder could never answer retrievability.
	nd.emitRepairClaim(root, r, holder, false)
	// No holder recorded.
	nd.emitRepairClaim(root, r, ports.NodeID{}, true)
	// Bounty economy disabled.
	nd.cfg.RepairBountyBase = 0
	nd.emitRepairClaim(root, r, holder, true)

	if nd.Stats.RepairClaims != 0 {
		t.Fatalf("a guarded emit still sent a claim: RepairClaims = %d", nd.Stats.RepairClaims)
	}
}
