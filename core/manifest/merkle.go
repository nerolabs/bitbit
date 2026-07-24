// Merkle tree over the chunk hashes of a file, following the RFC 6962
// (Certificate Transparency) construction. The root is the file's global
// identity; an inclusion proof shows "chunk i belongs to root R" with
// O(log n) hashes — the seam the future proof-of-retrieval credit system
// will hang off. See docs/math/01-merkle-trees.md.
//
// Two details worth noticing:
//
//   - Domain separation: leaves are hashed with a 0x00 prefix and
//     interior nodes with 0x01, so an interior node can never be
//     reinterpreted as a leaf (or vice versa) to forge a proof.
//   - Unbalanced trees: for n leaves the split point is the largest power
//     of two strictly less than n. This handles any leaf count without
//     padding or duplicating leaves, and makes proofs unambiguous.
package manifest

import (
	"crypto/sha256"
	"fmt"

	"github.com/nerolabs/bitbit/ports"
)

// MerkleRoot computes the root over an ordered list of leaf values (for
// us: chunk IDs). The empty tree has a defined root (SHA-256 of nothing)
// so a zero-byte file still gets a valid identity.
func MerkleRoot(leaves []ports.Hash) ports.Hash {
	return merkleTreeHash(leaves)
}

func leafHash(leaf ports.Hash) ports.Hash {
	h := sha256.New()
	h.Write([]byte{0x00})
	h.Write(leaf[:])
	var out ports.Hash
	h.Sum(out[:0])
	return out
}

func nodeHash(left, right ports.Hash) ports.Hash {
	h := sha256.New()
	h.Write([]byte{0x01})
	h.Write(left[:])
	h.Write(right[:])
	var out ports.Hash
	h.Sum(out[:0])
	return out
}

func merkleTreeHash(leaves []ports.Hash) ports.Hash {
	switch n := len(leaves); n {
	case 0:
		return sha256.Sum256(nil)
	case 1:
		return leafHash(leaves[0])
	default:
		k := largestPowerOfTwoBelow(n)
		return nodeHash(merkleTreeHash(leaves[:k]), merkleTreeHash(leaves[k:]))
	}
}

// largestPowerOfTwoBelow returns the largest power of two strictly less
// than n (n must be >= 2).
func largestPowerOfTwoBelow(n int) int {
	k := 1
	for k*2 < n {
		k *= 2
	}
	return k
}

// Proof is an inclusion proof: the sibling subtree hashes on the path
// from leaf Index up to the root of a tree with Total leaves, ordered
// bottom-up as consumed by verification.
type Proof struct {
	Index int
	Total int
	Path  []ports.Hash
}

// Prove builds the inclusion proof for leaves[index].
func Prove(leaves []ports.Hash, index int) (Proof, error) {
	if index < 0 || index >= len(leaves) {
		return Proof{}, fmt.Errorf("merkle: index %d out of range for %d leaves", index, len(leaves))
	}
	return Proof{Index: index, Total: len(leaves), Path: auditPath(index, leaves)}, nil
}

func auditPath(m int, leaves []ports.Hash) []ports.Hash {
	if len(leaves) == 1 {
		return nil
	}
	k := largestPowerOfTwoBelow(len(leaves))
	if m < k {
		return append(auditPath(m, leaves[:k]), merkleTreeHash(leaves[k:]))
	}
	return append(auditPath(m-k, leaves[k:]), merkleTreeHash(leaves[:k]))
}

// VerifyProof checks that leaf sits at p.Index in the tree of p.Total
// leaves whose root is root. It recomputes the root from the leaf and
// the proof path alone — no other knowledge of the tree required.
func VerifyProof(root ports.Hash, leaf ports.Hash, p Proof) bool {
	if p.Index < 0 || p.Total <= 0 || p.Index >= p.Total {
		return false
	}
	got, err := rootFromPath(p.Index, p.Total, leafHash(leaf), p.Path)
	if err != nil {
		return false
	}
	return got == root
}

// rootFromPath mirrors auditPath's recursion in reverse: the last path
// element is the sibling at the top split, everything before it proves
// the leaf within the half the leaf lives in.
func rootFromPath(m, n int, cur ports.Hash, path []ports.Hash) (ports.Hash, error) {
	if n == 1 {
		if len(path) != 0 {
			return ports.Hash{}, fmt.Errorf("merkle: proof too long")
		}
		return cur, nil
	}
	if len(path) == 0 {
		return ports.Hash{}, fmt.Errorf("merkle: proof too short")
	}
	sibling := path[len(path)-1]
	rest := path[:len(path)-1]
	k := largestPowerOfTwoBelow(n)
	if m < k {
		left, err := rootFromPath(m, k, cur, rest)
		if err != nil {
			return ports.Hash{}, err
		}
		return nodeHash(left, sibling), nil
	}
	right, err := rootFromPath(m-k, n-k, cur, rest)
	if err != nil {
		return ports.Hash{}, err
	}
	return nodeHash(sibling, right), nil
}
