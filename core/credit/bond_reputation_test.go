package credit

import "testing"

// attesterBar mirrors chain.DefaultConfig().MinAttesterRep — the standing a
// node needs to help commit a block. These tests assert the OUTCOME (can a
// node clear the bar and vote, or not), never the exact reputation number,
// so retuning the disk↔standing curve doesn't rot them.
const attesterBar = 100

// USE CASE: a node's standing must be BOUGHT WITH STORAGE, not chatter. A
// node that proves an identity-bound bond clears the bar and can attest; a
// Sybil ring that only wash-serves each other cannot — no matter how much it
// "serves". OUTCOME asserted: clears / does not clear the attester bar.
func TestOnlyProvenStorageClearsTheAttesterBar(t *testing.T) {
	l := New(50_000, 0)
	honest, sybilA, sybilB := id(1), id(2), id(3)

	l.RecordBondChallenge(honest, honest, 64<<20, true, 1)
	if l.Reputation(honest) < attesterBar {
		t.Fatal("a node that proved real held storage must clear the attester bar")
	}

	// Terabytes of wash-serving between the ring: balances move, standing
	// does not — so neither sybil can vote.
	l.RecordServe(sybilA, sybilB, id(9), 1<<40)
	l.RecordServe(sybilB, sybilA, id(9), 1<<40)
	if l.Reputation(sybilA) >= attesterBar || l.Reputation(sybilB) >= attesterBar {
		t.Fatal("wash-serving must not buy enough standing to attest")
	}
	if l.Balance(sybilA) == 0 {
		t.Fatal("wash-serving should still move balances (the observability economy is intact)")
	}
}

// USE CASE: a colluding operator must not amortise ONE plot across many
// identities. Standing is bound to the bond ROOT, and a root credits at most
// one identity — so N identities pointing at a single shared plot earn ONE
// bond's standing, not N. This closes the plot-amortisation gap (design §6):
// combined with per-identity-secret plots (core/bond), N identities need N
// distinct plots = N×disk. OUTCOME asserted: only the first identity to prove
// a shared root clears the attester bar; the rest earn nothing, while a
// distinct plot earns standing normally.
func TestSharedPlotEarnsStandingForOnlyOneIdentity(t *testing.T) {
	l := New(50_000, 0)
	shared := id(200) // one plot's root, pointed at by colluding identities
	a, b, c := id(1), id(2), id(3)

	l.RecordBondChallenge(a, shared, 64<<20, true, 1)
	l.RecordBondChallenge(b, shared, 64<<20, true, 1)
	l.RecordBondChallenge(c, shared, 64<<20, true, 1)

	if l.Reputation(a) < attesterBar {
		t.Fatal("the first identity to prove the plot should earn its standing")
	}
	if l.Reputation(b) >= attesterBar || l.Reputation(c) >= attesterBar {
		t.Fatal("identities sharing one plot must earn NO standing — amortisation is closed")
	}

	// An identity holding its OWN distinct plot (a different root) earns
	// standing normally — the dedup only bites a SHARED root.
	l.RecordBondChallenge(b, id(201), 64<<20, true, 1)
	if l.Reputation(b) < attesterBar {
		t.Fatal("an identity with its own distinct plot must earn standing")
	}
}

// USE CASE: a PROVEN consensus double-sign is the gravest offense and costs
// everything — an equivocator is buried below any threshold, barred from
// proposing or attesting, no matter how much standing it had earned. OUTCOME
// asserted: strong standing → slashed → deeply negative, well under the bar.
func TestEquivocationBuriesStanding(t *testing.T) {
	l := New(50_000, 0)
	v := id(1)
	l.RecordBondChallenge(v, v, 64<<20, true, 1)
	for a := 0; a < 40; a++ {
		l.RecordAudit(v, id(byte(a)), true) // pile on earned standing
	}
	if l.Reputation(v) < attesterBar {
		t.Fatal("setup: a bonded, audited validator should clear the bar")
	}
	l.SlashEquivocation(v)
	if l.Reputation(v) >= 0 {
		t.Fatal("a proven double-signer must be buried below zero — barred from consensus for good")
	}
}

// USE CASE: a validator that can no longer answer for the bond it committed
// to should lose its seat. OUTCOME asserted: after a failed challenge it no
// longer clears the attester bar.
func TestAFailedBondLosesTheAbilityToAttest(t *testing.T) {
	l := New(50_000, 0)
	n := id(1)
	l.RecordBondChallenge(n, n, 64<<20, true, 1)
	if l.Reputation(n) < attesterBar {
		t.Fatal("setup: bonded node should clear the bar")
	}
	l.RecordBondChallenge(n, n, 64<<20, false, 2)
	if l.Reputation(n) >= attesterBar {
		t.Fatal("a node that failed its bond challenge must lose its vote")
	}
}

// USE CASE: standing must be SUSTAINED — you cannot bond once and coast. A
// validator that stops re-proving loses its seat while one still proving
// keeps it. OUTCOME asserted: stale drops below the bar, fresh stays above.
func TestStandingMustBeSustainedToKeepVoting(t *testing.T) {
	l := New(50_000, 0)
	stale, fresh := id(1), id(2)
	l.RecordBondChallenge(stale, stale, 64<<20, true, 1) // last proven at tick 1
	l.RecordBondChallenge(fresh, fresh, 64<<20, true, 4) // last proven at tick 4

	l.DecayStale(5, 2) // now=5, maxAge=2: anything last proven before tick 3 is stale
	if l.Reputation(stale) >= attesterBar {
		t.Fatal("a validator that stopped re-proving must fall below the bar")
	}
	if l.Reputation(fresh) < attesterBar {
		t.Fatal("a validator still re-proving must keep its seat")
	}
}
