package blindtoken

import (
	"crypto/rand"
	"crypto/rsa"
	"math/big"
	mrand "math/rand"
	"testing"
)

func testKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// A token blind-signed by the issuer verifies once unblinded — and the issuer
// never saw the serial. This is the fee-preserving, unlinkable publish token.
func TestBlindRoundTrip(t *testing.T) {
	priv := testKey(t)
	pub := &priv.PublicKey
	rng := mrand.New(mrand.NewSource(1))

	serial, _ := NewSerial(rng)
	blinded, secret, err := Blind(rng, pub, serial)
	if err != nil {
		t.Fatal(err)
	}
	sig := Unblind(pub, SignBlinded(priv, blinded), secret)
	if !Verify(pub, serial, sig) {
		t.Fatal("a blind-signed token must verify on its plain serial")
	}
}

// V3 adversary: a token can't be minted without the issuer's key, and a
// signature can't be moved to a different serial.
func TestForgeryAndTamperFail(t *testing.T) {
	priv := testKey(t)
	pub := &priv.PublicKey
	rng := mrand.New(mrand.NewSource(2))

	serial, _ := NewSerial(rng)
	if Verify(pub, serial, []byte{1, 2, 3}) {
		t.Fatal("a forged signature must not verify")
	}
	blinded, secret, _ := Blind(rng, pub, serial)
	sig := Unblind(pub, SignBlinded(priv, blinded), secret)

	other, _ := NewSerial(rng)
	if Verify(pub, other, sig) {
		t.Fatal("a signature must not verify against a different serial")
	}
	stranger := testKey(t)
	if Verify(&stranger.PublicKey, serial, sig) {
		t.Fatal("a token must not verify under a different issuer key")
	}
}

// The unlinkability MECHANISM: the issuer signs a blinded value that is
// independent of the serial, and blinding the SAME serial twice yields
// different blinded values — so the issuer can't correlate what it signed at
// issuance with a token presented later at publish.
func TestIssuerSeesNothingLinkable(t *testing.T) {
	priv := testKey(t)
	pub := &priv.PublicKey
	rng := mrand.New(mrand.NewSource(3))

	serial, _ := NewSerial(rng)
	fdh := fullDomainHash(pub, serial)
	b1, s1, _ := Blind(rng, pub, serial)
	b2, s2, _ := Blind(rng, pub, serial)

	if new(big.Int).SetBytes(b1).Cmp(fdh) == 0 {
		t.Fatal("the issuer must not see FDH(serial) — that would be linkable")
	}
	if string(b1) == string(b2) {
		t.Fatal("blinding must be randomized — equal blinded values would be linkable")
	}
	// Both still unblind to valid tokens on the same serial.
	for _, p := range []struct{ b, s []byte }{{b1, s1}, {b2, s2}} {
		sig := Unblind(pub, SignBlinded(priv, p.b), p.s)
		if !Verify(pub, serial, sig) {
			t.Fatal("both blindings must unblind to a valid token")
		}
	}
}
