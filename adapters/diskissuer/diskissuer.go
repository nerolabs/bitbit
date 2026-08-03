// Package diskissuer persists a validator's publish-token issuer RSA key
// (core/blindtoken) so a restart REUSES it instead of minting a fresh one. The
// issuer key is load-bearing for privacy AND liveness: peers cache its public
// half to verify the token signatures it blind-signed, and outstanding tokens
// were signed under it — regenerate on every restart and those tokens become
// unverifiable and every cached peer key goes stale (#93 / §3d). A validator's
// issuer identity must survive a restart just as its bond plot does.
package diskissuer

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// keySize matches the daemon's issuer key. RSA-2048 is the token-signing key,
// not the VDF trust anchor — a per-validator secret it generates itself.
const keySize = 2048

// Store roots issuer-key persistence at a directory (one key per daemon).
type Store struct{ path string }

// Open prepares a store at dir/issuer.key, creating dir if needed.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("diskissuer: %w", err)
	}
	return &Store{path: filepath.Join(dir, "issuer.key")}, nil
}

// Save writes the private key (PKCS#1 DER) atomically with owner-only
// permissions — it is a secret, so a temp file + rename avoids a torn write and
// 0600 keeps it off other users.
func (s *Store) Save(key *rsa.PrivateKey) error {
	der := x509.MarshalPKCS1PrivateKey(key)
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".tmp-issuer-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(der); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.path)
}

// Load returns the persisted key. ok is false (nil error) when none exists yet;
// a corrupt/foreign file is a real error so the caller doesn't silently mint a
// new issuer identity over a damaged one.
func (s *Store) Load() (key *rsa.PrivateKey, ok bool, err error) {
	der, rerr := os.ReadFile(s.path)
	if rerr != nil {
		if os.IsNotExist(rerr) {
			return nil, false, nil
		}
		return nil, false, rerr
	}
	k, perr := x509.ParsePKCS1PrivateKey(der)
	if perr != nil {
		return nil, false, fmt.Errorf("diskissuer: corrupt issuer key: %w", perr)
	}
	return k, true, nil
}

// LoadOrCreate returns the persisted issuer key, generating and saving a fresh
// one only if none exists — so the first run mints an identity and every
// restart keeps it. rng is injected (nil uses crypto/rand) to keep it testable.
func (s *Store) LoadOrCreate(rng io.Reader) (*rsa.PrivateKey, error) {
	if k, ok, err := s.Load(); err != nil {
		return nil, err
	} else if ok {
		return k, nil
	}
	if rng == nil {
		rng = rand.Reader
	}
	k, err := rsa.GenerateKey(rng, keySize)
	if err != nil {
		return nil, err
	}
	if err := s.Save(k); err != nil {
		return nil, err
	}
	return k, nil
}
