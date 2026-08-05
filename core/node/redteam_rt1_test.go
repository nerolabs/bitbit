package node

import (
	"bytes"
	"testing"

	"github.com/nerolabs/silt/core/por"
	"github.com/nerolabs/silt/ports"
)

// M0 hardening H1 / red-team RT-1, the IDENTITY-BINDING half (Invariant A): the
// PoR challenge is now bound to the prover (porProverSeed = H(base ‖ proverID)),
// so a data-less identity B cannot pass by RELAYING honest holder A's aggregated
// proof — A's proof was computed under A's seed, and B is graded under B's seed.
// Before H1 the auditor sent one shared seed to every provider of a leaf, so a
// single relayed (μ, σ) satisfied everyone's verify. See por.go porProverSeed.
func TestRedteamRT1_RelayedProofFailsUnderProverBinding(t *testing.T) {
	unitID := ports.HashBytes([]byte("chunk-x"))
	key, err := por.DeriveKey([]byte("layout-key-seed"), por.DefaultParams)
	if err != nil {
		t.Fatalf("derive key: %v", err)
	}
	// A real holder's shard bytes + tags.
	data := bytes.Repeat([]byte{0x5a, 0x13, 0x27}, por.DefaultParams.SectorsPerBlock*8)
	tags := key.Tags(unitID[:], data)
	blocks := len(tags)

	base := porChallengeSeed(42)
	A := ports.HashBytes([]byte("holder-A"))
	B := ports.HashBytes([]byte("sybil-B"))

	// A answers ITS OWN challenge (seed bound to A) — the honest, passing case.
	cA := porChallenge(porProverSeed(base, A), blocks, porSampleCount)
	proofA, err := por.Prove(por.DefaultParams, data, tags, cA)
	if err != nil {
		t.Fatalf("prove A: %v", err)
	}
	if !key.Verify(unitID[:], cA, proofA) {
		t.Fatal("setup: A's own proof must verify under A's seed")
	}

	// B RELAYS A's proof. Graded under B's seed (H(base ‖ B)), it must FAIL —
	// this is the relay attack, now denied.
	cB := porChallenge(porProverSeed(base, B), blocks, porSampleCount)
	if key.Verify(unitID[:], cB, proofA) {
		t.Fatal("RT-1 regression: A's proof verified under B's challenge — a data-less Sybil could relay it")
	}

	// Sanity: the binding is per-identity, not a blanket break — B *would* pass if
	// it actually held the bytes and answered its own seed (what an honest second
	// holder does). Standing is still separately denied to a bondless B (see
	// core/credit TestRedteamRT1_*).
	proofB, err := por.Prove(por.DefaultParams, data, tags, cB)
	if err != nil {
		t.Fatalf("prove B: %v", err)
	}
	if !key.Verify(unitID[:], cB, proofB) {
		t.Fatal("an honest holder answering its OWN seed must still verify")
	}
}
