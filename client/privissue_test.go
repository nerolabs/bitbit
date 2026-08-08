package client

import (
	"crypto/rand"
	"crypto/rsa"
	"testing"
	"time"

	"github.com/nerolabs/silt/adapters/eventloop"
	"github.com/nerolabs/silt/adapters/identity"
	"github.com/nerolabs/silt/adapters/relay"
	"github.com/nerolabs/silt/adapters/tcpnet"
	"github.com/nerolabs/silt/core/blindtoken"
	"github.com/nerolabs/silt/core/demand"
	"github.com/nerolabs/silt/ports"
)

// mintCredit issues one valid blind credit against the issuer key (standing in for the
// durable-identity AcquireCredits step, which is linkable-but-blind and not the subject
// of these tests).
func mintCredit(t *testing.T, issuer *blindtoken.Issuer) ports.PublishCredit {
	t.Helper()
	pub := issuer.Public()
	serial, _ := blindtoken.NewSerial(rand.Reader)
	blinded, secret, err := blindtoken.BlindCredit(rand.Reader, pub, serial)
	if err != nil {
		t.Fatalf("blind credit: %v", err)
	}
	sig, err := issuer.Issue(func() error { return nil }, blinded)
	if err != nil {
		t.Fatalf("issue credit: %v", err)
	}
	return ports.PublishCredit{Serial: serial, Sig: blindtoken.Unblind(pub, sig, secret)}
}

// mockIssuerHandler answers a MsgTokenRequest: it requires+spends a valid blind credit
// (so an unfunded ephemeral identity can pay), records the authenticated identity on
// sawFrom, and blind-signs the request. Shared by the direct and relayed tests.
func mockIssuerHandler(tr *tcpnet.Transport, issuer *blindtoken.Issuer, sawFrom chan ports.NodeID) func(ports.NodeID, ports.Message) {
	spent := map[string]bool{}
	pub := issuer.Public()
	return func(from ports.NodeID, msg ports.Message) {
		if msg.Kind != ports.MsgTokenRequest {
			return
		}
		if msg.Credit == nil || !blindtoken.VerifyCredit(pub, msg.Credit.Serial, msg.Credit.Sig) || spent[string(msg.Credit.Serial)] {
			tr.Send(from, ports.Message{Kind: ports.MsgTokenReply, RID: msg.RID, OK: false})
			return
		}
		spent[string(msg.Credit.Serial)] = true
		select {
		case sawFrom <- from:
		default:
		}
		sig, ierr := issuer.Issue(func() error { return nil }, msg.Data)
		if ierr != nil {
			tr.Send(from, ports.Message{Kind: ports.MsgTokenReply, RID: msg.RID, OK: false})
			return
		}
		tr.Send(from, ports.Message{Kind: ports.MsgTokenReply, RID: msg.RID, OK: true, Data: sig})
	}
}

// registerWithRelay makes tr reachable through the relay at relayID@relayAddr and
// returns the node's relay-form address (relay:R@host:port) — how a NATed issuer is
// dialed, and how a fetcher routes THROUGH the relay to hide its IP.
func registerWithRelay(t *testing.T, ident *identity.Identity, relayID ports.NodeID, relayAddr string, tr *tcpnet.Transport) string {
	t.Helper()
	rc, err := relay.NewClient(ident, relayID, relayAddr, tr.RelayInbound, nil)
	if err != nil {
		t.Fatalf("relay client: %v", err)
	}
	ready := make(chan error, 1)
	go rc.Run(func(err error) { ready <- err })
	t.Cleanup(rc.Close)
	select {
	case err := <-ready:
		if err != nil {
			t.Fatalf("relay registration: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("relay registration timed out")
	}
	return rc.Addr()
}

// TestPrivateWithdrawalUsesEphemeralIdentity is the D3 slice-1 property over REAL TCP:
// a demand-token withdrawal made via WithdrawDemandTokenPrivately reaches the issuer
// over a FRESH EPHEMERAL identity and pays with a prepaid blind credit — so the identity
// the issuer authenticates is the throwaway one, NOT the fetcher's durable NodeID, and
// the withdrawal cannot be linked to the fetcher (the blind signature already hid the
// serial). Here a mock issuer records exactly which TLS identity it authenticated.
func TestPrivateWithdrawalUsesEphemeralIdentity(t *testing.T) {
	rng := rand.Reader

	// The token issuer: an ed25519 transport identity (its NodeID + TLS cert) and a
	// SEPARATE RSA token-issuer key (blind signatures over token serials).
	issuerIdent := identity.FromSeed(90001)
	issuerID := issuerIdent.NodeID()
	rsaKey, err := rsa.GenerateKey(rng, 2048)
	if err != nil {
		t.Fatalf("issuer rsa key: %v", err)
	}
	issuer := blindtoken.NewIssuer(rsaKey)
	issuerPub := issuer.Public()

	// A durable fetcher acquires a blind credit up front (this step IS linkable to the
	// fetcher — it pays for the credit — but the credit is blind, so SPENDING it later is not).
	credit := mintCredit(t, issuer)

	// The issuer transport records the TLS identity it authenticates and requires+spends
	// the credit (no durable account charged) before blind-signing.
	loop := eventloop.New()
	go loop.Run()
	defer loop.Stop()
	tr, err := tcpnet.New(loop, issuerIdent, "127.0.0.1:0")
	if err != nil {
		t.Fatalf("issuer transport: %v", err)
	}
	defer tr.Close()

	sawFrom := make(chan ports.NodeID, 1)
	tr.SetHandler(mockIssuerHandler(tr, issuer, sawFrom))

	// The private withdrawal: over a fresh ephemeral identity, paying with the credit.
	tok, ephID, err := WithdrawDemandTokenPrivately(rng, issuerID, tr.Addr(), issuerPub, credit, 10*time.Second)
	if err != nil {
		t.Fatalf("private withdrawal: %v", err)
	}

	// The token is a real, verifiable demand token.
	if !demand.VerifyToken(issuerPub, tok) {
		t.Fatal("privately-withdrawn token does not verify under the issuer key")
	}
	// The issuer authenticated the EPHEMERAL identity, never the issuer's own or any
	// durable fetcher id — the withdrawal is unlinkable to the fetcher.
	select {
	case from := <-sawFrom:
		if from != ephID {
			t.Fatalf("issuer authenticated %s, want the ephemeral withdrawer %s", from, ephID)
		}
		if from == issuerID {
			t.Fatal("withdrawer id collided with the issuer id")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("issuer never saw the token request")
	}
}

// TestPrivateWithdrawalRefusedWithoutCredit pins that the ephemeral path genuinely
// depends on the prepaid credit: without a valid credit the issuer (which cannot charge
// a durable account for an ephemeral identity) refuses, so the withdrawal fails rather
// than silently linking payment to some account.
func TestPrivateWithdrawalRefusedWithoutCredit(t *testing.T) {
	rng := rand.Reader
	issuerIdent := identity.FromSeed(90002)
	rsaKey, _ := rsa.GenerateKey(rng, 2048)
	issuer := blindtoken.NewIssuer(rsaKey)
	issuerPub := issuer.Public()

	loop := eventloop.New()
	go loop.Run()
	defer loop.Stop()
	tr, err := tcpnet.New(loop, issuerIdent, "127.0.0.1:0")
	if err != nil {
		t.Fatalf("issuer transport: %v", err)
	}
	defer tr.Close()
	tr.SetHandler(func(from ports.NodeID, msg ports.Message) {
		if msg.Kind != ports.MsgTokenRequest {
			return
		}
		if msg.Credit == nil || !blindtoken.VerifyCredit(issuerPub, msg.Credit.Serial, msg.Credit.Sig) {
			tr.Send(from, ports.Message{Kind: ports.MsgTokenReply, RID: msg.RID, OK: false})
			return
		}
		sig, _ := issuer.Issue(func() error { return nil }, msg.Data)
		tr.Send(from, ports.Message{Kind: ports.MsgTokenReply, RID: msg.RID, OK: true, Data: sig})
	})

	// An INVALID credit (not signed by the issuer) → refused → the withdrawal errors.
	bogus := ports.PublishCredit{Serial: []byte("nope"), Sig: []byte("bad")}
	_, _, err = WithdrawDemandTokenPrivately(rng, issuerIdent.NodeID(), tr.Addr(), issuerPub, bogus, 5*time.Second)
	if err == nil {
		t.Fatal("a withdrawal with no valid credit must fail (an ephemeral identity has no account to charge)")
	}
}

// TestPrivateWithdrawalThroughRelay is D3 slice 2 over REAL TCP: the private withdrawal
// is routed THROUGH a content-blind relay, so the issuer's inbound connection comes from
// the relay, not the fetcher — hiding the fetcher's IP as well as its identity. The
// fetcher is given ONLY the issuer's RELAY-form address (no direct address), so a
// successful withdrawal proves it went through the relay. The end-to-end TLS still
// authenticates the ephemeral key across the relay pipe (the relay carries bytes it
// cannot read).
func TestPrivateWithdrawalThroughRelay(t *testing.T) {
	rng := rand.Reader

	relayIdent := identity.FromSeed(90010)
	srv, err := relay.Serve("127.0.0.1:0", relayIdent, relay.Config{}, nil)
	if err != nil {
		t.Fatalf("relay serve: %v", err)
	}
	defer srv.Close()

	issuerIdent := identity.FromSeed(90011)
	issuerID := issuerIdent.NodeID()
	rsaKey, err := rsa.GenerateKey(rng, 2048)
	if err != nil {
		t.Fatalf("issuer rsa key: %v", err)
	}
	issuer := blindtoken.NewIssuer(rsaKey)
	issuerPub := issuer.Public()
	credit := mintCredit(t, issuer)

	loop := eventloop.New()
	go loop.Run()
	defer loop.Stop()
	tr, err := tcpnet.New(loop, issuerIdent, "127.0.0.1:0")
	if err != nil {
		t.Fatalf("issuer transport: %v", err)
	}
	defer tr.Close()

	sawFrom := make(chan ports.NodeID, 1)
	tr.SetHandler(mockIssuerHandler(tr, issuer, sawFrom))

	// The issuer registers with the relay; its relay-form address is the ONLY way the
	// fetcher is told to reach it.
	issuerRelayAddr := registerWithRelay(t, issuerIdent, relayIdent.NodeID(), srv.Addr(), tr)

	tok, ephID, err := WithdrawDemandTokenPrivately(rng, issuerID, issuerRelayAddr, issuerPub, credit, 15*time.Second)
	if err != nil {
		t.Fatalf("relayed private withdrawal: %v", err)
	}
	if !demand.VerifyToken(issuerPub, tok) {
		t.Fatal("relay-routed token does not verify")
	}
	// End-to-end TLS survived the relay: the issuer still authenticated the ephemeral key.
	select {
	case from := <-sawFrom:
		if from != ephID {
			t.Fatalf("issuer authenticated %s, want the ephemeral withdrawer %s", from, ephID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("issuer never saw the relayed token request")
	}
}
