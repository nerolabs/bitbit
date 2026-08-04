// Package blindtoken is a Chaumian blind signature — the crypto behind
// publisher-privacy publish tokens (T3, risk 14 / catalog F1). The problem:
// the chain records a publish's Publisher NodeID, so an observer maps a
// durable reputation key to every root it published (silt protects who-READS
// far better than who-WRITES). A blind token breaks that link while keeping
// the fee/anti-spam economics:
//
//  1. A publisher, using its durable identity, asks the issuer for a token.
//     It BLINDS a random serial and sends only the blinded value; the issuer
//     charges the fee to the durable key and signs the blinded value — WITHOUT
//     ever seeing the serial.
//  2. The publisher UNBLINDS the issuer's signature into a valid signature on
//     the plain serial, and later spends (serial, sig) to publish. The token
//     verifies (a fee was paid) but is unlinkable to the issuance session, so
//     the publish carries no durable identity.
//
// This is textbook RSA-FDH blind signatures (Chaum 1982): sig = FDH(serial)^d
// mod N; blinding multiplies by r^e so the issuer signs FDH(serial)·r, and
// dividing by r recovers FDH(serial)^d. Standard construction, NOT
// independently audited (see the threat model's crypto-composition note).
package blindtoken

import (
	"crypto/rsa"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"
	"math/big"
)

const fdhDomain = "silt/blindtoken/fdh/v1"

// creditDomain is the FDH domain for PREPAID PUBLISH CREDITS (M0 privacy D3,
// red-team F4). A credit is a blind signature under the SAME issuer key as a
// publish token, but over a domain-separated message — so a credit signature can
// never be presented as a publish token (or vice versa): each verifies only
// under its own domain. That lets one issuer key mint both a bulk, fee-charged
// credit (decoupled from any publish) and, spending that credit, an unlinkable
// publish token WITHOUT a second per-publish fee — the ledger link the red-team
// exploited. This is online Chaumian e-cash (Chaum 1982): the coin is a
// blind-signed serial, double-spend is caught by the issuer's spent-set.
const creditDomain = "silt/blindcredit/fdh/v1"

// SerialSize is the length of a token's random serial.
const SerialSize = 32

var (
	ErrBadToken = errors.New("blindtoken: signature does not verify")
	ErrZeroHash = errors.New("blindtoken: serial hashed to zero (retry)")
)

// NewSerial draws a fresh random token serial.
func NewSerial(r io.Reader) ([]byte, error) {
	s := make([]byte, SerialSize)
	if _, err := io.ReadFull(r, s); err != nil {
		return nil, err
	}
	return s, nil
}

// fullDomainHash maps serial to an element of Z_N (RSA-FDH) under the default
// publish-token domain. See fullDomainHashD for the domain-separated form.
func fullDomainHash(pub *rsa.PublicKey, serial []byte) *big.Int {
	return fullDomainHashD(pub, serial, fdhDomain)
}

// fullDomainHashD maps serial to an element of Z_N (RSA-FDH) under an explicit
// domain: expand SHA-256 in counter mode past the modulus length, then reduce.
// Domain-separated and a few bytes wide of N to keep the mod bias negligible.
// Distinct domains yield unrelated messages under the same key, which is how a
// credit signature and a token signature made by one issuer never interchange.
func fullDomainHashD(pub *rsa.PublicKey, serial []byte, domain string) *big.Int {
	nLen := (pub.N.BitLen() + 7) / 8
	out := make([]byte, 0, nLen+sha256.Size)
	for ctr := uint32(0); len(out) < nLen+8; ctr++ {
		h := sha256.New()
		h.Write([]byte(domain))
		var cb [4]byte
		binary.BigEndian.PutUint32(cb[:], ctr)
		h.Write(cb[:])
		h.Write(serial)
		out = h.Sum(out)
	}
	m := new(big.Int).SetBytes(out)
	return m.Mod(m, pub.N)
}

// randInt draws a random value in [0, max) from the injected reader. Core must
// not touch ambient randomness (crypto/rand) — the adapter passes crypto/rand,
// a sim passes a seeded source. A few extra bytes keep the mod bias negligible;
// uniformity isn't security-critical for the blinding factor anyway.
func randInt(rng io.Reader, max *big.Int) (*big.Int, error) {
	b := make([]byte, (max.BitLen()+7)/8+8)
	if _, err := io.ReadFull(rng, b); err != nil {
		return nil, err
	}
	return new(big.Int).Mod(new(big.Int).SetBytes(b), max), nil
}

// Blind blinds serial for the issuer and returns the blinded value to send and
// the secret (blinding factor) the publisher keeps to unblind the reply. Both
// are big-endian byte strings. rng supplies randomness (injected, not ambient).
func Blind(rng io.Reader, pub *rsa.PublicKey, serial []byte) (blinded, secret []byte, err error) {
	return blindD(rng, pub, serial, fdhDomain)
}

// BlindCredit blinds serial as a PREPAID CREDIT (domain-separated from a publish
// token, same key). Used at bulk mint time; the returned credit spends later for
// a token with no per-publish fee (F4).
func BlindCredit(rng io.Reader, pub *rsa.PublicKey, serial []byte) (blinded, secret []byte, err error) {
	return blindD(rng, pub, serial, creditDomain)
}

func blindD(rng io.Reader, pub *rsa.PublicKey, serial []byte, domain string) (blinded, secret []byte, err error) {
	m := fullDomainHashD(pub, serial, domain)
	if m.Sign() == 0 {
		return nil, nil, ErrZeroHash
	}
	// r random in [2, N), invertible mod N.
	var r, rInv *big.Int
	for {
		r, err = randInt(rng, pub.N)
		if err != nil {
			return nil, nil, err
		}
		if r.Sign() == 0 {
			continue
		}
		rInv = new(big.Int).ModInverse(r, pub.N)
		if rInv != nil { // coprime to N (overwhelmingly likely)
			break
		}
	}
	// blinded = m * r^e mod N
	re := new(big.Int).Exp(r, big.NewInt(int64(pub.E)), pub.N)
	b := new(big.Int).Mul(m, re)
	b.Mod(b, pub.N)
	return b.Bytes(), r.Bytes(), nil
}

// SignBlinded is the ISSUER side: it signs the blinded value, learning nothing
// about the serial. (Charging the fee to the requester is the caller's job.)
func SignBlinded(priv *rsa.PrivateKey, blinded []byte) []byte {
	b := new(big.Int).SetBytes(blinded)
	b.Mod(b, priv.N)
	s := new(big.Int).Exp(b, priv.D, priv.N)
	return s.Bytes()
}

// Unblind turns the issuer's blind signature into a signature on the plain
// serial, using the secret from Blind.
func Unblind(pub *rsa.PublicKey, blindSig, secret []byte) []byte {
	bs := new(big.Int).SetBytes(blindSig)
	r := new(big.Int).SetBytes(secret)
	rInv := new(big.Int).ModInverse(r, pub.N)
	if rInv == nil {
		return nil
	}
	sig := new(big.Int).Mul(bs, rInv)
	sig.Mod(sig, pub.N)
	return sig.Bytes()
}

// Verify checks that sig is a valid issuer signature on serial: sig^e == FDH(serial).
func Verify(pub *rsa.PublicKey, serial, sig []byte) bool {
	return verifyD(pub, serial, sig, fdhDomain)
}

// VerifyCredit checks that sig is a valid issuer signature on a prepaid CREDIT
// serial (credit domain). A publish-token signature fails this check and vice
// versa, so the two are not interchangeable even under one key.
func VerifyCredit(pub *rsa.PublicKey, serial, sig []byte) bool {
	return verifyD(pub, serial, sig, creditDomain)
}

func verifyD(pub *rsa.PublicKey, serial, sig []byte, domain string) bool {
	m := fullDomainHashD(pub, serial, domain)
	s := new(big.Int).SetBytes(sig)
	check := new(big.Int).Exp(s, big.NewInt(int64(pub.E)), pub.N)
	return check.Cmp(m) == 0
}
