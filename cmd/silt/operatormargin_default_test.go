package main

import "testing"

// Red-team blind-2026-08-08 hardening (C2 split-defense) INVERTED as a regression:
// the C2 operator margin M must be SAFE-BY-DEFAULT (>1) for an untrusted (objective)
// validator. The mechanism shipped but defaulted M=1 (no split protection), so a
// stock/doc-following open swarm gave one operator splitting real stake across NodeIDs
// zero margin against faking the decentralization that sheds the launch anchors — the
// Invariant-B footgun ("safe config is the default").
func TestOperatorMarginDefaultsAboveOneForUntrustedValidator(t *testing.T) {
	// Stock untrusted-validator posture (no -operator-margin passed): M is derived >1,
	// not left at the no-margin default of 1.
	got, defaulted := effectiveOperatorMargin(false, 1, true)
	if !defaulted || got != DerivedOperatorMargin {
		t.Fatalf("split-defense regression: an untrusted validator that sets no margin must GET one: got %d (defaulted=%v), want %d", got, defaulted, DerivedOperatorMargin)
	}
	if DerivedOperatorMargin < 2 {
		t.Fatalf("the derived operator margin must exceed 1 to give any split protection: got %d", DerivedOperatorMargin)
	}

	// An EXPLICIT choice always wins — including 1, the trusted/single-operator opt-out.
	if got, defaulted := effectiveOperatorMargin(true, 1, true); got != 1 || defaulted {
		t.Fatalf("an explicit -operator-margin 1 must opt out of the margin: got %d (defaulted=%v)", got, defaulted)
	}
	if got, _ := effectiveOperatorMargin(true, 5, true); got != 5 {
		t.Fatalf("an explicit margin must be honored verbatim: got %d", got)
	}

	// A TRUSTED / non-objective path is unaffected — demos and single-box deployments
	// keep M=1 (no anchors, so the shed metric is moot there anyway).
	if got, defaulted := effectiveOperatorMargin(false, 1, false); got != 1 || defaulted {
		t.Fatalf("a trusted/non-objective node must keep M=1: got %d (defaulted=%v)", got, defaulted)
	}

	// An operator who explicitly asks for MORE than the derived default keeps it
	// (the derivation only ever raises the bar, never lowers an explicit choice).
	if got, defaulted := effectiveOperatorMargin(false, DerivedOperatorMargin+3, true); got != DerivedOperatorMargin+3 || defaulted {
		t.Fatalf("a higher unset-but-larger explicit value must be preserved: got %d (defaulted=%v)", got, defaulted)
	}
}
