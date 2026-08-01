package publishtoken

import (
	"crypto/rand"
	"crypto/rsa"
	"errors"
	mrand "math/rand"
	"testing"

	"github.com/nerolabs/silt/core/blindtoken"
	"github.com/nerolabs/silt/ports"
)

type validator struct {
	id  ports.NodeID
	iss *blindtoken.Issuer
	pub *rsa.PublicKey
}

func mkValidators(t *testing.T, n int) []validator {
	t.Helper()
	vs := make([]validator, n)
	for i := range vs {
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatal(err)
		}
		iss := blindtoken.NewIssuer(key)
		vs[i] = validator{id: ports.HashBytes([]byte{byte(i + 1)}), iss: iss, pub: iss.Public()}
	}
	return vs
}

// mkToken plays the publisher: it gets a blind signature from each signer
// (paying the fee, here a no-op) and assembles the quorum token.
func mkToken(t *testing.T, rng *mrand.Rand, serial []byte, signers []validator) Token {
	t.Helper()
	tok := Token{Serial: serial}
	for _, v := range signers {
		blinded, secret, err := blindtoken.Blind(rng, v.pub, serial)
		if err != nil {
			t.Fatal(err)
		}
		blindSig, err := v.iss.Issue(func() error { return nil }, blinded)
		if err != nil {
			t.Fatal(err)
		}
		tok.Sigs = append(tok.Sigs, Sig{Validator: v.id, Sig: blindtoken.Unblind(v.pub, blindSig, secret)})
	}
	return tok
}

func keyLookup(vs []validator) func(ports.NodeID) *rsa.PublicKey {
	return func(id ports.NodeID) *rsa.PublicKey {
		for _, v := range vs {
			if v.id == id {
				return v.pub
			}
		}
		return nil
	}
}

func all(ports.NodeID) bool { return true }

// A token blind-signed by a quorum of distinct validators verifies — no single
// issuer minted it, and it carries no publisher identity.
func TestQuorumTokenVerifies(t *testing.T) {
	vs := mkValidators(t, 4)
	rng := mrand.New(mrand.NewSource(1))
	serial, _ := blindtoken.NewSerial(rng)
	tok := mkToken(t, rng, serial, vs[:3])
	if err := Verify(tok, 3, keyLookup(vs), all); err != nil {
		t.Fatalf("3 distinct validators at k=3 should verify: %v", err)
	}
}

// Fewer than k distinct validators cannot mint publish rights (no single issuer).
func TestTooFewSignaturesFail(t *testing.T) {
	vs := mkValidators(t, 4)
	rng := mrand.New(mrand.NewSource(2))
	serial, _ := blindtoken.NewSerial(rng)
	tok := mkToken(t, rng, serial, vs[:2])
	if err := Verify(tok, 3, keyLookup(vs), all); !errors.Is(err, ErrInsufficientSigs) {
		t.Fatalf("2 sigs at k=3 must fail; got %v", err)
	}
}

// A forged validator signature fails the whole token.
func TestForgedValidatorSigFails(t *testing.T) {
	vs := mkValidators(t, 4)
	rng := mrand.New(mrand.NewSource(3))
	serial, _ := blindtoken.NewSerial(rng)
	tok := mkToken(t, rng, serial, vs[:3])
	tok.Sigs[0].Sig = []byte{1, 2, 3}
	if err := Verify(tok, 3, keyLookup(vs), all); !errors.Is(err, ErrBadSig) {
		t.Fatalf("a forged validator signature must fail the token; got %v", err)
	}
}

// Unqualified validators and duplicated signatures don't count toward quorum.
func TestUnqualifiedAndDuplicateDontCount(t *testing.T) {
	vs := mkValidators(t, 4)
	rng := mrand.New(mrand.NewSource(4))
	serial, _ := blindtoken.NewSerial(rng)

	tok := mkToken(t, rng, serial, vs[:3])
	notV2 := func(id ports.NodeID) bool { return id != vs[2].id }
	if err := Verify(tok, 3, keyLookup(vs), notV2); !errors.Is(err, ErrInsufficientSigs) {
		t.Fatalf("an unqualified validator's sig must not count; got %v", err)
	}

	tok2 := mkToken(t, rng, serial, []validator{vs[0], vs[1]})
	tok2.Sigs = append(tok2.Sigs, tok2.Sigs[0]) // duplicate vs[0]
	if err := Verify(tok2, 3, keyLookup(vs), all); !errors.Is(err, ErrInsufficientSigs) {
		t.Fatalf("a duplicated validator sig must not reach quorum; got %v", err)
	}
}
