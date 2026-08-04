package chain

// RED-TEAM REGRESSION — accountability corner (censor persona), red-team F5.
//
// The external red-team broke accountability three ways through the on-chain
// revocation path (redteam/M0-REDTEAM-REPORT.md Finding 5):
//   (A) a quorum could revoke a root it never published / that never existed —
//       ValidateProposal did no ownership or existence check;
//   (B) the revocation was GLOBAL and non-voluntary — the canonical registry
//       seam hid it from every chain-follower with no per-operator opt-in;
//   (C) it was IRREVERSIBLE — revoked was set-only, no un-revoke record.
//
// These tests are the original PoC's teeth, INVERTED to assert the fix: each
// break is now denied. (Part B/C at the registry+chain layer are also covered
// by TestRevocationByQuorum; here we keep the adversary's own framing so a
// regression is obvious.)

import (
	"context"
	"errors"
	"testing"

	"github.com/nerolabs/silt/ports"
)

// (A) DENIED: a takedown that names a root never committed on this chain is
// refused — by the attester pre-check AND the commit path. A quorum cannot
// censor content that isn't on the ledger, nor a competitor's unpublished hash.
func TestF5_RevokeUnknownRoot_Denied(t *testing.T) {
	w := newWorld(DefaultConfig())
	pub := w.block(entry(42))
	w.attestAll(pub)
	if err := w.c.Append(*pub); err != nil {
		t.Fatalf("victim publish failed: %v", err)
	}

	neverPublished := ports.HashBytes([]byte("content that never existed on chain"))
	prev, height := w.c.Head()

	// A block mixing the victim's real root with a never-published one: the
	// whole block must be refused because ONE named root was never committed.
	rev := &Block{Version: BlockVersion, Height: height, Prev: prev,
		Revocations: []ports.Hash{entry(42).Root, neverPublished}}
	Sign(rev, w.prop)
	w.attestAll(rev)

	if err := w.c.ValidateProposal(rev); !errors.Is(err, ErrRevokeUnknownRoot) {
		t.Fatalf("ValidateProposal must reject an unknown-root revocation with ErrRevokeUnknownRoot, got %v", err)
	}
	if err := w.c.Append(*rev); !errors.Is(err, ErrRevokeUnknownRoot) {
		t.Fatalf("Append must reject an unknown-root revocation, got %v", err)
	}
	if w.c.Revoked(entry(42).Root) {
		t.Fatal("no revocation should have committed (whole block refused)")
	}

	// The legitimate case still works: revoking a genuinely-published root.
	prev, height = w.c.Head()
	ok := &Block{Version: BlockVersion, Height: height, Prev: prev, Revocations: []ports.Hash{entry(42).Root}}
	Sign(ok, w.prop)
	w.attestAll(ok)
	if err := w.c.Append(*ok); err != nil {
		t.Fatalf("revoking a real published root must still work: %v", err)
	}
	if !w.c.Revoked(entry(42).Root) {
		t.Fatal("published root should be revoked after a valid takedown")
	}
}

// (B) DENIED: revocation is per-operator, not a global switch. Two readers of
// the SAME chain reach opposite resolutions purely by their own subscription
// choice — "proportional to who trusts you," not universal.
func TestF5_RevocationIsPerOperator_Denied(t *testing.T) {
	w := newWorld(DefaultConfig())
	pub := w.block(entry(7))
	w.attestAll(pub)
	if err := w.c.Append(*pub); err != nil {
		t.Fatal(err)
	}
	prev, height := w.c.Head()
	rev := &Block{Version: BlockVersion, Height: height, Prev: prev, Revocations: []ports.Hash{entry(7).Root}}
	Sign(rev, w.prop)
	w.attestAll(rev)
	if err := w.c.Append(*rev); err != nil {
		t.Fatal(err)
	}

	subscriber := ReplicaRegistry{C: w.c, HonorRevocations: true}
	abstainer := ReplicaRegistry{C: w.c} // did not subscribe

	if _, ok, _ := subscriber.Lookup(context.Background(), entry(7).Root); ok {
		t.Fatal("a subscribing reader should honor the takedown")
	}
	if _, ok, _ := abstainer.Lookup(context.Background(), entry(7).Root); !ok {
		t.Fatal("DENIED-check failed: a non-subscribing reader of the same chain must " +
			"still resolve the root — no global switch")
	}
}
