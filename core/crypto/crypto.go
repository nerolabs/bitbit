// Package crypto encrypts and decrypts individual chunks. Two modes:
//
// Convergent: the key is derived from the plaintext itself, so identical
// plaintext chunks produce identical ciphertext — anyone storing the same
// file stores the same chunks, giving global deduplication for free.
//
// Tradeoff (the "confirmation attack"): because encryption is
// deterministic, anyone who can GUESS your exact plaintext can encrypt
// their guess and check whether the resulting chunk exists in the
// network. Convergent encryption hides content only when the content is
// not guessable. Fine for public/shared data; wrong for secrets. See
// docs/math/02-convergent-encryption.md.
//
// Private: one random key per file, AES-256-GCM per chunk, with the chunk
// index bound into the nonce so a manifest full of valid ciphertexts
// still can't be reassembled in a different order (chunk i decrypts only
// as chunk i).
//
// Nothing here touches an RNG directly — randomness for private keys is
// injected by the caller, which keeps the core deterministic under test.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
)

// Mode selects an encryption scheme; it is recorded in the manifest.
type Mode string

const (
	Convergent Mode = "convergent"
	Private    Mode = "private"
)

func ParseMode(s string) (Mode, error) {
	switch Mode(s) {
	case Convergent, Private:
		return Mode(s), nil
	}
	return "", fmt.Errorf("crypto: unknown mode %q", s)
}

// KeySize is the AES-256 key length; SecretSize is the per-chunk secret
// stored in manifests for convergent mode.
const (
	KeySize    = 32
	SecretSize = 32
	nonceSize  = 12
)

const (
	convergentAAD = "github.com/nerolabs/silt/convergent/v1"
	privateAAD    = "github.com/nerolabs/silt/private/v1"
)

// ConvergentEncrypt encrypts one plaintext chunk. The returned secret is
// SHA-256(plaintext); key and nonce are both HKDF-derived from it. Using
// a fixed nonce is normally fatal for GCM, but here it is safe by
// construction: the key is unique to this exact plaintext, so the
// (key, nonce) pair can never be reused for two different messages.
// Determinism is the point — it is what makes dedup work.
func ConvergentEncrypt(plaintext []byte) (ciphertext []byte, secret [SecretSize]byte, err error) {
	secret = sha256.Sum256(plaintext)
	ct, err := convergentSeal(plaintext, secret)
	return ct, secret, err
}

// ConvergentDecrypt reverses ConvergentEncrypt given the secret recorded
// in the manifest, and additionally checks the recovered plaintext really
// hashes to the secret — a manifest lying about a secret is caught here.
func ConvergentDecrypt(ciphertext []byte, secret [SecretSize]byte) ([]byte, error) {
	gcm, nonce, err := convergentCipher(secret)
	if err != nil {
		return nil, err
	}
	pt, err := gcm.Open(nil, nonce, ciphertext, []byte(convergentAAD))
	if err != nil {
		return nil, fmt.Errorf("crypto: convergent decrypt: %w", err)
	}
	if sha256.Sum256(pt) != secret {
		return nil, fmt.Errorf("crypto: plaintext does not match convergent secret")
	}
	return pt, nil
}

func convergentSeal(plaintext []byte, secret [SecretSize]byte) ([]byte, error) {
	gcm, nonce, err := convergentCipher(secret)
	if err != nil {
		return nil, err
	}
	return gcm.Seal(nil, nonce, plaintext, []byte(convergentAAD)), nil
}

func convergentCipher(secret [SecretSize]byte) (cipher.AEAD, []byte, error) {
	key, err := hkdf.Key(sha256.New, secret[:], nil, "github.com/nerolabs/silt/convergent/v1/key", KeySize)
	if err != nil {
		return nil, nil, err
	}
	nonce, err := hkdf.Key(sha256.New, secret[:], nil, "github.com/nerolabs/silt/convergent/v1/nonce", nonceSize)
	if err != nil {
		return nil, nil, err
	}
	gcm, err := newGCM(key)
	return gcm, nonce, err
}

// NewFileKey draws a random per-file key for private mode from the
// injected randomness source.
func NewFileKey(rand io.Reader) ([KeySize]byte, error) {
	var key [KeySize]byte
	if _, err := io.ReadFull(rand, key[:]); err != nil {
		return key, fmt.Errorf("crypto: generating file key: %w", err)
	}
	return key, nil
}

// PrivateEncrypt encrypts chunk number index under the file key. The
// index fills the nonce, which guarantees nonce uniqueness within a file
// AND binds position: swapping two ciphertext chunks makes both fail
// authentication on decrypt.
func PrivateEncrypt(key [KeySize]byte, index uint64, plaintext []byte) ([]byte, error) {
	gcm, err := newGCM(key[:])
	if err != nil {
		return nil, err
	}
	return gcm.Seal(nil, privateNonce(index), plaintext, []byte(privateAAD)), nil
}

// PrivateDecrypt reverses PrivateEncrypt for the chunk at index.
func PrivateDecrypt(key [KeySize]byte, index uint64, ciphertext []byte) ([]byte, error) {
	gcm, err := newGCM(key[:])
	if err != nil {
		return nil, err
	}
	pt, err := gcm.Open(nil, privateNonce(index), ciphertext, []byte(privateAAD))
	if err != nil {
		return nil, fmt.Errorf("crypto: private decrypt chunk %d: %w", index, err)
	}
	return pt, nil
}

// SealBox encrypts pt under a caller-supplied 32-byte key with a
// deterministic nonce derived from (key, domain). Safe under the same
// argument as convergent mode: callers must guarantee a given key is
// used for one plaintext only (ours are content-derived, so identical
// key ⇒ identical plaintext). domain separates uses of the same key.
func SealBox(key [SecretSize]byte, domain string, pt []byte) ([]byte, error) {
	gcm, nonce, err := boxCipher(key, domain)
	if err != nil {
		return nil, err
	}
	return gcm.Seal(nil, nonce, pt, []byte(domain)), nil
}

// OpenBox reverses SealBox.
func OpenBox(key [SecretSize]byte, domain string, ct []byte) ([]byte, error) {
	gcm, nonce, err := boxCipher(key, domain)
	if err != nil {
		return nil, err
	}
	pt, err := gcm.Open(nil, nonce, ct, []byte(domain))
	if err != nil {
		return nil, fmt.Errorf("crypto: open box (%s): %w", domain, err)
	}
	return pt, nil
}

func boxCipher(key [SecretSize]byte, domain string) (cipher.AEAD, []byte, error) {
	k, err := hkdf.Key(sha256.New, key[:], nil, domain+"/key", KeySize)
	if err != nil {
		return nil, nil, err
	}
	nonce, err := hkdf.Key(sha256.New, key[:], nil, domain+"/nonce", nonceSize)
	if err != nil {
		return nil, nil, err
	}
	gcm, err := newGCM(k)
	return gcm, nonce, err
}

// DeriveKey derives a subordinate key one-way: knowing the child never
// reveals the parent. This is what makes graded capabilities possible —
// hand out the child, keep the parent.
func DeriveKey(parent [SecretSize]byte, domain string) [SecretSize]byte {
	out, err := hkdf.Key(sha256.New, parent[:], nil, domain, SecretSize)
	if err != nil {
		panic(err) // static parameters; cannot fail
	}
	var k [SecretSize]byte
	copy(k[:], out)
	return k
}

func privateNonce(index uint64) []byte {
	nonce := make([]byte, nonceSize)
	binary.BigEndian.PutUint64(nonce[nonceSize-8:], index)
	return nonce
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
