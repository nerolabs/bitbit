package blindtoken

import (
	"errors"
	mrand "math/rand"
	"testing"
)

// The full publisher-privacy flow at the logic level: a publisher pays the fee
// with its durable identity, gets a blind token, and spends it unlinkably to
// publish — the fee is preserved, the spend is unlinkable, and a token spends
// exactly once.
func TestIssueChargesFeeSpendUnlinkableOnce(t *testing.T) {
	priv := testKey(t)
	iss := NewIssuer(priv)
	pub := iss.Public()
	rng := mrand.New(mrand.NewSource(4))

	// Publisher blinds a serial and asks the issuer for a token; the fee is
	// charged to the DURABLE identity here (modeled by the charge callback).
	serial, _ := NewSerial(rng)
	blinded, secret, _ := Blind(rng, pub, serial)

	charged := 0
	ok := func() error { charged++; return nil }
	blindSig, err := iss.Issue(ok, blinded)
	if err != nil {
		t.Fatal(err)
	}
	if charged != 1 {
		t.Fatalf("issuance must charge the fee exactly once, got %d", charged)
	}
	sig := Unblind(pub, blindSig, secret)

	// OUTCOME: the token spends (the issuer never saw this serial at issuance).
	if err := iss.Spend(serial, sig); err != nil {
		t.Fatalf("a validly-issued token must spend: %v", err)
	}
	// OUTCOME: it can't be spent twice.
	if err := iss.Spend(serial, sig); !errors.Is(err, ErrDoubleSpend) {
		t.Fatalf("re-spending a token must fail with ErrDoubleSpend, got %v", err)
	}
}

// Fee preserved: if the charge fails (no credit), no token is minted.
func TestNoFeeNoToken(t *testing.T) {
	priv := testKey(t)
	iss := NewIssuer(priv)
	rng := mrand.New(mrand.NewSource(5))
	serial, _ := NewSerial(rng)
	blinded, _, _ := Blind(rng, iss.Public(), serial)

	brokeErr := errors.New("insufficient credit")
	if _, err := iss.Issue(func() error { return brokeErr }, blinded); !errors.Is(err, brokeErr) {
		t.Fatalf("a failed fee charge must mint no token; got %v", err)
	}
}

// A forged token — never issued — cannot be spent.
func TestForgedTokenCannotSpend(t *testing.T) {
	priv := testKey(t)
	iss := NewIssuer(priv)
	rng := mrand.New(mrand.NewSource(6))
	serial, _ := NewSerial(rng)
	if err := iss.Spend(serial, []byte{9, 9, 9}); !errors.Is(err, ErrBadToken) {
		t.Fatalf("spending a token the issuer never signed must fail; got %v", err)
	}
}
