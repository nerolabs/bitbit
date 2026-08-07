package credit

// The finite-but-renewable durability instruments (H7 slice 3, decision D-S7).
//
// silt does NOT promise perpetual cold-data solvency — that promise is the Arweave
// endowment identity in credits, and it holds only if the credit-denominated cost
// of storage keeps falling. 2020s hardware evidence (HDD $/TB plateauing) says it
// may not. So silt ships durability as an explicit finite-but-renewable contract:
// fund a horizon, let the serve auto-skim extend it, and re-endow before it runs
// out. These pure functions are how an operator or the observatory MEASURES where
// an object sits on that contract, from a ports.DurabilitySnapshot:
//
//   - CostPerRepair — the realised credits per shard-repair so far.
//   - Horizon       — how long the current reserve lasts at the observed burn.
//   - G             — instrument g: the annualized trend of cost-per-repair, the
//                     single number that decides perpetual-vs-finite. g stays
//                     MEASURED, never assumed: "perpetual" is a claim silt earns
//                     only while g > 0, never an architectural promise.
//
// They read only observable accounting — never Reputation — so measuring durability
// can't perturb standing.

import (
	"math"
	"math/big"

	"github.com/nerolabs/silt/ports"
)

// CostPerRepair is the mean credits one shard-repair has cost object so far
// (Paid / Repairs) — the numerator of instrument g. It is 0 when the object has
// funded no repairs yet (no realised cost to report).
func CostPerRepair(s ports.DurabilitySnapshot) int64 {
	if s.Repairs <= 0 {
		return 0
	}
	return s.Paid / s.Repairs
}

// Horizon projects how much longer the current reserve (Balance) will last at the
// burn rate the object has ACTUALLY shown — its lifetime Paid spread over the
// elapsed observation window. It returns (remaining, finite):
//
//   - finite == true  → a real, bounded horizon: the reserve runs dry in
//     `remaining` at the observed burn (re-endow before then).
//   - finite == false → no burn has been observed (Paid == 0) or the window is
//     non-positive, so the reserve is unbounded at the current (zero) rate. This
//     is NOT "perpetual achieved": it means the horizon is not yet measurable, and
//     the caller should treat perpetual as unproven. `remaining` is 0 and ignored.
//
// A depleted reserve (Balance <= 0) is a real, finite horizon of zero.
func Horizon(s ports.DurabilitySnapshot, elapsed ports.Duration) (ports.Duration, bool) {
	if s.Paid <= 0 || elapsed <= 0 {
		return 0, false // no observed burn → not yet measurable, not "perpetual"
	}
	if s.Balance <= 0 {
		return 0, true // already spent
	}
	// remaining = Balance / burnRate, burnRate = Paid / elapsed (credits per unit
	// time) ⇒ remaining = Balance * elapsed / Paid, in nanoseconds. The product
	// overflows int64 for a large reserve over a long window (e.g. 1000 credits ×
	// a decade in ns), so compute it in big.Int and clamp an astronomically-long
	// horizon to MaxInt64 — still "finite", just longer than the type can name.
	r := new(big.Int).Mul(big.NewInt(int64(elapsed)), big.NewInt(s.Balance))
	r.Quo(r, big.NewInt(s.Paid))
	if !r.IsInt64() {
		return ports.Duration(math.MaxInt64), true
	}
	return ports.Duration(r.Int64()), true
}

// G is instrument g: the annualized fractional change in cost-per-shard-repair
// between two snapshots of the SAME object, signed so g > 0 means the credit cost
// is DECLINING — the condition under which the endowment identity can hold and
// "perpetual" becomes earnable. dt is the wall time between old and new.
//
// g = ((costOld - costNew) / costOld) * (Year / dt)
//
// A positive g is a cost decline (solvency-favourable); a negative g is a cost
// RISE (the plateau/inflation regime that forces re-endowment). It returns 0 when
// it cannot be computed — dt <= 0, or either snapshot has funded no repairs, or
// the earlier cost was 0 — so an un-measurable trend reads as "flat / unknown",
// never a false signal.
func G(old, new ports.DurabilitySnapshot, dt ports.Duration) float64 {
	if dt <= 0 {
		return 0
	}
	costOld, costNew := CostPerRepair(old), CostPerRepair(new)
	if costOld <= 0 || new.Repairs <= 0 {
		return 0
	}
	frac := float64(costOld-costNew) / float64(costOld)
	return frac * float64(ports.Year) / float64(dt)
}
