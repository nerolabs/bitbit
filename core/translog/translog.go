// Package translog is an append-only Merkle transparency log — an RFC 6962
// (Certificate Transparency) accumulator, adopted not invented (B8). It is the
// M0-honest core of pluralistic takedown (D-TAKEDOWN / H9, issue #180): every
// honored revocation is entered into the log, and the log offers two proofs that
// make a takedown AUDITABLE and NON-SILENT:
//
//   - INCLUSION — "revocation R is entry i of the log at size N" (a Merkle audit
//     path), so anyone can prove a specific takedown was recorded; and
//   - CONSISTENCY — "the log at size M is a prefix of the log at size N" (nothing
//     was removed or altered, only appended), so an operator cannot quietly rewrite
//     history — a silently-dropped or back-dated revocation breaks the consistency
//     proof.
//
// This is a plain binary-Merkle accumulator with 0x00/0x01 domain separation
// (leaf vs interior), independent of any ZK / PIR machinery: the ZK non-globality
// PREDICATE and PIR-routed probes that quantify how non-global a takedown is are a
// post-M0 layer on top of this log. What ships now is the tamper-evident,
// publicly-auditable record itself.
package translog

import (
	"crypto/sha256"
	"errors"

	"github.com/nerolabs/silt/ports"
)

// ErrRange is returned for an out-of-range index or size (e.g. proving inclusion of
// a leaf the log does not have at that size, or a consistency proof to a size larger
// than the log).
var ErrRange = errors.New("translog: index or size out of range")

// Log is an append-only sequence of entries with RFC-6962 Merkle proofs. It is not
// safe for concurrent use (silt runs on one event loop); a caller that shares it
// serializes access.
type Log struct {
	entries []ports.Hash
}

// New returns an empty log.
func New() *Log { return &Log{} }

// Append adds entry to the end of the log and returns the new size. Entries are the
// content-addressed identifiers being logged (e.g. a revoked root hash).
func (l *Log) Append(entry ports.Hash) int {
	l.entries = append(l.entries, entry)
	return len(l.entries)
}

// Size is the number of entries currently in the log.
func (l *Log) Size() int { return len(l.entries) }

// Root is the Merkle Tree Head over all entries — the log's current commitment.
func (l *Log) Root() ports.Hash { return mth(l.entries) }

// RootAt is the Merkle Tree Head over the first `size` entries (the historical root
// a consistency proof is checked against). size must be 0..Size().
func (l *Log) RootAt(size int) (ports.Hash, error) {
	if size < 0 || size > len(l.entries) {
		return ports.Hash{}, ErrRange
	}
	return mth(l.entries[:size]), nil
}

// InclusionProof returns the Merkle audit path proving that entry `index` is in the
// log of the given `size`. Verify it with VerifyInclusion.
func (l *Log) InclusionProof(index, size int) ([]ports.Hash, error) {
	if size < 0 || size > len(l.entries) || index < 0 || index >= size {
		return nil, ErrRange
	}
	return path(index, l.entries[:size]), nil
}

// ConsistencyProof returns the proof that the log at size m is a prefix of the log
// at size n (m ≤ n ≤ Size). An empty proof means the trees are identical (m == n)
// or m == 0 (the empty tree is a prefix of everything). Verify with VerifyConsistency.
func (l *Log) ConsistencyProof(m, n int) ([]ports.Hash, error) {
	if m < 0 || n < m || n > len(l.entries) {
		return nil, ErrRange
	}
	if m == 0 || m == n {
		return nil, nil
	}
	return subproof(m, l.entries[:n], true), nil
}

// --- RFC 6962 tree hashing (prover side) ---

// leafHash is MTH of a single entry: HASH(0x00 || entry).
func leafHash(entry ports.Hash) ports.Hash {
	return ports.Hash(sha256.Sum256(append([]byte{0x00}, entry[:]...)))
}

// nodeHash is an interior node: HASH(0x01 || left || right).
func nodeHash(left, right ports.Hash) ports.Hash {
	b := make([]byte, 0, 1+len(left)+len(right))
	b = append(b, 0x01)
	b = append(b, left[:]...)
	b = append(b, right[:]...)
	return ports.Hash(sha256.Sum256(b))
}

// mth is the Merkle Tree Head over entries (RFC 6962 §2.1): the empty hash for the
// empty list, the leaf hash for one entry, else HASH(0x01 || MTH(left) || MTH(right))
// split at the largest power of two below n.
func mth(entries []ports.Hash) ports.Hash {
	n := len(entries)
	switch {
	case n == 0:
		return ports.Hash(sha256.Sum256(nil))
	case n == 1:
		return leafHash(entries[0])
	}
	k := split(n)
	return nodeHash(mth(entries[:k]), mth(entries[k:]))
}

// split is the largest power of two strictly less than n (n ≥ 2): k < n ≤ 2k.
func split(n int) int {
	k := 1
	for k<<1 < n {
		k <<= 1
	}
	return k
}

// path is the RFC 6962 §2.1.1 audit path for leaf m in the tree over entries.
func path(m int, entries []ports.Hash) []ports.Hash {
	n := len(entries)
	if n <= 1 {
		return nil
	}
	k := split(n)
	if m < k {
		return append(path(m, entries[:k]), mth(entries[k:]))
	}
	return append(path(m-k, entries[k:]), mth(entries[:k]))
}

// subproof is the RFC 6962 §2.1.2 consistency subproof for prefix size m of entries.
func subproof(m int, entries []ports.Hash, b bool) []ports.Hash {
	n := len(entries)
	if m == n {
		if b {
			return nil
		}
		return []ports.Hash{mth(entries)}
	}
	k := split(n)
	if m <= k {
		return append(subproof(m, entries[:k], b), mth(entries[k:]))
	}
	return append(subproof(m-k, entries[k:], false), mth(entries[:k]))
}

// --- RFC 6962 verification (verifier side; stateless) ---

// VerifyInclusion checks that `entry` is leaf `index` of a log of `size` entries
// whose Merkle Tree Head is `root`, given the audit `proof` (RFC 6962 §2.1.1). It
// recomputes the root from the leaf and the proof and compares.
func VerifyInclusion(entry ports.Hash, index, size int, root ports.Hash, proof []ports.Hash) bool {
	if index < 0 || index >= size {
		return false
	}
	fn, sn := index, size-1
	r := leafHash(entry)
	for _, p := range proof {
		if sn == 0 {
			return false // proof longer than the tree is tall
		}
		if fn&1 == 1 || fn == sn {
			r = nodeHash(p, r)
			if fn&1 == 0 {
				for fn&1 == 0 && fn != 0 {
					fn >>= 1
					sn >>= 1
				}
			}
		} else {
			r = nodeHash(r, p)
		}
		fn >>= 1
		sn >>= 1
	}
	return sn == 0 && r == root
}

// VerifyConsistency checks that a log with Merkle Tree Head `oldRoot` at size `m` is
// a prefix of a log with head `newRoot` at size `n`, given the `proof` (RFC 6962
// §2.1.2). An empty proof is accepted exactly when it must be (m == 0, or m == n and
// the roots already match) — the degenerate prefixes.
func VerifyConsistency(oldRoot ports.Hash, m int, newRoot ports.Hash, n int, proof []ports.Hash) bool {
	if m < 0 || n < m {
		return false
	}
	if m == 0 {
		return len(proof) == 0 // the empty tree is a prefix of every tree
	}
	if m == n {
		return len(proof) == 0 && oldRoot == newRoot
	}
	// RFC 6962 §2.1.2: if m is an exact power of two, the first node (MTH of the
	// old tree's left part) is implicit — prepend oldRoot.
	path := proof
	if isPow2(m) {
		path = append([]ports.Hash{oldRoot}, proof...)
	}
	if len(path) == 0 {
		return false
	}
	fn, sn := m-1, n-1
	for fn&1 == 1 {
		fn >>= 1
		sn >>= 1
	}
	fr, sr := path[0], path[0]
	for _, c := range path[1:] {
		if sn == 0 {
			return false
		}
		if fn&1 == 1 || fn == sn {
			fr = nodeHash(c, fr)
			sr = nodeHash(c, sr)
			if fn&1 == 0 {
				for fn&1 == 0 && fn != 0 {
					fn >>= 1
					sn >>= 1
				}
			}
		} else {
			sr = nodeHash(sr, c)
		}
		fn >>= 1
		sn >>= 1
	}
	return sn == 0 && fr == oldRoot && sr == newRoot
}

func isPow2(x int) bool { return x > 0 && x&(x-1) == 0 }
