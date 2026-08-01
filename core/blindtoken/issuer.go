package blindtoken

import (
	"crypto/rsa"
	"errors"
)

// ErrDoubleSpend is returned when a token serial is presented more than once.
var ErrDoubleSpend = errors.New("blindtoken: token already spent")

// Issuer blind-signs fee-paid publish tokens and later accepts them for spend,
// rejecting double-spends. The RSA key is generated at the edge and injected;
// the fee is charged through a callback, so this stays free of the ledger and
// ports — the token economics without the coupling.
//
// The spent set here is in-memory; the full design records spends ON-CHAIN so
// double-spend is caught network-wide (a publish Entry carries the token, and
// the chain rejects a duplicate serial the way it rejects a duplicate root).
// This is the single-issuer core the wire integration builds on.
type Issuer struct {
	key   *rsa.PrivateKey
	spent map[string]bool
}

func NewIssuer(key *rsa.PrivateKey) *Issuer {
	return &Issuer{key: key, spent: make(map[string]bool)}
}

// Public is the issuer verification key — published so anyone can check a token.
func (i *Issuer) Public() *rsa.PublicKey { return &i.key.PublicKey }

// Issue charges the fee (via charge) to the requesting identity, then
// blind-signs the token — learning nothing about its serial. If the charge
// fails (e.g. insufficient credit), no token is minted.
func (i *Issuer) Issue(charge func() error, blinded []byte) ([]byte, error) {
	if err := charge(); err != nil {
		return nil, err
	}
	return SignBlinded(i.key, blinded), nil
}

// Spend accepts a token for a publish: it must verify against the issuer key
// and not have been spent before. The (serial, sig) carries no link to the
// identity that paid at issuance.
func (i *Issuer) Spend(serial, sig []byte) error {
	if !Verify(&i.key.PublicKey, serial, sig) {
		return ErrBadToken
	}
	if i.spent[string(serial)] {
		return ErrDoubleSpend
	}
	i.spent[string(serial)] = true
	return nil
}
