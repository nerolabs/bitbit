package tcpnet

import (
	"bytes"
	"testing"

	"github.com/fxamacker/cbor/v2"

	"github.com/nerolabs/silt/ports"
)

// TestPorFieldsSurviveWire is the #65-class guard: the wire codec is
// hand-rolled (toWire/fromWire map field-by-field), so a new field is
// SILENTLY DROPPED over real TCP until it is added there — a unit test on
// the port struct alone would never notice. This round-trips a message
// carrying the full PoR surface (challenge + proof + per-shard tags on the
// StorageProof) through encode → CBOR → decode and asserts every field
// arrives intact, so an audit that depends on them can't fail invisibly in
// the field.
func TestPorFieldsSurviveWire(t *testing.T) {
	// A challenge (auditor → prover).
	challenge := ports.Message{
		Kind:     ports.MsgChallenge,
		RID:      7,
		ChunkID:  ports.ChunkID{0xab, 0xcd},
		PorSeed:  bytes.Repeat([]byte{0x5a}, 32),
		PorCount: 128,
	}
	// A reply (prover → auditor): Merkle proof carrying PoR tags, plus the
	// aggregated PoR response.
	reply := ports.Message{
		Kind:  ports.MsgChallengeReply,
		RID:   7,
		Found: true,
		Proof: &ports.StorageProof{
			Root:    ports.Hash{0x11},
			Index:   2,
			Total:   6,
			Path:    []ports.Hash{{0x22}, {0x33}},
			Column:  3,
			PorTags: [][]byte{bytes.Repeat([]byte{1}, 32), bytes.Repeat([]byte{2}, 32)},
		},
		PorMu:     [][]byte{bytes.Repeat([]byte{3}, 32), bytes.Repeat([]byte{4}, 32)},
		PorSigma:  bytes.Repeat([]byte{5}, 32),
		PorBlocks: 2,
	}

	for _, want := range []ports.Message{challenge, reply} {
		b, err := encMode.Marshal(envelope{Msg: toWire(want)})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var env envelope
		if err := cbor.Unmarshal(b, &env); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		got := fromWire(env.Msg)

		if !bytes.Equal(got.PorSeed, want.PorSeed) {
			t.Errorf("PorSeed lost over the wire: got %x want %x", got.PorSeed, want.PorSeed)
		}
		if got.PorCount != want.PorCount {
			t.Errorf("PorCount lost: got %d want %d", got.PorCount, want.PorCount)
		}
		if got.PorBlocks != want.PorBlocks {
			t.Errorf("PorBlocks lost: got %d want %d", got.PorBlocks, want.PorBlocks)
		}
		if !bytes.Equal(got.PorSigma, want.PorSigma) {
			t.Errorf("PorSigma lost: got %x want %x", got.PorSigma, want.PorSigma)
		}
		if !equalChunks(got.PorMu, want.PorMu) {
			t.Errorf("PorMu lost: got %v want %v", got.PorMu, want.PorMu)
		}
		if want.Proof != nil {
			if got.Proof == nil {
				t.Fatal("Proof dropped entirely over the wire")
			}
			if !equalChunks(got.Proof.PorTags, want.Proof.PorTags) {
				t.Errorf("Proof.PorTags lost: got %v want %v", got.Proof.PorTags, want.Proof.PorTags)
			}
		}
	}
}

func equalChunks(a, b [][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !bytes.Equal(a[i], b[i]) {
			return false
		}
	}
	return true
}
