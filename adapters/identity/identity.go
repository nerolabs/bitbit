// Package identity makes a node's ID and its cryptographic identity the
// same thing: NodeID = SHA-256(Ed25519 public key). Connections are TLS
// with self-signed certificates, verified not by any CA but by hashing
// the presented public key and comparing it to the NodeID you meant to
// talk to — the identity IS the key. Consequences worth savoring:
//
//   - No accounts, no certificate authority, no enrollment. Generating
//     a keypair is joining.
//   - Peer addresses ("ID@host:port") are self-authenticating: if the
//     TLS handshake's key doesn't hash to the ID you dialed, you are
//     talking to an impostor and the connection dies.
//   - Reputation (audit history, serving record) attaches to the key.
//     Shedding a bad reputation means shedding the identity — and
//     starting from zero trust again.
package identity

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	mrand "math/rand"
	"os"
	"time"

	"github.com/nerolabs/silt/ports"
)

type Identity struct {
	priv ed25519.PrivateKey
}

// Generate draws a fresh identity from r (crypto/rand in production).
func Generate(r io.Reader) (*Identity, error) {
	_, priv, err := ed25519.GenerateKey(r)
	if err != nil {
		return nil, fmt.Errorf("identity: %w", err)
	}
	return &Identity{priv: priv}, nil
}

// FromSeed derives a deterministic identity — for scripted demos and
// tests only; a predictable key is no identity at all.
func FromSeed(seed int64) *Identity {
	var b [ed25519.SeedSize]byte
	mrand.New(mrand.NewSource(seed)).Read(b[:])
	return &Identity{priv: ed25519.NewKeyFromSeed(b[:])}
}

func (id *Identity) Public() ed25519.PublicKey {
	return id.priv.Public().(ed25519.PublicKey)
}

// Signer exposes the private key for chain signing (M12): proposals and
// attestations are signed by the same key whose hash is the NodeID.
func (id *Identity) Signer() ed25519.PrivateKey { return id.priv }

// NodeID is the identity's network name: the hash of its public key.
func (id *Identity) NodeID() ports.NodeID {
	return sha256.Sum256(id.Public())
}

const pemType = "BITBIT ED25519 PRIVATE KEY"

// Save writes the private key as PEM, owner-readable only.
func (id *Identity) Save(path string) error {
	der, err := x509.MarshalPKCS8PrivateKey(id.priv)
	if err != nil {
		return err
	}
	return os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: pemType, Bytes: der}), 0o600)
}

func Load(path string) (*Identity, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(raw)
	if block == nil || block.Type != pemType {
		return nil, fmt.Errorf("identity: %s is not a %s PEM", path, pemType)
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	priv, ok := key.(ed25519.PrivateKey)
	if !ok {
		return nil, errors.New("identity: not an Ed25519 key")
	}
	return &Identity{priv: priv}, nil
}

// LoadOrCreate returns the identity at path, minting and saving one on
// first run.
func LoadOrCreate(path string) (*Identity, error) {
	if id, err := Load(path); err == nil {
		return id, nil
	}
	id, err := Generate(rand.Reader)
	if err != nil {
		return nil, err
	}
	return id, id.Save(path)
}

// Certificate builds the identity's self-signed TLS certificate. The
// x509 fields are ceremony; the only thing anyone verifies is the
// public key inside.
func (id *Identity) Certificate() (tls.Certificate, error) {
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(20, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, id.Public(), id.priv)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: id.priv}, nil
}

// PeerID extracts the authenticated NodeID from a completed TLS
// handshake: the hash of the peer certificate's Ed25519 key.
func PeerID(state tls.ConnectionState) (ports.NodeID, error) {
	if len(state.PeerCertificates) == 0 {
		return ports.NodeID{}, errors.New("identity: peer presented no certificate")
	}
	pub, ok := state.PeerCertificates[0].PublicKey.(ed25519.PublicKey)
	if !ok {
		return ports.NodeID{}, errors.New("identity: peer key is not Ed25519")
	}
	return sha256.Sum256(pub), nil
}

// VerifyExpected returns a VerifyPeerCertificate hook pinning the
// remote end to a specific NodeID. InsecureSkipVerify is set alongside
// it because we are REPLACING PKI verification with pubkey pinning,
// not skipping verification.
func VerifyExpected(expect ports.NodeID) func([][]byte, [][]*x509.Certificate) error {
	return func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
		if len(rawCerts) == 0 {
			return errors.New("identity: no certificate presented")
		}
		cert, err := x509.ParseCertificate(rawCerts[0])
		if err != nil {
			return err
		}
		pub, ok := cert.PublicKey.(ed25519.PublicKey)
		if !ok {
			return errors.New("identity: peer key is not Ed25519")
		}
		if sha256.Sum256(pub) != expect {
			return fmt.Errorf("identity: peer key hashes to %x, expected %s — impostor or stale address book",
				sha256.Sum256(pub), expect)
		}
		return nil
	}
}
