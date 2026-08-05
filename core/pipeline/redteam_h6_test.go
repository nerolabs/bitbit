package pipeline_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"testing"

	"github.com/nerolabs/silt/adapters/memstore"
	"github.com/nerolabs/silt/core/crypto"
	"github.com/nerolabs/silt/core/link"
	"github.com/nerolabs/silt/core/pipeline"
	"github.com/nerolabs/silt/core/registry"
	"github.com/nerolabs/silt/ports"
)

// M0 hardening H6 (Memo 02): convergent encryption is a membership/existence
// oracle for guessable data — because the key is derived from the plaintext, the
// whole content address is a deterministic function of the plaintext, so anyone
// who GUESSES it can compute the root and look it up to confirm you stored it (the
// confirmation attack). H6 flips the DEFAULT publish mode to `private` (a random
// per-file key), which breaks that deterministic mapping. This test proves the
// oracle works under convergent and is DEFEATED under the new private default.
func TestRedteamH6_PrivateDefaultDefeatsExistenceOracle(t *testing.T) {
	ctx := context.Background()
	plaintext := bytes.Repeat([]byte("guessable-secret-"), 512) // a few chunks' worth

	add := func(mode crypto.Mode, reg ports.Registry) link.Handle {
		t.Helper()
		opts := pipeline.Options{ChunkSize: 1024, Mode: mode}
		if mode == crypto.Private {
			opts.Rand = rand.Reader
		}
		h, err := pipeline.Add(ctx, memstore.New(), reg, bytes.NewReader(plaintext), opts)
		if err != nil {
			t.Fatalf("add(%s): %v", mode, err)
		}
		return h
	}

	// The attacker holds a guessed plaintext and computes the address it WOULD have
	// under convergent encryption — the value it probes the network for.
	attackerRoot := add(crypto.Convergent, registry.New()).Root

	// Convergent publish: the honest root equals the attacker's computed root, and a
	// registry probe HITS — the oracle H6 closes by default (kept as documentation).
	cReg := registry.New()
	cRoot := add(crypto.Convergent, cReg).Root
	if cRoot != attackerRoot {
		t.Fatal("convergent addressing must be deterministic from plaintext (this is the oracle)")
	}
	if _, ok, _ := cReg.Lookup(ctx, attackerRoot); !ok {
		t.Fatal("convergent: a guessed plaintext confirms existence — the oracle exists (as documented)")
	}

	// Private publish (the NEW default): the honest root is NOT derivable from the
	// plaintext, so the attacker's probe MISSES — the oracle is defeated.
	pReg := registry.New()
	pRoot := add(crypto.Private, pReg).Root
	if pRoot == attackerRoot {
		t.Fatal("H6: a private root must not be a deterministic function of the plaintext")
	}
	if _, ok, _ := pReg.Lookup(ctx, attackerRoot); ok {
		t.Fatal("H6 regression: under the private default a guessed plaintext must NOT confirm existence")
	}

	// Two independent private uploads of the SAME content don't even collide with
	// each other — there is no cross-upload dedup to probe.
	pRoot2 := add(crypto.Private, registry.New()).Root
	if pRoot == pRoot2 {
		t.Fatal("H6: two private uploads of identical content must produce different roots (no dedup oracle)")
	}
}
