// Package bond is the identity-bound storage commitment that puts a
// PRICE ON IDENTITY. To hold consensus standing (core/credit Reputation),
// a node must maintain a large, per-identity blob it can be challenged on
// at any moment. Two Sybils are two DISTINCT multi-GB blobs that each
// occupy real disk, so N identities cost N×size of real storage — the
// missing Sybil cost the reputation-quorum chain has always assumed but
// never charged (threat-catalog B1/D3).
//
// The challenge shape mirrors the toy PoR in core/node/por.go, but points
// at an identity-bound bond instead of a shared file, and — crucially —
// the verifier checks the answer against ONLY the committed Merkle root it
// already holds. No ground-truth fetch: verification is O(log n), while
// the prover must hold the probed blocks to answer. An unpredictable
// nonce picks which blocks, so a prover must hold (nearly) all of them —
// cost = Size.
//
// THE PLOT — why storing beats recomputing (Gate 4b). The bond dataset is a
// SEQUENTIAL LABELING: block i is derived from the node's identity, its
// index, its immediate predecessor, and a few pseudo-random EARLIER blocks
// (a chain plus long-range parents — a directed acyclic graph). Because a
// block depends on earlier ones, recomputing a single probed block on demand
// forces recomputing its whole dependency subgraph back toward block 0; and
// the pseudo-random long-range parents defeat cheap checkpointing (you would
// have to store enough checkpoints to cover random reaches, i.e. store the
// plot anyway). So the rational strategy is to STORE the S bytes — which is
// exactly the space we are charging for. Same identity ⇒ same plot, so an
// owner can regenerate on setup, but must then hold it to answer cheaply.
//
// HONESTLY LABELED — what this does and does not prove:
//   - It delivers SPACE-hardness heuristically: the labeling makes recompute
//     cost scale with dependency depth, so holding the plot beats recomputing
//     it. It is NOT yet a formally depth-robust graph (DRG); a proven-DRG
//     labeling (Ateniese-style) and/or a memory-hard label function are the
//     hardening path, and the TIME half — a VDF binding a fresh epoch
//     challenge to non-parallelisable elapsed work (core/vdf, already built)
//     so the challenge can't be precomputed and the space must be held ACROSS
//     time — is wired in the next 4b step.
//   - No replication proof and no zero-knowledge: it proves "this identity
//     holds a distinct blob of this size," not "this is a unique replica of
//     user data." Elevating held REAL network content to standing (so the
//     Sybil cost and durability funding become one mechanism) is the intended
//     follow-up; the synthetic bond here is the cold-start.
package bond

import (
	"crypto/sha256"
	"encoding/binary"

	"github.com/fxamacker/cbor/v2"

	"github.com/nerolabs/silt/core/manifest"
	"github.com/nerolabs/silt/ports"
)

// EncodeAnswer / DecodeAnswer serialize a challenge answer for the wire
// (MsgBondReply carries it in Message.Data). CBOR is the codec the chain
// already uses.
func EncodeAnswer(a Answer) ([]byte, error) { return cbor.Marshal(a) }

func DecodeAnswer(b []byte) (Answer, error) {
	var a Answer
	err := cbor.Unmarshal(b, &a)
	return a, err
}

const (
	// BlockSize is the granularity of a bond: the unit a challenge probes
	// and a prover must have on disk to answer.
	BlockSize = 4 << 10
	// Samples is how many independent blocks one challenge probes. A prover
	// missing a fraction f of its bond slips through with probability
	// (1-f)^Samples, so 20 samples makes even a 10%-short bond fail ~88% of
	// the time and a 30%-short bond fail ~99.9%.
	Samples = 20
)

// NumBlocks is how many BlockSize blocks a bond of size bytes holds. It is
// derived from the PUBLIC size, so prover and verifier agree without
// exchanging it.
func NumBlocks(size int64) int {
	if size <= 0 {
		return 1
	}
	return int((size + BlockSize - 1) / BlockSize)
}

// Commitment is a sealed, identity-bound bond held on disk by its owner.
// Root is public (published once, cheap to gossip); blocks/leaves are the
// cost the owner carries to keep answering.
type Commitment struct {
	ID     ports.NodeID
	Size   int64
	Root   ports.Hash
	blocks [][]byte
	leaves []ports.Hash
}

// Seal plots the identity-bound bond of ~size bytes for id. Blocks are
// generated in order because each depends on earlier ones (see plotBlock),
// so this is the deliberately non-trivial "plotting" step; the owner then
// STORES the result to answer challenges cheaply, which is the whole point.
// Same id ⇒ same plot.
func Seal(id ports.NodeID, size int64) *Commitment {
	n := NumBlocks(size)
	blocks := make([][]byte, n)
	leaves := make([]ports.Hash, n)
	for i := 0; i < n; i++ {
		b := plotBlock(id, i, leaves) // reads leaves[0..i-1] already filled
		blocks[i] = b
		leaves[i] = ports.HashBytes(b)
	}
	return &Commitment{ID: id, Size: size, Root: manifest.MerkleRoot(leaves), blocks: blocks, leaves: leaves}
}

// Answer is a prover's response to a challenge: the probed blocks plus
// their Merkle inclusion proofs against the committed root.
type Answer struct {
	Indices []int
	Blocks  [][]byte
	Proofs  []manifest.Proof
}

// Answer builds the response for nonce from held blocks. It returns false
// if the owner no longer holds a probed block (i.e. it cannot prove the
// bond it committed to).
func (c *Commitment) Answer(nonce uint64) (Answer, bool) {
	idxs := challengeIndices(c.Root, len(c.leaves), nonce)
	a := Answer{Indices: idxs}
	for _, i := range idxs {
		if i >= len(c.blocks) || c.blocks[i] == nil {
			return Answer{}, false
		}
		p, err := manifest.Prove(c.leaves, i)
		if err != nil {
			return Answer{}, false
		}
		a.Blocks = append(a.Blocks, c.blocks[i])
		a.Proofs = append(a.Proofs, p)
	}
	return a, true
}

// Verify checks an answer against ONLY the committed root — no ground
// truth, no held blocks on the verifier's side. Passing requires the
// prover to have produced the exact probed blocks and valid inclusion
// proofs, which it cannot do without holding them.
func Verify(root ports.Hash, size int64, nonce uint64, a Answer) bool {
	n := NumBlocks(size)
	want := challengeIndices(root, n, nonce)
	if len(a.Indices) != len(want) || len(a.Blocks) != len(want) || len(a.Proofs) != len(want) {
		return false
	}
	for j := range want {
		if a.Indices[j] != want[j] {
			return false
		}
		p := a.Proofs[j]
		if p.Index != want[j] || p.Total != n {
			return false
		}
		if !manifest.VerifyProof(root, ports.HashBytes(a.Blocks[j]), p) {
			return false
		}
	}
	return true
}

// challengeIndices derives which blocks a nonce probes, from the PUBLIC
// (root, nBlocks, nonce) so prover and verifier compute them identically.
func challengeIndices(root ports.Hash, nBlocks int, nonce uint64) []int {
	idx := make([]int, Samples)
	buf := make([]byte, len(root)+16)
	copy(buf, root[:])
	for j := 0; j < Samples; j++ {
		binary.BigEndian.PutUint64(buf[len(root):], nonce)
		binary.BigEndian.PutUint64(buf[len(root)+8:], uint64(j))
		h := ports.HashBytes(buf)
		idx[j] = int(binary.BigEndian.Uint64(h[:8]) % uint64(nBlocks))
	}
	return idx
}

const (
	plotDomain = "silt/bond/plot/v1"
	// plotParents is how many pseudo-random EARLIER blocks each block depends
	// on, on top of its immediate predecessor. More parents raise recompute
	// cost and defeat checkpointing harder, at a small plotting-time cost.
	plotParents = 3
)

// plotBlock is the identity-bound, dependency-chained block generator. Block
// i mixes the identity, the index, its predecessor's leaf, and plotParents
// pseudo-random earlier leaves, then expands that label to BlockSize. The
// dependency on earlier blocks is what forces a prover to STORE the plot:
// recomputing block i on demand means recomputing its dependency subgraph,
// which the long-range parents make as costly as holding the plot outright.
// leaves must already hold the finalized leaves of blocks 0..i-1.
func plotBlock(id ports.NodeID, i int, leaves []ports.Hash) []byte {
	h := sha256.New()
	h.Write([]byte(plotDomain))
	h.Write(id[:])
	var ib [8]byte
	binary.BigEndian.PutUint64(ib[:], uint64(i))
	h.Write(ib[:])
	if i > 0 {
		h.Write(leaves[i-1][:]) // the chain: immediate predecessor
	} else {
		h.Write(make([]byte, len(ports.Hash{}))) // genesis: zero seed
	}
	for _, p := range parentIndices(id, i) {
		h.Write(leaves[p][:]) // long-range dependencies
	}
	label := h.Sum(nil)

	// Expand the label to a full block by chaining SHA-256. Filling from the
	// label (not the raw seed) binds the block's every byte to its parents.
	block := make([]byte, BlockSize)
	cur := label
	for off := 0; off < BlockSize; off += len(cur) {
		copy(block[off:], cur)
		next := sha256.Sum256(cur)
		cur = next[:]
	}
	return block
}

// parentIndices derives plotParents pseudo-random dependency indices in
// [0, i) for block i, from the PUBLIC (id, i) so a verifier could recompute
// the graph. Returns nil for block 0 (no predecessors). Repeats are harmless.
func parentIndices(id ports.NodeID, i int) []int {
	if i == 0 {
		return nil
	}
	out := make([]int, plotParents)
	for j := 0; j < plotParents; j++ {
		h := sha256.New()
		h.Write([]byte(plotDomain + "/parent"))
		h.Write(id[:])
		var b [16]byte
		binary.BigEndian.PutUint64(b[:8], uint64(i))
		binary.BigEndian.PutUint64(b[8:], uint64(j))
		h.Write(b[:])
		sum := h.Sum(nil)
		out[j] = int(binary.BigEndian.Uint64(sum[:8]) % uint64(i))
	}
	return out
}
