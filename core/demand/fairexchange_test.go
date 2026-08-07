package demand

import (
	"testing"

	"github.com/nerolabs/silt/ports"
)

// TestAbortLeavesTokenReusable is the fetcher-side fair-exchange floor (P2): an
// aborted exchange never consumes the fetcher's token. The fetcher commits and the
// server then vanishes (delivers nothing, banks nothing) — so the serial is never
// spent, and the SAME token still redeems a real, correct delivery at another server.
// A non-delivering server cannot rob the fetcher of the paid token.
func TestAbortLeavesTokenReusable(t *testing.T) {
	s := newScene(t, "obj-C")
	tok := s.token(t)

	// Optimistic phase begins: the fetcher signs a pre-release commitment to serverA…
	serverA := ports.HashBytes([]byte("server-A"))
	c := Commit(s.fetcher, tok, s.object, serverA)
	if !VerifyCommitment(c) {
		t.Fatal("a well-formed pre-release commitment must verify")
	}
	// …then serverA aborts: no bytes delivered, nothing banked. The token was NOT
	// consumed (only a completed Redeem spends a serial).

	// The fetcher retries at serverB (s.server) and completes a genuine delivery. The
	// same token redeems — proving the abort did not burn it.
	r, err := Ack(s.fetcher, tok, s.object, s.server, s.data, s.tags)
	if err != nil {
		t.Fatalf("ack: %v", err)
	}
	if ok, reason := NewBank().Redeem(s.issuerPub, tok, r); !ok {
		t.Fatalf("an aborted exchange must leave the token reusable, but redeem failed: %s", reason)
	}
}

// TestPreReleaseCommitmentIsNotAReceipt is the server-side fair-exchange floor (P2): a
// fetcher's pre-release commitment cannot be turned into demand credit. A malicious
// server holding a valid ExchangeCommitment (the fetcher engaged) but WITHOUT a
// PoR-bound delivery proof cannot bank it — the commitment is domain-separated from
// the receipt signature and carries no possession proof, so lifting it onto a receipt
// fails verification. Only a completed delivery redeems (#receipts ≤ completed
// deliveries survives the abort path).
func TestPreReleaseCommitmentIsNotAReceipt(t *testing.T) {
	s := newScene(t, "obj-C")
	tok := s.token(t)
	c := Commit(s.fetcher, tok, s.object, s.server)
	if !VerifyCommitment(c) {
		t.Fatal("setup: commitment should verify")
	}

	// The server tries to pass the commitment off as a delivery receipt: it copies the
	// commitment's fields and its signature into a DeliveryReceipt (it has no bytes of
	// C, so it cannot produce a real PoR proof).
	forged := DeliveryReceipt{
		Serial:  append([]byte(nil), c.Serial...),
		Object:  c.Object,
		Server:  c.Server,
		Fetcher: append([]byte(nil), c.Fetcher...),
		Blocks:  0,
		Sig:     append([]byte(nil), c.Sig...), // a commitment sig, over the wrong domain
	}
	if ok, reason := NewBank().Redeem(s.issuerPub, tok, forged); ok {
		t.Fatalf("a pre-release commitment must not redeem as demand (got credited, reason=%q)", reason)
	}
}

// TestCommitmentDomainSeparation pins the crypto behind the server-side floor: a
// signature good as a commitment is NOT good as a receipt, and vice versa — the two
// share the (serial‖object‖server‖fetcher) binding but sit in distinct domains.
func TestCommitmentDomainSeparation(t *testing.T) {
	s := newScene(t, "obj-C")
	tok := s.token(t)
	good, err := Ack(s.fetcher, tok, s.object, s.server, s.data, s.tags)
	if err != nil {
		t.Fatalf("ack: %v", err)
	}
	// The real receipt's signature must NOT verify as a commitment over the same tuple.
	asCommit := ExchangeCommitment{Serial: good.Serial, Object: good.Object, Server: good.Server, Fetcher: good.Fetcher, Sig: good.Sig}
	if VerifyCommitment(asCommit) {
		t.Fatal("a delivery-receipt signature must not verify as an exchange commitment (domains not separated)")
	}
	// A tampered-key commitment must fail.
	c := Commit(s.fetcher, tok, s.object, s.server)
	c.Fetcher[0] ^= 0xFF
	if VerifyCommitment(c) {
		t.Fatal("a commitment whose fetcher key was altered must not verify")
	}
}

// TestOptimisticPathStillCredits: committing first does not disturb the happy path —
// after a pre-release commitment, a genuine delivery still redeems and credits demand
// exactly once.
func TestOptimisticPathStillCredits(t *testing.T) {
	s := newScene(t, "obj-C")
	tok := s.token(t)
	_ = Commit(s.fetcher, tok, s.object, s.server) // optimistic phase
	r, err := Ack(s.fetcher, tok, s.object, s.server, s.data, s.tags)
	if err != nil {
		t.Fatalf("ack: %v", err)
	}
	bank := NewBank()
	if ok, reason := bank.Redeem(s.issuerPub, tok, r); !ok {
		t.Fatalf("optimistic completion should credit: %s", reason)
	}
	if got := bank.Demand(s.object); got != 1 {
		t.Fatalf("demand = %d, want 1", got)
	}
}
