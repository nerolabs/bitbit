package manifest

import (
	"encoding/binary"
	"strings"
	"testing"

	"github.com/nerolabs/silt/core/crypto"
	"github.com/nerolabs/silt/ports"
)

// A manifest that *declares* an out-of-bounds chunk-array count must be
// rejected as the array header is read — before the node allocates a slice
// against the attacker's number (Gate 1 / A6, #88). The crafted input is a
// handful of bytes: a CBOR map {5: <array declaring MaxChunks+1 elements>}
// with no elements actually supplied. If the bound were enforced only
// after decode, the decoder would try to grow a slice toward billions of
// entries first; instead it errors immediately.
func TestUnmarshalRejectsOverDeclaredChunkCount(t *testing.T) {
	var b []byte
	b = append(b, 0xa1)    // map, 1 pair
	b = append(b, 0x05)    // key 5 == Chunks
	b = append(b, 0x9a)    // array, 4-byte length follows
	hdr := make([]byte, 4) // declare MaxChunks+1 elements
	binary.BigEndian.PutUint32(hdr, uint32(MaxChunks+1))
	b = append(b, hdr...)
	// deliberately no array elements follow

	_, err := Unmarshal(b)
	if err == nil {
		t.Fatal("manifest declaring an over-bound chunk count was accepted")
	}
	// The decoder rejected on the declared count, not on running out of
	// bytes for a billion elements.
	if !strings.Contains(err.Error(), "exceeded max number of elements") {
		t.Fatalf("expected a max-elements rejection, got: %v", err)
	}
}

// A manifest declaring an oversize per-chunk frame size is rejected cleanly
// by validation (the count path is covered above; this is the "size" half).
func TestValidateRejectsOversizeChunkSize(t *testing.T) {
	m := &Manifest{
		Version:   Version,
		Mode:      string(crypto.Convergent),
		ChunkSize: MaxChunkSize + 1,
		FileSize:  1,
		Chunks:    [][]byte{make([]byte, len(ports.Hash{}))},
		ChunkSecrets: [][]byte{
			make([]byte, crypto.SecretSize),
		},
	}
	err := m.Validate()
	if err == nil || !strings.Contains(err.Error(), "exceeds max") {
		t.Fatalf("oversize chunk size not rejected, got: %v", err)
	}
	// And it must be unmarshalable-proof end to end: even bytes that encode
	// this can't slip past Unmarshal. Encode with the raw (non-validating)
	// encoder to simulate a hostile producer.
	raw, encErr := encMode.Marshal(m)
	if encErr != nil {
		t.Fatalf("marshal for test setup: %v", encErr)
	}
	if _, err := Unmarshal(raw); err == nil {
		t.Fatal("Unmarshal accepted a manifest with an oversize chunk size")
	}
}

// OpenLayout returns before the full manifest validation, so it must
// enforce the declared bounds itself. A hostile producer can seal a layout
// with an oversize chunk size (Seal itself validates, so we box the bytes
// directly); OpenLayout must still refuse it.
func TestOpenLayoutRejectsOversizeChunkSize(t *testing.T) {
	lk, _ := keys()
	eb, err := encMode.Marshal(Layout{
		Version:   Version,
		ChunkSize: MaxChunkSize + 1,
		Chunks:    [][]byte{make([]byte, len(ports.Hash{}))},
		Box:       []byte{},
	})
	if err != nil {
		t.Fatalf("marshal layout: %v", err)
	}
	blob, err := crypto.SealBox(lk, layoutDomain, eb)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if _, err := OpenLayout(blob, lk); err == nil {
		t.Fatal("OpenLayout accepted a layout with an oversize chunk size")
	}
}

// A normal manifest still round-trips — the bounds don't reject legitimate
// values.
func TestBoundsAllowNormalManifest(t *testing.T) {
	m := &Manifest{
		Version:      Version,
		Mode:         string(crypto.Convergent),
		ChunkSize:    64 << 10,
		FileSize:     100,
		Chunks:       [][]byte{make([]byte, len(ports.Hash{}))},
		ChunkSecrets: [][]byte{make([]byte, crypto.SecretSize)},
	}
	raw, err := m.Marshal()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if _, err := Unmarshal(raw); err != nil {
		t.Fatalf("a normal manifest was rejected by the bounds: %v", err)
	}
}
