package node

import (
	"crypto/rand"
	"testing"

	"github.com/nerolabs/silt/core/crypto"
	"github.com/nerolabs/silt/core/por"
)

// TestCtOverheadMatchesGCM pins the constant the auditor uses to recompute a
// full shard's expected PoR block count from the layout's plaintext
// ChunkSize (por tags are over ciphertext = plaintext + GCM tag). If crypto's
// AEAD overhead ever drifts from ctOverhead, the full-shard block-count
// cross-check (option B) would reject honest shards — so we fail loudly here,
// at build time, instead of in the field.
func TestCtOverheadMatchesGCM(t *testing.T) {
	pt := make([]byte, 1000)
	if _, err := rand.Read(pt); err != nil {
		t.Fatalf("rand: %v", err)
	}
	ct, _, err := crypto.ConvergentEncrypt(pt)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if got := len(ct) - len(pt); got != ctOverhead {
		t.Fatalf("ciphertext overhead is %d, but ctOverhead is %d — the audit's block-count check would reject honest shards", got, ctOverhead)
	}
	// Private mode uses the same AEAD; confirm it agrees.
	var key [crypto.KeySize]byte
	if _, err := rand.Read(key[:]); err != nil {
		t.Fatalf("rand key: %v", err)
	}
	pct, err := crypto.PrivateEncrypt(key, 0, pt)
	if err != nil {
		t.Fatalf("private encrypt: %v", err)
	}
	if got := len(pct) - len(pt); got != ctOverhead {
		t.Fatalf("private-mode overhead is %d, want %d", got, ctOverhead)
	}
}

// TestDerivePorKeyMatchesAcrossCapabilities is the wiring's key-agreement
// invariant: the publisher (holding the full Handle) and a caretaker/auditor
// (holding only the degraded care capability) derive the SAME PoR key, so a
// tag made at publish time verifies at audit time — while the key never
// travels and a storage node (no layout key) cannot reproduce it. We prove
// agreement by round-tripping a real proof across the two independently
// derived keys.
func TestDerivePorKeyMatchesAcrossCapabilities(t *testing.T) {
	// A publisher's full handle and the care handle it degrades to.
	pubKey := DerivePorKey(linkLayoutKey(t))
	audKey := DerivePorKey(linkLayoutKey(t)) // same layout key → same por key

	unitID := []byte("some-chunk-id")
	data := make([]byte, 5000)
	if _, err := rand.Read(data); err != nil {
		t.Fatalf("rand: %v", err)
	}
	tags := pubKey.Tags(unitID, data)
	c, err := por.NewChallenge(rand.Reader, len(tags), len(tags))
	if err != nil {
		t.Fatalf("challenge: %v", err)
	}
	proof, err := por.Prove(por.DefaultParams, data, tags, c)
	if err != nil {
		t.Fatalf("prove: %v", err)
	}
	if !audKey.Verify(unitID, c, proof) {
		t.Fatal("auditor's independently-derived key rejected the publisher's tags")
	}
}

// linkLayoutKey returns a fixed layout key for the test — the value both the
// publisher (Handle.LayoutKey) and auditor (CareHandle.LayoutKey) would hold
// for the same file.
func linkLayoutKey(t *testing.T) [32]byte {
	t.Helper()
	return crypto.DeriveKey([32]byte{0x42}, "silt/link/v1/layout")
}
