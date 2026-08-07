package demand

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"testing"

	"github.com/nerolabs/silt/core/blindtoken"
	"github.com/nerolabs/silt/core/por"
	"github.com/nerolabs/silt/ports"
)

// scene sets up a (blind-signing RSA) issuer, a fetcher, a server, and one object C
// with the bytes + PoR tags the fetcher would have received from the server.
type scene struct {
	issuerPub  *rsa.PublicKey
	issuerPriv *rsa.PrivateKey
	fetcher    ed25519.PrivateKey
	server     ports.NodeID
	object     ports.Hash
	data       []byte
	tags       [][]byte
}

func newScene(t *testing.T, objectLabel string) scene {
	t.Helper()
	ipriv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("issuer key: %v", err)
	}
	_, fpriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("fetcher key: %v", err)
	}
	object := ports.HashBytes([]byte(objectLabel))
	data := make([]byte, por.DefaultParams.SectorsPerBlock*por.SectorBytes*4) // ~4 blocks
	for i := range data {
		data[i] = byte(i*31 + 7)
	}
	tags := ObjectKey(object).Tags(object[:], data) // as the publisher would tag C
	return scene{
		issuerPub: &ipriv.PublicKey, issuerPriv: ipriv, fetcher: fpriv,
		server: ports.HashBytes([]byte("server-A")), object: object, data: data, tags: tags,
	}
}

// token runs a full blind withdrawal: the fetcher blinds a fresh serial, the issuer
// blind-signs it (never seeing the serial), and the fetcher unblinds into a Token.
func (s scene) token(t *testing.T) Token {
	t.Helper()
	serial, err := blindtoken.NewSerial(rand.Reader)
	if err != nil {
		t.Fatalf("serial: %v", err)
	}
	blinded, secret, err := Withdraw(rand.Reader, s.issuerPub, serial)
	if err != nil {
		t.Fatalf("withdraw: %v", err)
	}
	blindSig := SignWithdrawal(s.issuerPriv, blinded)
	return Unblind(s.issuerPub, serial, blindSig, secret)
}

// TestHonestDeliveryCreditsDemand: a real issued token, spent on a PoR-bound
// delivery-ack over the correct bytes, redeems once and credits the object's
// witnessed-demand counter — and only the observable counter, never standing.
func TestHonestDeliveryCreditsDemand(t *testing.T) {
	s := newScene(t, "obj-C")
	tok := s.token(t)
	r, err := Ack(s.fetcher, tok, s.object, s.server, s.data, s.tags)
	if err != nil {
		t.Fatalf("ack: %v", err)
	}
	bank := NewBank()
	if ok, reason := bank.Redeem(s.issuerPub, tok, r); !ok {
		t.Fatalf("honest receipt rejected: %s", reason)
	}
	if got := bank.Demand(s.object); got != 1 {
		t.Fatalf("demand = %d, want 1", got)
	}
}

// TestDoubleSpendRejected: a token serial redeems at most once — banking the same
// receipt twice credits demand only once (#receipts ≤ #issued tokens).
func TestDoubleSpendRejected(t *testing.T) {
	s := newScene(t, "obj-C")
	tok := s.token(t)
	r, _ := Ack(s.fetcher, tok, s.object, s.server, s.data, s.tags)
	bank := NewBank()
	if ok, _ := bank.Redeem(s.issuerPub, tok, r); !ok {
		t.Fatal("first redeem should succeed")
	}
	if ok, reason := bank.Redeem(s.issuerPub, tok, r); ok || reason == "" {
		t.Fatalf("second redeem of the same serial must be rejected, got ok=%v", ok)
	}
	if got := bank.Demand(s.object); got != 1 {
		t.Fatalf("double-spend inflated demand to %d, want 1", got)
	}
}

// TestForgedTokenRejected: a serial not signed by the issuer buys nothing, even
// with an otherwise valid delivery proof.
func TestForgedTokenRejected(t *testing.T) {
	s := newScene(t, "obj-C")
	// A token blind-signed by an IMPOSTOR issuer key.
	impostor, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("impostor key: %v", err)
	}
	serial, _ := blindtoken.NewSerial(rand.Reader)
	blinded, secret, _ := Withdraw(rand.Reader, &impostor.PublicKey, serial)
	forged := Unblind(&impostor.PublicKey, serial, SignWithdrawal(impostor, blinded), secret)
	r, _ := Ack(s.fetcher, forged, s.object, s.server, s.data, s.tags)
	bank := NewBank()
	if ok, _ := bank.Redeem(s.issuerPub, forged, r); ok {
		t.Fatal("a token not signed by the REAL issuer must be rejected")
	}
	if bank.Demand(s.object) != 0 {
		t.Fatal("forged token credited demand")
	}
}

// TestBlindWithdrawalIsUnlinkable pins P1: the issuer, signing a blinded
// withdrawal, learns nothing that ties it to the serial the token later redeems
// under — the blinded value it saw is independent of the plain serial. (This is the
// cryptographic half; the IP/timing channel is D3's job, still nominal.)
func TestBlindWithdrawalIsUnlinkable(t *testing.T) {
	s := newScene(t, "obj-C")
	serial, _ := blindtoken.NewSerial(rand.Reader)
	blinded, secret, err := Withdraw(rand.Reader, s.issuerPub, serial)
	if err != nil {
		t.Fatalf("withdraw: %v", err)
	}
	// The token redeems under a valid signature the issuer never made on the serial
	// directly (it signed only `blinded`).
	tok := Unblind(s.issuerPub, serial, SignWithdrawal(s.issuerPriv, blinded), secret)
	if !VerifyToken(s.issuerPub, tok) {
		t.Fatal("a blind-withdrawn token must verify under the issuer key")
	}
	// The issuer's signing-time view (`blinded`) must not equal or reveal the serial:
	// a blinding factor r makes blinded = m·rᵉ, uniformly hiding m=FDH(serial).
	if string(blinded) == string(serial) || string(blinded) == string(tok.Serial) {
		t.Fatal("the blinded value the issuer signed leaked the serial — withdrawal is not blind")
	}
	// End to end: the unlinkable token still spends on a correct delivery.
	r, _ := Ack(s.fetcher, tok, s.object, s.server, s.data, s.tags)
	if ok, reason := NewBank().Redeem(s.issuerPub, tok, r); !ok {
		t.Fatalf("blind-withdrawn token failed to redeem on a real delivery: %s", reason)
	}
}

// TestTamperedReceiptRejected: the fetcher signature covers what was delivered, to
// whom-for, and the possession proof — so mutating any of them (or lifting the
// proof onto another receipt) is caught. A server cannot mint a receipt the fetcher
// did not sign.
func TestTamperedReceiptRejected(t *testing.T) {
	s := newScene(t, "obj-C")
	tok := s.token(t)
	good, _ := Ack(s.fetcher, tok, s.object, s.server, s.data, s.tags)
	bank := NewBank()

	mutations := map[string]func(r *DeliveryReceipt){
		"flip server":      func(r *DeliveryReceipt) { r.Server = ports.HashBytes([]byte("server-B")) },
		"flip object":      func(r *DeliveryReceipt) { r.Object = ports.HashBytes([]byte("obj-D")) },
		"flip fetcher key": func(r *DeliveryReceipt) { pub, _, _ := ed25519.GenerateKey(rand.Reader); r.Fetcher = pub },
		"flip a sig byte":  func(r *DeliveryReceipt) { r.Sig[0] ^= 0xFF },
	}
	for name, mutate := range mutations {
		r := good // struct copy
		r.Sig = append([]byte(nil), good.Sig...)
		r.Fetcher = append([]byte(nil), good.Fetcher...)
		mutate(&r)
		if ok, _ := bank.Redeem(s.issuerPub, tok, r); ok {
			t.Fatalf("%s: a tampered receipt must be rejected", name)
		}
	}
	if bank.Demand(s.object) != 0 {
		t.Fatal("a tampered receipt credited demand")
	}
}

// TestWrongObjectRejected pins the PoR binding (not just the signature): a fetcher
// that genuinely holds object C cannot mint a validly-signed receipt claiming it
// delivered a DIFFERENT object C'. The possession proof is built over C's bytes/tags
// but must verify under C”s key and challenge, which it does not.
func TestWrongObjectRejected(t *testing.T) {
	s := newScene(t, "obj-C")
	other := ports.HashBytes([]byte("obj-C-prime"))
	tok := s.token(t)
	// A self-consistent, fetcher-SIGNED receipt for `other`, but whose proof was
	// produced from C's bytes and C's tags.
	r, err := Ack(s.fetcher, tok, other, s.server, s.data, s.tags)
	if err != nil {
		t.Fatalf("ack: %v", err)
	}
	if r.Object != other {
		t.Fatal("setup: receipt should claim the other object")
	}
	bank := NewBank()
	if ok, _ := bank.Redeem(s.issuerPub, tok, r); ok {
		t.Fatal("a receipt claiming C' while holding C's bytes must be rejected — the proof binds to the named object")
	}
}

// TestDataLessRedeemerRejected: no correct delivery, no receipt. A fetcher (or a
// self-dealing server) that did not hold the correct bytes cannot produce a proof
// that verifies — modelled by proving over the right tags but the wrong bytes (the
// H7 liar shape).
func TestDataLessRedeemerRejected(t *testing.T) {
	s := newScene(t, "obj-C")
	tok := s.token(t)
	wrong := make([]byte, len(s.data)) // all zeros: held nothing of C
	r, err := Ack(s.fetcher, tok, s.object, s.server, wrong, s.tags)
	if err != nil {
		t.Fatalf("ack: %v", err)
	}
	bank := NewBank()
	if ok, _ := bank.Redeem(s.issuerPub, tok, r); ok {
		t.Fatal("a proof over bytes that are not the object's must be rejected — retrievability of the correct C is the binding")
	}
}
