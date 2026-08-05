package credit

import "testing"

// M0 hardening H1 / red-team RT-1 INVERTED as a regression. The blind red team
// broke the Sybil corner (over our own G2 merge) through the PoR-audit standing
// press: a data-less identity RELAYS an honest holder's aggregated proof, passes
// the audit, and — under the old formula `+ auditsPassed*25` — reached
// propose/attest eligibility (100 rep) with ZERO storage. The fix: PoR audits no
// longer mint Sybil-resistant standing; standing rests on the bond alone.
// See docs/design/m0-hardening-strategy.md §2 (Invariant A), §4 (S2), memo 03.

// RT-1: piling on PoR audit passes must NOT lift a bondless identity to standing.
// This is the exact "4 passes = 100 rep = eligibility with zero disk" claim,
// denied: with no bond, no number of audit passes clears the bar.
func TestRedteamRT1_AuditPassesGrantNoStandingWithoutBond(t *testing.T) {
	l := New(50_000, 0)
	liar := id(1)
	// A relay farm's identity "passes" many audits (it relayed a real holder's
	// proof). Before H1 this was 40*25 = 1000 rep, way over the 100 bar.
	for a := 0; a < 40; a++ {
		l.RecordAudit(liar, id(byte(a)), true)
	}
	if got := l.Reputation(liar); got >= attesterBar {
		t.Fatalf("RT-1 regression: %d audit passes lifted a BONDLESS identity to %d ≥ %d — a disk-less Sybil earns consensus standing", 40, got, attesterBar)
	}
	if got := l.Reputation(liar); got > 0 {
		t.Fatalf("RT-1: PoR audits must grant ZERO standing without a bond, got %d", got)
	}
}

// Invariant A (docs/design/m0-hardening-strategy.md §2): no POSITIVE standing
// without a verified, identity-bound, bond-gated proof. Property form: for ANY
// identity holding no bond, Reputation ≤ 0 regardless of how many audits it
// passed — so the only press that GRANTS standing is the bond. A future press
// that mints standing without a bond would trip this.
func TestInvariantA_NoPositiveStandingWithoutBond(t *testing.T) {
	l := New(50_000, 0)
	for i, passes := range []int{0, 1, 4, 100, 10_000} {
		n := id(byte(100 + i))
		for a := 0; a < passes; a++ {
			l.RecordAudit(n, id(byte(a)), true)
		}
		if got := l.Reputation(n); got > 0 {
			t.Fatalf("Invariant A violated: bondless identity with %d audit passes has standing %d > 0 — some press mints standing without a bond", passes, got)
		}
	}
	// Positive control: the bond press DOES grant standing (so the invariant is
	// "no standing without a bond", not "no standing ever").
	bonded := id(200)
	l.RecordBondChallenge(bonded, id(200), 64<<20, true, 1)
	if l.Reputation(bonded) < attesterBar {
		t.Fatal("the bond press must still grant standing — 64 MiB should clear the bar")
	}
}

// A failed PoR audit is still a NEGATIVE integrity signal (a prover that claimed
// shards it cannot answer for loses standing) — that term can never be Sybil-
// amplified since it only subtracts. Confirms we kept the teeth while removing
// the mint.
func TestRedteamRT1_FailedAuditStillPenalizes(t *testing.T) {
	l := New(50_000, 0)
	n := id(1)
	l.RecordBondChallenge(n, id(1), 64<<20, true, 1)
	before := l.Reputation(n)
	l.RecordAudit(n, id(9), false) // claimed a shard, could not answer
	if l.Reputation(n) >= before {
		t.Fatal("a failed audit must still subtract standing — the integrity teeth stay")
	}
}
