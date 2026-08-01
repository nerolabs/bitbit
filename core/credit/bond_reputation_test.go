package credit

import "testing"

// A passed bond challenge is the ONLY large input to standing; a fresh
// identity that has proven held storage clears the chain's bar, and one
// that only wash-serves does not.
func TestBondChallengeIsTheStandingInput(t *testing.T) {
	l := New(50_000, 0)
	honest, sybilA, sybilB := id(1), id(2), id(3)

	// Honest node proves a 64 MiB identity-bound bond.
	l.RecordBondChallenge(honest, 64<<20, true, 1)
	if got, want := l.Reputation(honest), int64(64<<20)/(64<<10); got != want {
		t.Fatalf("bonded reputation %d, want %d", got, want)
	}

	// Two Sybils wash-serve each other a terabyte each way: balances move,
	// standing does not.
	l.RecordServe(sybilA, sybilB, id(9), 1<<40)
	l.RecordServe(sybilB, sybilA, id(9), 1<<40)
	if l.Reputation(sybilA) != 0 || l.Reputation(sybilB) != 0 {
		t.Fatal("wash-serving created standing — the whole hole this closes")
	}
	if l.Balance(sybilA) == 0 {
		t.Fatal("expected wash-serving to still move balances (observability economy)")
	}
}

// A failed bond challenge zeroes standing and bites: a node that cannot
// answer for the bond it committed to loses its footing.
func TestFailedBondChallengeZeroesAndPenalizes(t *testing.T) {
	l := New(50_000, 0)
	n := id(1)
	l.RecordBondChallenge(n, 64<<20, true, 1)
	if l.Reputation(n) <= 0 {
		t.Fatal("setup: bonded node should have positive standing")
	}
	l.RecordBondChallenge(n, 64<<20, false, 2)
	if got := l.Reputation(n); got >= 0 {
		t.Fatalf("failed bond should zero standing and apply a penalty, got %d", got)
	}
}

// DecayStale retires standing that stops being re-proven, so standing is
// an integral over *sustained* proof — you cannot buy last month's uptime.
func TestDecayStaleRetiresUnrefreshedStanding(t *testing.T) {
	l := New(50_000, 0)
	stale, fresh := id(1), id(2)
	l.RecordBondChallenge(stale, 64<<20, true, 1) // last proven at tick 1
	l.RecordBondChallenge(fresh, 64<<20, true, 4) // last proven at tick 4

	l.DecayStale(5, 2) // now=5, maxAge=2: anything last proven before tick 3 is stale
	if l.Reputation(stale) != 0 {
		t.Fatal("standing older than maxAge should have decayed to zero")
	}
	if l.Reputation(fresh) <= 0 {
		t.Fatal("recently-proven standing should survive decay")
	}
}
