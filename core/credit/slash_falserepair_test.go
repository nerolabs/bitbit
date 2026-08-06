package credit

import "testing"

// TestSlashFalseRepair_BitesHardButFinite: a proven false repair claim docks a
// bonded caretaker's standing hard — below the attester bar — but the penalty is
// FINITE (unlike an equivocation's permanent burial), it accumulates per incident,
// and it does not disturb the underlying bond, so a node whose penalty ages out of
// the standing calculus is not permanently barred.
func TestSlashFalseRepair_BitesHardButFinite(t *testing.T) {
	l := New(50_000, 0)
	n := id(1)

	// A well-bonded caretaker, comfortably an attester.
	const bond = 128 << 20 // 128 MiB
	l.RecordBondChallenge(n, id(1), bond, true, 1)
	base := l.Reputation(n)
	if base < attesterBar {
		t.Fatalf("setup: bonded node should clear the bar, got %d", base)
	}

	// One proven false repair drops it below the bar.
	l.SlashFalseRepair(n)
	after := l.Reputation(n)
	if after >= attesterBar {
		t.Fatalf("a proven false repair must drop standing below the attester bar, got %d", after)
	}
	if want := base - falseRepairSlash; after != want {
		t.Fatalf("standing after slash = %d, want base-penalty = %d (finite, not permanent)", after, want)
	}

	// It accumulates per incident (repeatable, not a one-shot flag).
	l.SlashFalseRepair(n)
	if want := base - 2*falseRepairSlash; l.Reputation(n) != want {
		t.Fatalf("two false repairs = %d, want %d", l.Reputation(n), want)
	}

	// It is NOT the permanent equivocation burial: re-proving the bond restores the
	// bonded term; only the finite false-repair dent remains subtracted.
	l.RecordBondChallenge(n, id(1), bond, true, 2)
	if want := base - 2*falseRepairSlash; l.Reputation(n) != want {
		t.Fatalf("re-proving the bond after false repairs = %d, want %d (bond intact, dent finite)", l.Reputation(n), want)
	}
}

// TestSlashFalseRepair_CannotRaiseBondlessIdentity: like every slash it is a
// reduces-class press — firing it on a bondless identity can only ever leave
// standing at or below zero (the Invariant-A guard asserts this structurally too).
func TestSlashFalseRepair_CannotRaiseBondlessIdentity(t *testing.T) {
	l := New(50_000, 0)
	n := id(2)
	for i := 0; i < 3; i++ {
		l.SlashFalseRepair(n)
		if got := l.Reputation(n); got > 0 {
			t.Fatalf("SlashFalseRepair lifted a bondless identity to %d > 0", got)
		}
	}
}
