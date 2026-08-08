package client

import (
	"crypto/rand"
	"crypto/rsa"
	"testing"
	"time"

	"github.com/nerolabs/silt/adapters/eventloop"
	"github.com/nerolabs/silt/adapters/identity"
	"github.com/nerolabs/silt/adapters/tcpnet"
	"github.com/nerolabs/silt/core/blindtoken"
	"github.com/nerolabs/silt/core/demand"
	"github.com/nerolabs/silt/ports"
)

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
	// fetcher — it pays for the credit — but the credit is blind, so SPENDING it later is
	// not). Mint one valid credit against the issuer key.
	creditSerial, _ := blindtoken.NewSerial(rng)
	cblinded, csecret, err := blindtoken.BlindCredit(rng, issuerPub, creditSerial)
	if err != nil {
		t.Fatalf("blind credit: %v", err)
	}
	csig, err := issuer.Issue(func() error { return nil }, cblinded)
	if err != nil {
		t.Fatalf("issue credit: %v", err)
	}
	credit := ports.PublishCredit{Serial: creditSerial, Sig: blindtoken.Unblind(issuerPub, csig, csecret)}

	// The issuer transport: records the TLS identity it authenticates on a token
	// request, requires+spends the credit (no durable account charged), and blind-signs.
	loop := eventloop.New()
	go loop.Run()
	defer loop.Stop()
	tr, err := tcpnet.New(loop, issuerIdent, "127.0.0.1:0")
	if err != nil {
		t.Fatalf("issuer transport: %v", err)
	}
	defer tr.Close()

	sawFrom := make(chan ports.NodeID, 1)
	spent := map[string]bool{}
	tr.SetHandler(func(from ports.NodeID, msg ports.Message) {
		if msg.Kind != ports.MsgTokenRequest {
			return
		}
		// Payment MUST be a valid, unspent blind credit — a request with no credit (which
		// would charge the durable `from`) is refused. This is what lets an unfunded
		// ephemeral identity withdraw.
		if msg.Credit == nil || !blindtoken.VerifyCredit(issuerPub, msg.Credit.Serial, msg.Credit.Sig) || spent[string(msg.Credit.Serial)] {
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
	})

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
