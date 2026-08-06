package credit

import (
	"reflect"
	"testing"

	"github.com/nerolabs/silt/ports"
)

// M0 hardening H3 — the Invariant-A structural guardrail.
//
// Invariant A (docs/design/m0-hardening-strategy.md §2): no standing without a
// verified, identity-bound, unpredictable-challenge, deduped, bond-gated proof.
//
// The strategy doc's meta-pattern #1 is "we fix instances, not classes": F1 →
// G2 → RT-1 were three re-instances of one class (a standing press that skips
// identity-binding), and we only ever hardened the surface a red team pointed
// at. This file is the guardrail that turns the class into a compile-and-test
// obligation: it ENUMERATES every press that can mint standing and asserts each
// satisfies Invariant A, and it fails loudly when a new Ledger method ships
// unclassified — so a fourth instance cannot slip in unaudited.
//
// Why enumerating *credit.Ledger is complete for the MINT side: all standing
// state lives in credit.account (bondedBytes/auditsFailed/bondFails/
// equivocations), Reputation is a pure function of those fields, and
// credit.Ledger is the sole ports.CreditLedger implementation (sim wraps it).
// So every way to move standing is a method on *Ledger — enumerate them and the
// mint surface is closed. The consumption side (who READS Reputation to gate
// consensus) is surface S4, guarded elsewhere.

// standingClass is how a Ledger method may move consensus standing (Reputation).
type standingClass int

const (
	mints   standingClass = iota // can RAISE standing — must satisfy Invariant A
	reduces                      // can only LOWER standing (integrity teeth; never Sybil-amplifiable)
	neutral                      // cannot move standing at all (balance/observability only)
)

// standingClassification is the audited class of every exported *Ledger method.
// Adding a method to Ledger fails TestInvariantA_EveryLedgerMethodClassified
// until you classify it here — and if you classify a new one as `mints`, you
// must extend the registry test below to prove it is identity-bound, deduped,
// bond-gated, and unpredictable-challenge. That is the structural obstacle to
// shipping a fourth "identity binding asserted but never verified" instance.
var standingClassification = map[string]standingClass{
	// The one and only standing-GRANTING press. Everything Invariant A demands
	// is asserted in TestInvariantA_TheOnlyMintingPressIsBondGated.
	"RecordBondChallenge": mints,

	// Integrity teeth — can only ever SUBTRACT standing, so they can never be
	// Sybil-amplified (piling them on hurts you, it cannot lift a bondless id).
	"RecordAudit":       reduces, // a FAILED PoR audit subtracts (a passed one funds balance only — RT-1/H1)
	"SlashEquivocation": reduces, // proven double-sign buries standing
	"DecayStale":        reduces, // un-refreshed bond standing retires to zero

	// Standing-neutral: balance economy, publishing, and observability. None
	// touch bondedBytes, so none can move Reputation upward.
	"Register":      neutral,
	"RecordServe":   neutral, // self-reported serving funds BALANCE, never standing
	"ChargePublish": neutral,
	"CanPublish":    neutral,
	"Balance":       neutral,
	"Balances":      neutral,
	"ServedBytes":   neutral,
	"FetchedBytes":  neutral,
	"Audits":        neutral,
	"Fee":           neutral,
	"Gini":          neutral,
	"Reputation":    neutral, // the reader itself

	// H7/S7 durability escrow (escrow.go). The durability budget is part of the
	// BALANCE economy — it moves the credit unit between balances and object
	// reserves — and confers ZERO consensus standing. That firewall is THE
	// load-bearing H7 invariant (a durability credit that bought standing would
	// re-open the shared-content γ→1/N hole), so every escrow press is neutral,
	// and TestInvariantA_NoNonMintPressRaisesStanding fires each one against a
	// bondless identity to prove it stays there.
	"FundEscrow":          neutral, // moves credits balance→reserve; no bond touched
	"RecordServeToObject": neutral, // object-aware serve + auto-skim; funds balance, never standing
	"PayBounty":           neutral, // pays repairer's BALANCE from a reserve; not standing
	"EscrowBalance":       neutral, // observability
	"EscrowFunded":        neutral, // observability
	"EscrowPaid":          neutral, // observability
}

// TestInvariantA_EveryLedgerMethodClassified is the reflection guard: every
// exported method on *Ledger must appear in standingClassification. When you add
// a method and forget to classify it, this fails with the method name — forcing
// the Invariant-A question ("can this mint standing?") to be answered in review,
// not discovered by a red team.
func TestInvariantA_EveryLedgerMethodClassified(t *testing.T) {
	typ := reflect.TypeOf(&Ledger{})
	for i := 0; i < typ.NumMethod(); i++ {
		name := typ.Method(i).Name
		if _, ok := standingClassification[name]; !ok {
			t.Fatalf("unclassified *Ledger method %q: it is a new standing press until proven otherwise. "+
				"Classify it in standingClassification (mints/reduces/neutral); if it can RAISE Reputation, "+
				"it MUST satisfy Invariant A — add its identity-bound/deduped/bond-gated assertions to "+
				"TestInvariantA_TheOnlyMintingPressIsBondGated. See docs/design/m0-hardening-strategy.md §2.", name)
		}
	}
	// And the reverse: keep the map honest — no stale entries for deleted methods.
	methods := make(map[string]bool, typ.NumMethod())
	for i := 0; i < typ.NumMethod(); i++ {
		methods[typ.Method(i).Name] = true
	}
	for name := range standingClassification {
		if !methods[name] {
			t.Errorf("standingClassification lists %q but *Ledger has no such method — remove the stale entry", name)
		}
	}
}

// TestInvariantA_NoNonMintPressRaisesStanding is the behavioral half of the
// guard: reflection catches a new method NAME, this catches a MISCLASSIFICATION.
// It drives every `neutral`/`reduces` press against a bondless identity — alone
// and in combination — and asserts standing never rises above zero. So if a
// method secretly mints standing but is classified non-mint, the invariant trips
// here regardless of what the map claims.
func TestInvariantA_NoNonMintPressRaisesStanding(t *testing.T) {
	l := New(50_000, 0)
	n := id(1)
	other := id(2)

	obj := id(7) // used as an object root for the escrow presses

	// Fire every non-mint press that mutates state, repeatedly and interleaved —
	// including the H7 durability-escrow presses. A durability credit that could
	// buy standing would re-open the shared-content γ→1/N hole, so funding a
	// reserve, skimming serve revenue into it, and paying a bounty out of it must
	// all leave a bondless identity at zero standing.
	for round := 0; round < 5; round++ {
		l.Register(n)
		l.RecordServe(n, other, id(9), 1<<40)              // terabytes of self-reported serving
		l.RecordAudit(n, id(9), true)                      // passed PoR audits fund balance only
		l.RecordServeToObject(n, other, obj, id(9), 1<<40) // object-aware serve + auto-skim
		_ = l.FundEscrow(obj, n, 1<<20)                    // prepay a durability reserve
		l.PayBounty(obj, n, 1<<30)                         // drain the reserve to this identity
		l.DecayStale(uint64(round+1), 1)
		if got := l.Reputation(n); got > 0 {
			t.Fatalf("Invariant A violated: a non-mint press lifted a BONDLESS identity to standing %d > 0 "+
				"(round %d) — either a press is misclassified in standingClassification or a new mint leaked in", got, round)
		}
	}
	// reduces-class presses can only push further down.
	l.RecordAudit(n, id(9), false)
	l.SlashEquivocation(n)
	if got := l.Reputation(n); got > 0 {
		t.Fatalf("Invariant A violated: reduce-class presses produced positive standing %d", got)
	}
}

// TestInvariantA_TheOnlyMintingPressIsBondGated proves the sole `mints` press —
// RecordBondChallenge — satisfies every clause of Invariant A. If a future press
// is classified `mints`, extend this test to cover it too.
func TestInvariantA_TheOnlyMintingPressIsBondGated(t *testing.T) {
	const size = 64 << 20 // 64 MiB — comfortably over the attester bar

	// (1) bond-gated + verified: a PASSED challenge grants standing proportional
	// to proven bytes; a FAILED one grants nothing (a bond you cannot answer
	// buys zero — the "verified" clause).
	t.Run("bond_gated", func(t *testing.T) {
		l := New(50_000, 0)
		passer, failer := id(10), id(11)
		l.RecordBondChallenge(passer, id(10), size, true, 1)
		l.RecordBondChallenge(failer, id(11), size, false, 1)
		if l.Reputation(passer) < attesterBar {
			t.Fatal("a proven bond must grant standing")
		}
		if l.Reputation(failer) > 0 {
			t.Fatal("a FAILED bond challenge must grant zero standing — verification is not optional")
		}
	})

	// (2) identity-bound: a proof settled for A must not lift B. (The deep
	// binding — a relayed proof for A failing B's verify — lives at the challenge
	// layer, core/bond + core/node/por.go porProverSeed; here we assert the
	// ledger credits only the prover it was told.)
	t.Run("identity_bound", func(t *testing.T) {
		l := New(50_000, 0)
		a, b := id(20), id(21)
		l.RecordBondChallenge(a, id(20), size, true, 1)
		if l.Reputation(b) > 0 {
			t.Fatal("settling A's bond must not grant B any standing")
		}
	})

	// (3) deduped: one bond root credits AT MOST ONE identity, so a colluding
	// operator cannot amortise one plot across N Sybils (rootOwner / F1).
	t.Run("deduped_per_root", func(t *testing.T) {
		l := New(50_000, 0)
		first, second := id(30), id(31)
		shared := ports.HashBytes([]byte("one-shared-plot"))
		l.RecordBondChallenge(first, shared, size, true, 1)
		l.RecordBondChallenge(second, shared, size, true, 2)
		if l.Reputation(first) < attesterBar {
			t.Fatal("the first identity to prove a root owns it and earns standing")
		}
		if l.Reputation(second) > 0 {
			t.Fatal("a second identity re-advertising the SAME root must earn zero — one plot, one standing")
		}
	})

	// (4) unpredictable-challenge / continuously re-proven: standing is an
	// integral over SUSTAINED proof — a single pass decays away if not refreshed,
	// so last month's uptime cannot be bought. (The challenge's unpredictability
	// itself is enforced in core/bond + por.go; here we assert the decay teeth
	// the ledger contributes.)
	t.Run("must_be_re_proven", func(t *testing.T) {
		l := New(50_000, 0)
		n := id(40)
		l.RecordBondChallenge(n, id(40), size, true, 1)
		if l.Reputation(n) < attesterBar {
			t.Fatal("fresh proof clears the bar")
		}
		l.DecayStale(100, 10) // now=100, maxAge=10 → a tick-1 proof is stale
		if l.Reputation(n) >= attesterBar {
			t.Fatal("standing not re-proven within maxAge must decay below the bar")
		}
	})
}
