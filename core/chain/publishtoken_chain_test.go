package chain

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	mrand "math/rand"
	"testing"

	"github.com/nerolabs/silt/core/blindtoken"
	"github.com/nerolabs/silt/ports"
)

// TestPublishTokensGateAndPreventDoubleSpend is the chain-level outcome of
// publisher privacy (F1): when the chain requires publish tokens, an entry
// commits only if it carries a quorum-issued token (a serial blind-signed by k
// distinct validators) — carrying NO publisher identity — and each serial can
// be spent only once, chain-wide.
func TestPublishTokensGateAndPreventDoubleSpend(t *testing.T) {
	prop := key(1)
	vals := []ed25519.PrivateKey{key(2), key(3), key(4)}

	// Each validator has a block-signing key (above) AND an RSA token-issuer
	// key (here), both keyed by the same NodeID.
	issuers := map[ports.NodeID]*rsa.PrivateKey{}
	reps := map[ports.NodeID]int64{idOf(prop): 1000}
	for _, v := range vals {
		k, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatal(err)
		}
		issuers[idOf(v)] = k
		reps[idOf(v)] = 1000
	}

	c := New(Config{MinProposerRep: 100, MinAttesterRep: 100, Quorum: 2},
		func(n ports.NodeID) int64 { return reps[n] })
	c.RequireTokens(2, func(n ports.NodeID) *rsa.PublicKey {
		if k, ok := issuers[n]; ok {
			return &k.PublicKey
		}
		return nil
	})

	rng := mrand.New(mrand.NewSource(1))
	mint := func(serial []byte, signers []ed25519.PrivateKey) *ports.PublishToken {
		tok := &ports.PublishToken{Serial: serial}
		for _, v := range signers {
			iss := blindtoken.NewIssuer(issuers[idOf(v)])
			blinded, secret, err := blindtoken.Blind(rng, iss.Public(), serial)
			if err != nil {
				t.Fatal(err)
			}
			blindSig, _ := iss.Issue(func() error { return nil }, blinded)
			tok.Sigs = append(tok.Sigs, ports.TokenSig{Validator: idOf(v), Sig: blindtoken.Unblind(iss.Public(), blindSig, secret)})
		}
		return tok
	}
	withToken := func(b byte, tok *ports.PublishToken) ports.Entry {
		e := entry(b)
		e.Token = tok
		return e
	}
	commit := func(e ports.Entry) error {
		prev, height := c.Head()
		blk := &Block{Version: BlockVersion, Height: height, Prev: prev, Entries: []ports.Entry{e}}
		Sign(blk, prop)
		blk.Atts = append(blk.Atts, Attest(blk, vals[0]), Attest(blk, vals[1]))
		return c.Append(*blk)
	}

	serial1, _ := blindtoken.NewSerial(rng)

	// OUTCOME 1: no token → refused (a Publisher-less entry can't slip through).
	if err := commit(entry(1)); !errors.Is(err, ErrTokenRequired) {
		t.Fatalf("token-required chain must refuse an entry with no token; got %v", err)
	}
	// OUTCOME 2: a valid 2-of-3 quorum token → commits, with no Publisher.
	if err := commit(withToken(1, mint(serial1, vals[:2]))); err != nil {
		t.Fatalf("a valid quorum-token entry should commit; got %v", err)
	}
	// OUTCOME 3: reusing the serial → double-spend refused.
	if err := commit(withToken(2, mint(serial1, vals[:2]))); !errors.Is(err, ErrTokenSpent) {
		t.Fatalf("re-spending a token serial must be refused; got %v", err)
	}
	// OUTCOME 4: too few validator signatures (1 < quorum 2) → refused.
	serial2, _ := blindtoken.NewSerial(rng)
	if err := commit(withToken(3, mint(serial2, vals[:1]))); err == nil {
		t.Fatal("a token with too few validator signatures must be refused")
	}
}
