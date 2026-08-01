package bond

import (
	"testing"

	"github.com/nerolabs/silt/ports"
)

func nodeID(b byte) ports.NodeID { return ports.HashBytes([]byte{b}) }

// A node that HOLDS its bond can answer any challenge, and the verifier
// confirms it against only the committed root.
func TestHeldBondAnswersItsOwnChallenge(t *testing.T) {
	c := Seal(nodeID(1), 1<<20) // 1 MiB
	for _, nonce := range []uint64{1, 42, 9999} {
		ans, ok := c.Answer(nonce)
		if !ok {
			t.Fatalf("holder could not answer nonce %d", nonce)
		}
		if !Verify(c.Root, c.Size, nonce, ans) {
			t.Fatalf("valid held answer for nonce %d rejected", nonce)
		}
	}
}

// V3, test-the-adversary: a prover that does NOT hold the bytes cannot
// satisfy the root. Corrupting a probed block, or answering with another
// identity's bond, both fail — so passing genuinely proves held storage.
func TestForgedOrUnheldBondFails(t *testing.T) {
	c := Seal(nodeID(1), 1<<20)
	ans, ok := c.Answer(42)
	if !ok {
		t.Fatal("setup: holder should answer")
	}

	// Corrupt one probed block: the inclusion proof no longer binds to the
	// committed root, i.e. the prover no longer has the real bytes.
	bad := Answer{Indices: ans.Indices, Proofs: ans.Proofs, Blocks: append([][]byte(nil), ans.Blocks...)}
	bad.Blocks[0] = append([]byte(nil), ans.Blocks[0]...)
	bad.Blocks[0][0] ^= 0xff
	if Verify(c.Root, c.Size, 42, bad) {
		t.Fatal("forged block accepted — storage not actually proven")
	}

	// Another identity's bond does not answer for this root (identity-bound).
	other, ok := Seal(nodeID(2), 1<<20).Answer(42)
	if !ok {
		t.Fatal("setup: other holder should answer its own bond")
	}
	if Verify(c.Root, c.Size, 42, other) {
		t.Fatal("another identity's bond satisfied this root — bonds are not identity-bound")
	}
}
