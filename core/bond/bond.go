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
// HONESTLY LABELED: this is proof-of-SPACE-lite, not Filecoin PoRep.
//   - It forces STORAGE over recomputation only insofar as sealing is
//     expensive relative to a disk read. The placeholder sealBlock below
//     is iterated SHA-256 — cheap, NOT memory-hard. A real deployment MUST
//     swap it for a memory-hard function (argon2 / scrypt / Chia-style
//     plotting) or the storage↔compute tradeoff lets an attacker recompute
//     blocks on demand instead of storing them.
//   - No replication proof and no zero-knowledge: it proves "this identity
//     holds a distinct blob of this size," not "this is a unique replica
//     of user data." Elevating held REAL network content to standing (so
//     the Sybil cost and durability funding become one mechanism) is the
//     intended follow-up; the synthetic bond here is the cold-start.
package bond

import (
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

// Seal generates the identity-bound bond of ~size bytes for id. Same id ⇒
// same bond, so an owner can regenerate on setup — but must then STORE it
// to answer challenges cheaply, which is the whole point.
func Seal(id ports.NodeID, size int64) *Commitment {
	n := NumBlocks(size)
	blocks := make([][]byte, n)
	leaves := make([]ports.Hash, n)
	for i := 0; i < n; i++ {
		b := sealBlock(id, i)
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

// sealBlock is the identity-bound block generator. PLACEHOLDER: iterated
// SHA-256 over (id ‖ index), expanded to BlockSize — deterministic and
// identity-bound, but NOT memory-hard. Swap for argon2/scrypt/plotting
// before this is a real Sybil cost (see package doc).
func sealBlock(id ports.NodeID, index int) []byte {
	seed := make([]byte, len(id)+8)
	copy(seed, id[:])
	binary.BigEndian.PutUint64(seed[len(id):], uint64(index))
	block := make([]byte, BlockSize)
	h := ports.HashBytes(seed)
	for off := 0; off < BlockSize; off += len(h) {
		copy(block[off:], h[:])
		h = ports.HashBytes(h[:]) // chain the hash to fill the block
	}
	return block
}
