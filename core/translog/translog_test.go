package translog

import (
	"testing"

	"github.com/nerolabs/silt/ports"
)

func entry(i int) ports.Hash { return ports.HashBytes([]byte{byte(i), byte(i >> 8), 0xAB}) }

func build(n int) *Log {
	l := New()
	for i := 0; i < n; i++ {
		l.Append(entry(i))
	}
	return l
}

// TestInclusionAllLeavesAllSizes cross-checks prover and verifier: for every tree
// size and every leaf, the audit path the log produces must reconstruct the log's
// root — and a wrong entry or index must not.
func TestInclusionAllLeavesAllSizes(t *testing.T) {
	const N = 33
	l := build(N)
	for size := 1; size <= N; size++ {
		root, err := l.RootAt(size)
		if err != nil {
			t.Fatalf("RootAt(%d): %v", size, err)
		}
		for i := 0; i < size; i++ {
			proof, err := l.InclusionProof(i, size)
			if err != nil {
				t.Fatalf("InclusionProof(%d,%d): %v", i, size, err)
			}
			if !VerifyInclusion(entry(i), i, size, root, proof) {
				t.Fatalf("inclusion of leaf %d in size %d did not verify", i, size)
			}
			// A different entry at the same position must fail.
			if VerifyInclusion(entry(i+1000), i, size, root, proof) {
				t.Fatalf("inclusion verified for the WRONG entry at leaf %d, size %d", i, size)
			}
			// The proof for leaf i must not verify leaf i at a different index.
			if i+1 < size && VerifyInclusion(entry(i), i+1, size, root, proof) {
				t.Fatalf("leaf %d's proof verified at index %d", i, i+1)
			}
		}
	}
}

// TestConsistencyAllPairs cross-checks: for every prefix m ≤ n, the consistency
// proof must bind the historical root at m to the current root at n — proving the
// log only grew — and a tampered historical root must not verify.
func TestConsistencyAllPairs(t *testing.T) {
	const N = 33
	l := build(N)
	for n := 0; n <= N; n++ {
		newRoot, _ := l.RootAt(n)
		for m := 0; m <= n; m++ {
			oldRoot, _ := l.RootAt(m)
			proof, err := l.ConsistencyProof(m, n)
			if err != nil {
				t.Fatalf("ConsistencyProof(%d,%d): %v", m, n, err)
			}
			if !VerifyConsistency(oldRoot, m, newRoot, n, proof) {
				t.Fatalf("consistency %d→%d did not verify", m, n)
			}
			// A wrong "old" root must not pass as a prefix (rewriting history).
			if m > 0 && m < n && VerifyConsistency(entry(999), m, newRoot, n, proof) {
				t.Fatalf("consistency %d→%d verified a FORGED old root", m, n)
			}
		}
	}
}

// TestAppendOnlyCatchesRewrite: an operator that alters a past entry (a silently
// dropped/back-dated revocation) produces a different history whose old root no
// longer proves consistent with the tampered log — the whole point of the log.
func TestAppendOnlyCatchesRewrite(t *testing.T) {
	honest := build(8)
	root4, _ := honest.RootAt(4)
	root8 := honest.Root()

	// A tampered log agreeing on the first 3 entries but rewriting entry 3.
	tampered := New()
	for i := 0; i < 8; i++ {
		if i == 3 {
			tampered.Append(entry(3000)) // rewritten history
			continue
		}
		tampered.Append(entry(i))
	}
	// The honest size-4 root cannot be shown consistent with the tampered size-8 log:
	// any consistency proof the tampered log offers is against ITS root, and the
	// honest root4 is not a prefix of it.
	tRoot8 := tampered.Root()
	proof, _ := tampered.ConsistencyProof(4, 8)
	if VerifyConsistency(root4, 4, tRoot8, 8, proof) {
		t.Fatal("a tampered log proved consistency with the honest historical root")
	}
	// Sanity: the honest log's own proof still verifies, and the roots differ.
	hproof, _ := honest.ConsistencyProof(4, 8)
	if !VerifyConsistency(root4, 4, root8, 8, hproof) {
		t.Fatal("honest consistency 4→8 failed")
	}
	if tRoot8 == root8 {
		t.Fatal("tampering did not change the root")
	}
}

// TestDegenerateProofs: the empty tree is a prefix of everything, identical trees
// need no proof, and out-of-range asks error.
func TestDegenerateProofs(t *testing.T) {
	l := build(5)
	r5 := l.Root()
	empty, _ := l.RootAt(0)

	if p, _ := l.ConsistencyProof(0, 5); !VerifyConsistency(empty, 0, r5, 5, p) {
		t.Fatal("empty tree must be a prefix of every tree")
	}
	if p, _ := l.ConsistencyProof(5, 5); !VerifyConsistency(r5, 5, r5, 5, p) {
		t.Fatal("identical trees must verify with an empty proof")
	}
	if _, err := l.InclusionProof(5, 5); err != ErrRange {
		t.Fatal("inclusion of a nonexistent leaf must be ErrRange")
	}
	if _, err := l.ConsistencyProof(3, 6); err != ErrRange {
		t.Fatal("consistency beyond the log size must be ErrRange")
	}
	// Empty log root is the hash of the empty string; a single append changes it.
	el := New()
	before := el.Root()
	el.Append(entry(1))
	if el.Root() == before {
		t.Fatal("appending to the empty log did not change the root")
	}
}
