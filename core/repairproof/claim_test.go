package repairproof

import (
	"testing"

	"github.com/nerolabs/silt/ports"
)

// TestRepairClaim_RoundTrip: a claim survives marshal→unmarshal byte-for-byte in
// its fields, and canonical encoding is deterministic (same claim → same bytes).
func TestRepairClaim_RoundTrip(t *testing.T) {
	c := RepairClaim{
		Root:     ports.HashBytes([]byte("object-root")),
		Stripe:   3,
		ShardPos: 5,
		ShardID:  ports.HashBytes([]byte("repaired-shard")),
		Holder:   ports.HashBytes([]byte("fresh-holder-node")),
	}

	b, err := c.Marshal()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got, err := UnmarshalClaim(b)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got != c {
		t.Fatalf("round-trip mismatch:\n got %+v\nwant %+v", got, c)
	}

	// Canonical encoding is deterministic.
	b2, err := c.Marshal()
	if err != nil {
		t.Fatalf("marshal 2: %v", err)
	}
	if string(b) != string(b2) {
		t.Fatal("canonical CBOR must be deterministic for the same claim")
	}
}

// TestRepairClaim_RejectsGarbage: undecodable bytes are an error, not a
// zero-value claim silently accepted.
func TestRepairClaim_RejectsGarbage(t *testing.T) {
	if _, err := UnmarshalClaim([]byte{0xff, 0x00, 0x13, 0x37}); err == nil {
		t.Fatal("garbage bytes must fail to decode")
	}
}
