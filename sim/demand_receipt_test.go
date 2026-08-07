package sim

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"testing"

	"github.com/nerolabs/silt/adapters/simnet"
	"github.com/nerolabs/silt/core/demand"
	"github.com/nerolabs/silt/core/node"
	"github.com/nerolabs/silt/ports"
)

// TestDemandReceiptFlowBanksWitnessedDemand is the D-DEMAND wiring at the
// integration tier: a fetcher blind-withdraws a retrieval token from an issuer over
// the wire, "receives" an object's bytes, submits a PoR-bound delivery receipt to
// the server, and the server banks it into its NEUTRAL witnessed-demand observable —
// once per token, and only for a genuine, correctly-delivered, validly-issued
// receipt. A forged token and a replay are both rejected over the wire.
func TestDemandReceiptFlowBanksWitnessedDemand(t *testing.T) {
	const seed = 20260809
	cl := NewCluster(seed, 8, simnet.DefaultConfig(), node.DefaultConfig())
	issuer, server, fetcher := cl.Nodes[0], cl.Nodes[1], cl.Nodes[2]

	// The issuer blind-signs retrieval tokens (no ledger → free withdrawal; the
	// fee-burn cost-to-wash knob is D-DEMAND P3).
	issuerKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("issuer key: %v", err)
	}
	issuer.EnableTokenIssuer(issuerKey)

	// The server banks receipts (trusting the issuer's key) and the fetcher signs them.
	server.EnableDemandBank(&issuerKey.PublicKey)
	_, fetcherSigner, _ := ed25519.GenerateKey(rand.Reader)
	fetcher.SetSigner(fetcherSigner)

	// The object the fetcher "received" from the server.
	data := make([]byte, 32<<10)
	cl.rng.Read(data)
	object := ports.HashBytes(data)

	// Fetcher learns the issuer key, then withdraws a blind retrieval token.
	fetcher.FetchIssuerKey(issuer.ID(), func(error) {})
	cl.Sched.Run()
	var tok demand.Token
	var gotTok bool
	fetcher.AcquireDemandToken(rand.Reader, issuer.ID(), func(tk demand.Token, err error) {
		if err != nil {
			t.Fatalf("acquire demand token: %v", err)
		}
		tok, gotTok = tk, true
	})
	cl.Sched.Run()
	if !gotTok {
		t.Fatal("never acquired a demand token")
	}

	// Submit a delivery receipt for the object to the server.
	var credited, done bool
	fetcher.SubmitDeliveryReceipt(server.ID(), tok, object, data, func(c bool, err error) {
		if err != nil {
			t.Fatalf("submit receipt: %v", err)
		}
		credited, done = c, true
	})
	cl.Sched.Run()
	if !done || !credited {
		t.Fatalf("honest receipt was not banked (done=%v credited=%v)", done, credited)
	}
	if got := server.WitnessedDemand(object); got != 1 {
		t.Fatalf("witnessed demand = %d, want 1", got)
	}

	// Replay: resubmitting the SAME token must not double-count (the double-spend set).
	fetcher.SubmitDeliveryReceipt(server.ID(), tok, object, data, func(c bool, _ error) { credited = c })
	cl.Sched.Run()
	if credited {
		t.Fatal("a replayed receipt (same token serial) was banked twice")
	}
	if got := server.WitnessedDemand(object); got != 1 {
		t.Fatalf("replay inflated witnessed demand to %d, want 1", got)
	}

	// Forged issuer: a token blind-signed by a DIFFERENT key buys nothing.
	impostor, _ := rsa.GenerateKey(rand.Reader, 2048)
	serial := make([]byte, 32)
	rand.Read(serial)
	blinded, secret, _ := demand.Withdraw(rand.Reader, &impostor.PublicKey, serial)
	forged := demand.Unblind(&impostor.PublicKey, serial, demand.SignWithdrawal(impostor, blinded), secret)
	fetcher.SubmitDeliveryReceipt(server.ID(), forged, object, data, func(c bool, _ error) { credited = c })
	cl.Sched.Run()
	if credited {
		t.Fatal("a token from an impostor issuer was banked")
	}
	if got := server.WitnessedDemand(object); got != 1 {
		t.Fatalf("forged token changed witnessed demand to %d, want 1", got)
	}
}
