package credit

import (
	"testing"

	"github.com/nerolabs/silt/ports"
)

// TestCostPerRepair: realised credits per shard-repair = Paid/Repairs, and 0 when
// nothing has been repaired yet.
func TestCostPerRepair(t *testing.T) {
	if got := CostPerRepair(ports.DurabilitySnapshot{Paid: 700, Repairs: 7}); got != 100 {
		t.Fatalf("cost/repair = %d, want 100", got)
	}
	if got := CostPerRepair(ports.DurabilitySnapshot{Paid: 0, Repairs: 0}); got != 0 {
		t.Fatalf("cost/repair with no repairs = %d, want 0", got)
	}
}

// TestHorizon: the reserve's remaining life at the observed burn, and the
// not-yet-measurable cases that must NOT read as "perpetual achieved".
func TestHorizon(t *testing.T) {
	// Balance 1000, burned 100 credits over 10 years → 10 credits/yr → 100 yr left.
	rem, finite := Horizon(ports.DurabilitySnapshot{Balance: 1000, Paid: 100}, 10*ports.Year)
	if !finite {
		t.Fatal("a reserve with observed burn must have a finite horizon")
	}
	if want := 100 * ports.Year; rem != want {
		t.Fatalf("horizon = %d, want %d (100 years)", rem, want)
	}

	// No burn observed yet → not measurable (finite=false), NOT perpetual.
	if _, finite := Horizon(ports.DurabilitySnapshot{Balance: 1000, Paid: 0}, ports.Year); finite {
		t.Fatal("no observed burn must report finite=false (unproven), not a real horizon")
	}
	// Non-positive window → not measurable.
	if _, finite := Horizon(ports.DurabilitySnapshot{Balance: 1000, Paid: 100}, 0); finite {
		t.Fatal("a non-positive window must report finite=false")
	}
	// Depleted reserve → a real, finite horizon of zero.
	rem, finite = Horizon(ports.DurabilitySnapshot{Balance: 0, Paid: 100}, ports.Year)
	if !finite || rem != 0 {
		t.Fatalf("a spent reserve = (%d, %v), want (0, true)", rem, finite)
	}
}

// TestG: instrument g is the annualized fractional change of cost-per-repair,
// POSITIVE when cost is declining (solvency-favourable), negative when it rises.
func TestG(t *testing.T) {
	// cost 200 → 100 over exactly one year → a 50% annual decline → g = +0.5.
	old := ports.DurabilitySnapshot{Paid: 200, Repairs: 1}   // cost/repair 200
	newer := ports.DurabilitySnapshot{Paid: 300, Repairs: 3} // cost/repair 100
	if g := G(old, newer, ports.Year); g < 0.49 || g > 0.51 {
		t.Fatalf("g = %v, want ~+0.5 (a 50%%/yr cost decline)", g)
	}

	// Cost RISING (100 → 200) → g negative (the plateau/inflation regime).
	rising := G(ports.DurabilitySnapshot{Paid: 100, Repairs: 1}, ports.DurabilitySnapshot{Paid: 200, Repairs: 1}, ports.Year)
	if rising >= 0 {
		t.Fatalf("a rising cost must give g < 0, got %v", rising)
	}

	// Same window halved → the annualization doubles the rate.
	half := G(old, newer, ports.Year/2)
	if half < 0.99 || half > 1.01 {
		t.Fatalf("g over half a year = %v, want ~+1.0 (annualized)", half)
	}

	// Un-measurable trends read as flat, never a false signal.
	if g := G(old, newer, 0); g != 0 {
		t.Fatalf("g with dt=0 = %v, want 0", g)
	}
	if g := G(ports.DurabilitySnapshot{}, newer, ports.Year); g != 0 {
		t.Fatalf("g with no prior repairs = %v, want 0", g)
	}
}

// TestDurabilitySnapshotTracksRepairs: the ledger's snapshot exposes the live
// accounting — funded, paid, and the repair COUNT that PayBounty increments — so
// cost-per-repair is computable from real ledger state.
func TestDurabilitySnapshotTracksRepairs(t *testing.T) {
	l := New(50_000, 1_000_000)
	root := objRoot("obj-g")
	if err := l.FundEscrow(root, id(1), 10_000); err != nil {
		t.Fatalf("fund: %v", err)
	}
	l.PayBounty(root, id(2), 300)
	l.PayBounty(root, id(3), 300)

	snap := l.DurabilitySnapshot(root)
	if snap.Funded != 10_000 || snap.Paid != 600 || snap.Repairs != 2 {
		t.Fatalf("snapshot = %+v, want funded 10000, paid 600, repairs 2", snap)
	}
	if snap.Balance != 10_000-600 {
		t.Fatalf("reserve = %d, want 9400", snap.Balance)
	}
	if got := CostPerRepair(snap); got != 300 {
		t.Fatalf("cost/repair from ledger snapshot = %d, want 300", got)
	}
	if got := l.EscrowRepairs(root); got != 2 {
		t.Fatalf("EscrowRepairs = %d, want 2", got)
	}
	// A partial payment when the reserve is nearly dry still counts as one repair.
	l.PayBounty(root, id(4), 1_000_000) // more than remains → pays the rest
	snap = l.DurabilitySnapshot(root)
	if snap.Balance != 0 || snap.Repairs != 3 {
		t.Fatalf("after draining: balance %d repairs %d, want 0 and 3", snap.Balance, snap.Repairs)
	}
}
