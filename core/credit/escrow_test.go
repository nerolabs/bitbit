package credit

import (
	"testing"

	"github.com/nerolabs/silt/ports"
)

func objRoot(s string) ports.Hash { return ports.HashBytes([]byte(s)) }

// TestFundEscrow_MovesBalanceIntoReserve: prepaying a reserve deducts the
// funder's balance and credits the object, leaving standing untouched.
func TestFundEscrow_MovesBalanceIntoReserve(t *testing.T) {
	l := New(50_000, 1_000_000)
	funder := id(1)
	root := objRoot("obj-A")

	if err := l.FundEscrow(root, funder, 300_000); err != nil {
		t.Fatalf("fund: %v", err)
	}
	if got := l.Balance(funder); got != 700_000 {
		t.Fatalf("funder balance = %d, want 700000 (1_000_000 - 300_000)", got)
	}
	if got := l.EscrowBalance(root); got != 300_000 {
		t.Fatalf("escrow balance = %d, want 300000", got)
	}
	if got := l.EscrowFunded(root); got != 300_000 {
		t.Fatalf("escrow funded = %d, want 300000", got)
	}
	if got := l.Reputation(funder); got != 0 {
		t.Fatalf("funding a reserve must not move standing: rep = %d, want 0", got)
	}
}

// TestFundEscrow_InsufficientCreditIsNoOp: a funder that cannot cover the amount
// gets ErrInsufficientCredit and NOTHING moves.
func TestFundEscrow_InsufficientCreditIsNoOp(t *testing.T) {
	l := New(50_000, 100)
	funder := id(1)
	root := objRoot("obj-A")

	if err := l.FundEscrow(root, funder, 500); err != ports.ErrInsufficientCredit {
		t.Fatalf("err = %v, want ErrInsufficientCredit", err)
	}
	if got := l.Balance(funder); got != 100 {
		t.Fatalf("balance moved on a failed fund: %d, want 100", got)
	}
	if got := l.EscrowBalance(root); got != 0 {
		t.Fatalf("escrow credited on a failed fund: %d, want 0", got)
	}
}

// TestFundEscrow_NonPositiveIsNoOp: zero/negative amounts do nothing (no error,
// no movement) rather than corrupting a reserve.
func TestFundEscrow_NonPositiveIsNoOp(t *testing.T) {
	l := New(50_000, 1_000)
	funder := id(1)
	root := objRoot("obj-A")
	for _, amt := range []int64{0, -1, -1_000_000} {
		if err := l.FundEscrow(root, funder, amt); err != nil {
			t.Fatalf("fund(%d): %v", amt, err)
		}
	}
	if got := l.Balance(funder); got != 1_000 {
		t.Fatalf("balance moved on a non-positive fund: %d", got)
	}
	if got := l.EscrowBalance(root); got != 0 {
		t.Fatalf("escrow credited on a non-positive fund: %d", got)
	}
}

// TestRecordServeToObject_AutoSkim: an object-aware serve pays the server the net
// and diverts the SkimNum/SkimDen fraction into that object's reserve, so popular
// data self-funds its durability. Full bytes are still counted as served.
func TestRecordServeToObject_AutoSkim(t *testing.T) {
	l := New(50_000, 0)
	server, requester := id(1), id(2)
	root := objRoot("obj-A")

	const bytes = 80_000
	wantSkim := int64(bytes) * SkimNum / SkimDen // 80_000 / 8 = 10_000
	skim := l.RecordServeToObject(server, requester, root, id(9), bytes)

	if skim != wantSkim {
		t.Fatalf("skim = %d, want %d", skim, wantSkim)
	}
	if got := l.Balance(server); got != bytes-wantSkim {
		t.Fatalf("server balance = %d, want %d (net of skim)", got, bytes-wantSkim)
	}
	if got := l.EscrowBalance(root); got != wantSkim {
		t.Fatalf("escrow balance = %d, want %d", got, wantSkim)
	}
	if got := l.ServedBytes(server); got != bytes {
		t.Fatalf("served bytes = %d, want %d (full volume counted)", got, bytes)
	}
	if got := l.FetchedBytes(requester); got != bytes {
		t.Fatalf("fetched bytes = %d, want %d", got, bytes)
	}
	if got := l.Reputation(server); got != 0 {
		t.Fatalf("object-aware serving must not move standing: rep = %d", got)
	}
}

// TestRecordServeToObject_SelfServeEarnsNothing: serving yourself mints no credit
// and skims nothing — the cheapest gaming, blocked exactly as in RecordServe.
func TestRecordServeToObject_SelfServeEarnsNothing(t *testing.T) {
	l := New(50_000, 0)
	n := id(1)
	root := objRoot("obj-A")
	if skim := l.RecordServeToObject(n, n, root, id(9), 1<<40); skim != 0 {
		t.Fatalf("self-serve skim = %d, want 0", skim)
	}
	if got := l.Balance(n); got != 0 {
		t.Fatalf("self-serve balance = %d, want 0", got)
	}
	if got := l.EscrowBalance(root); got != 0 {
		t.Fatalf("self-serve escrow = %d, want 0", got)
	}
}

// TestPayBounty_DrawsDownReserve: a paid bounty moves credit from the reserve to
// the repairer's balance and records the lifetime paid total.
func TestPayBounty_DrawsDownReserve(t *testing.T) {
	l := New(50_000, 1_000_000)
	funder, repairer := id(1), id(2)
	root := objRoot("obj-A")
	if err := l.FundEscrow(root, funder, 500_000); err != nil {
		t.Fatalf("fund: %v", err)
	}

	paid := l.PayBounty(root, repairer, 120_000)
	if paid != 120_000 {
		t.Fatalf("paid = %d, want 120000", paid)
	}
	if got := l.EscrowBalance(root); got != 380_000 {
		t.Fatalf("escrow balance = %d, want 380000", got)
	}
	if got := l.EscrowPaid(root); got != 120_000 {
		t.Fatalf("escrow paid = %d, want 120000", got)
	}
	if got := l.Balance(repairer); got != 1_120_000 {
		t.Fatalf("repairer balance = %d, want 1120000", got)
	}
	if got := l.Reputation(repairer); got != 0 {
		t.Fatalf("earning a bounty must not move standing: rep = %d", got)
	}
}

// TestPayBounty_PartialWhenUnderfunded: a reserve short of the owed bounty pays
// what is left and no more — that is the object's funded horizon running out
// (finite-but-renewable), not an overdraft.
func TestPayBounty_PartialWhenUnderfunded(t *testing.T) {
	l := New(50_000, 1_000_000)
	funder, repairer := id(1), id(2)
	root := objRoot("obj-A")
	if err := l.FundEscrow(root, funder, 30_000); err != nil {
		t.Fatalf("fund: %v", err)
	}

	paid := l.PayBounty(root, repairer, 100_000) // owed more than the reserve holds
	if paid != 30_000 {
		t.Fatalf("paid = %d, want 30000 (all the reserve had)", paid)
	}
	if got := l.EscrowBalance(root); got != 0 {
		t.Fatalf("reserve should be drained to 0, got %d", got)
	}
	// A second claim on an empty reserve pays nothing (no negative escrow).
	if again := l.PayBounty(root, repairer, 100_000); again != 0 {
		t.Fatalf("paying from an empty reserve returned %d, want 0", again)
	}
	if got := l.EscrowBalance(root); got != 0 {
		t.Fatalf("empty reserve went negative: %d", got)
	}
}

// TestPayBounty_UnknownOrNonPositiveIsZero: paying against an object with no
// reserve, or a non-positive amount, is a clean no-op returning 0.
func TestPayBounty_UnknownOrNonPositiveIsZero(t *testing.T) {
	l := New(50_000, 0)
	repairer := id(2)
	if got := l.PayBounty(objRoot("never-funded"), repairer, 1_000); got != 0 {
		t.Fatalf("bounty on an unfunded object = %d, want 0", got)
	}
	root := objRoot("obj-A")
	_ = l.FundEscrow(root, id(1), 0) // touches nothing
	for _, amt := range []int64{0, -1} {
		if got := l.PayBounty(root, repairer, amt); got != 0 {
			t.Fatalf("PayBounty(amount=%d) = %d, want 0", amt, got)
		}
	}
	if got := l.Balance(repairer); got != 0 {
		t.Fatalf("repairer balance moved on a no-op bounty: %d", got)
	}
}

// TestBountyFor_RarestShardMultiplier: the bounty rises monotonically as a stripe
// loses shards — repairing the last spare before data loss pays the most.
func TestBountyFor_RarestShardMultiplier(t *testing.T) {
	const base = 1_000
	const k, n = 4, 10 // 6 parity shards of slack

	// A fully-healthy stripe pays the base; each loss adds one base unit.
	cases := []struct {
		reachable int
		want      int64
	}{
		{10, 1_000}, // 0 lost → 1×
		{9, 2_000},  // 1 lost → 2×
		{7, 4_000},  // 3 lost → 4×
		{5, 6_000},  // 5 lost → 6×
		{4, 7_000},  // 6 lost (at the k-floor) → (n-k+1)=7×, the max
	}
	for _, c := range cases {
		if got := BountyFor(base, k, n, c.reachable); got != c.want {
			t.Fatalf("BountyFor(base=%d,k=%d,n=%d,reachable=%d) = %d, want %d",
				base, k, n, c.reachable, got, c.want)
		}
	}

	// Strict monotonicity: fewer reachable ⇒ never a smaller bounty.
	prev := int64(-1)
	for reachable := n; reachable >= 0; reachable-- {
		got := BountyFor(base, k, n, reachable)
		if got < prev {
			t.Fatalf("bounty decreased as the stripe got rarer: reachable=%d gave %d < previous %d",
				reachable, got, prev)
		}
		prev = got
	}
}

// TestBountyFor_ClampAndDegenerate: past the k-floor the multiplier is already
// maxed (an unrecoverable-adjacent stripe cannot pay more), an over-full stripe
// pays the base, and degenerate parameters yield 0.
func TestBountyFor_ClampAndDegenerate(t *testing.T) {
	const base = 1_000
	const k, n = 4, 10
	maxBounty := int64(base * (n - k + 1)) // 7_000

	// Below the k-floor (data already at/near loss) stays clamped at the max.
	for _, reachable := range []int{4, 3, 0, -5} {
		if got := BountyFor(base, k, n, reachable); got != maxBounty {
			t.Fatalf("BountyFor at/below k-floor (reachable=%d) = %d, want clamp %d",
				reachable, got, maxBounty)
		}
	}
	// More reachable than n (shouldn't happen, but be defensive) pays the base.
	if got := BountyFor(base, k, n, n+3); got != base {
		t.Fatalf("over-full stripe bounty = %d, want base %d", got, base)
	}
	// Degenerate inputs yield 0 rather than a bogus payout.
	for _, c := range []struct {
		base int64
		k, n int
	}{
		{0, 4, 10},     // no base
		{-5, 4, 10},    // negative base
		{1_000, 0, 10}, // k<=0
		{1_000, 4, 3},  // n<k
	} {
		if got := BountyFor(c.base, c.k, c.n, c.k); got != 0 {
			t.Fatalf("BountyFor(base=%d,k=%d,n=%d) = %d, want 0", c.base, c.k, c.n, got)
		}
	}
}

// TestEscrow_ReadersOnUnknownObjectAreZero: reading an object that was never
// funded returns zeros, not a panic or a phantom reserve.
func TestEscrow_ReadersOnUnknownObjectAreZero(t *testing.T) {
	l := New(50_000, 0)
	root := objRoot("never-touched")
	if b, f, p := l.EscrowBalance(root), l.EscrowFunded(root), l.EscrowPaid(root); b|f|p != 0 {
		t.Fatalf("unknown object readers = (%d,%d,%d), want all 0", b, f, p)
	}
}

// TestEscrow_AutoSkimKeepsColdObjectSolventAcrossRepairs is the small end-to-end
// of the funding model: a publisher prepays a reserve, serving revenue auto-skims
// top it up, and repair bounties draw it down. The reserve tracks funded - paid
// throughout — the horizon accounting the g-instrument (slice 3) will read.
func TestEscrow_AutoSkimKeepsColdObjectSolventAcrossRepairs(t *testing.T) {
	l := New(50_000, 10_000_000)
	publisher, repairer := id(1), id(2)
	readers := []ports.NodeID{id(3), id(4), id(5)}
	root := objRoot("obj-A")

	// Prepay a durability reserve.
	if err := l.FundEscrow(root, publisher, 1_000_000); err != nil {
		t.Fatalf("fund: %v", err)
	}
	// Popular data: many fetches auto-skim into the reserve.
	var skimmed int64
	for _, r := range readers {
		skimmed += l.RecordServeToObject(publisher, r, root, id(9), 64<<10)
	}
	// Repairs draw it down over time (rarest-shard priced).
	paid := l.PayBounty(root, repairer, BountyFor(1_000, 4, 10, 7)) // a 3-lost stripe
	paid += l.PayBounty(root, repairer, BountyFor(1_000, 4, 10, 9)) // a 1-lost stripe

	wantBalance := 1_000_000 + skimmed - paid
	if got := l.EscrowBalance(root); got != wantBalance {
		t.Fatalf("escrow balance = %d, want %d (prepay + skim - paid)", got, wantBalance)
	}
	if got := l.EscrowFunded(root); got != 1_000_000+skimmed {
		t.Fatalf("funded = %d, want %d", got, 1_000_000+skimmed)
	}
	if got := l.EscrowPaid(root); got != paid {
		t.Fatalf("paid = %d, want %d", got, paid)
	}
	// The identity that funded, served, and repaired earns/loses NO standing.
	for _, who := range []ports.NodeID{publisher, repairer} {
		if got := l.Reputation(who); got != 0 {
			t.Fatalf("durability activity moved standing for %x: rep = %d", who[:2], got)
		}
	}
}
