package manifest

import (
	"bytes"
	"testing"

	"github.com/nerolabs/silt/core/crypto"
	"github.com/nerolabs/silt/ports"
)

// FuzzUnmarshal drives arbitrary bytes through the manifest decoder. A
// manifest arrives as reassembled chunk data — attacker-controlled bytes
// — so Unmarshal must return an error on anything malformed and never
// panic (Gate 1 / anti-persona #14). Any input that panics is a
// top-severity bug (tenets S1/S3).
func FuzzUnmarshal(f *testing.F) {
	// Seed with a valid manifest so the fuzzer starts from real CBOR
	// structure and mutates outward, plus a few degenerate inputs.
	valid := &Manifest{
		Version:   Version,
		Mode:      string(crypto.Convergent),
		ChunkSize: 1024,
		FileSize:  10,
		Chunks:    [][]byte{make([]byte, len(ports.Hash{}))},
		ChunkSecrets: [][]byte{
			make([]byte, crypto.SecretSize),
		},
	}
	if b, err := encMode.Marshal(valid); err == nil {
		f.Add(b)
	}
	f.Add([]byte{})
	f.Add([]byte{0xff})
	f.Add(bytes.Repeat([]byte{0xa1}, 64)) // nested CBOR maps

	f.Fuzz(func(t *testing.T, b []byte) {
		m, err := Unmarshal(b)
		if err != nil {
			return
		}
		// A manifest that decoded cleanly must round-trip: Marshal of the
		// accepted value re-parses. This keeps the fuzzer honest about the
		// "accepted" branch, not just the reject branch.
		if _, err := m.Marshal(); err != nil {
			t.Fatalf("accepted manifest fails to re-marshal: %v", err)
		}
	})
}
