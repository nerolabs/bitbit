// Package bond is the identity-bound storage commitment that puts a
// PRICE ON IDENTITY. To hold consensus standing (core/credit Reputation),
// a node must maintain a large, per-identity blob it can be challenged on
// at any moment. Two Sybils are two DISTINCT multi-GB blobs that each
// occupy real disk, so N identities cost N×size of real storage — the
// missing Sybil cost the reputation-quorum chain has always assumed but
// never charged (threat-catalog B1/D3).
//
// ⚠️ As of 2026-08-04 this "N identities cost N×size" claim is DISPROVEN by the
// M0 red-team (F1/F3) — see the RED-TEAM note below: the real cost is ~size/128
// (→0 for small bonds). The paragraphs here describe the INTENDED design.
//
// IDENTITY BINDING (Gate 4b). The plot is sealed from a per-identity SECRET
// (derived from the node's signing key), not its public NodeID, so:
//   - only the identity's owner can generate its plot — an outsider cannot
//     precompute a victim's root to grief it; and
//   - a validator credits a given bond root to at most ONE identity
//     (core/credit's root-owner dedup), so a colluding operator cannot point
//     N identities at ONE shared plot: each identity needs its own distinct
//     plot (distinct secret ⇒ distinct root), restoring the N×size cost.
//
// Together these close the plot-amortisation gap (design §6): "strict binding,
// N plots for N identities." Note this is NOT a proof of CORRECT plotting
// (no PoRep/SNARK): a verifier still trusts the advertised root and only
// checks the prover can answer challenges on it — the dedup and the secret
// are what make sharing a root uneconomical and un-grief-able, respectively.
//
// The challenge shape mirrors the toy PoR in core/node/por.go, but points
// at an identity-bound bond instead of a shared file, and — crucially —
// the verifier checks the answer against ONLY the committed Merkle root it
// already holds. No ground-truth fetch: verification is O(log n), while
// the prover must hold the probed blocks to answer. An unpredictable
// nonce picks which blocks, so a prover must hold (nearly) all of them —
// cost = Size.
//
// ⚠️ RED-TEAM 2026-08-04 — THIS SCHEME IS BROKEN (F1/F2), fix pending the Sybil
// design turn. The claims in the next paragraph are FALSE against the shipped
// code and are kept only so the correction has something to point at:
//   - plotBlock derives block i from the 32-byte LEAVES of its parents, not the
//     parents' 4 KiB block BYTES (see plotBlock + leaves[i]=HashBytes(block_i)).
//     So a prover that stores only the leaves (32 B/block = 1/128 of the bond)
//     recomputes any probed block in one call and builds its Merkle proof —
//     passing Verify/VerifySpaceTime while holding 1/128 of what it advertises
//     (→ 0 resident bytes for bonds small enough to re-plot inside the VDF
//     window). "Store the S bytes" is NOT the rational strategy; storing the
//     leaves is.
//   - the VDF "time" half gates nothing: AnswerSpaceTime seeds the VDF from the
//     PUBLIC challenge seed, so a zero-resident prover runs the VDF, learns the
//     probed indices, then re-derives exactly those blocks. Releasing the space
//     does not forfeit the answer.
//
// The fix (design turn): make each block depend on the full parent BYTES (a
// memory-hard label / proven depth-robust graph), and bind the sampling
// challenge to a plot read BEFORE the VDF so releasing the space forfeits it.
// Report: docs/reviews/M0-REDTEAM-REPORT.md §1–§2.
//
// THE PLOT — the INTENDED design (Gate 4b), not yet achieved. The bond dataset
// is a SEQUENTIAL LABELING: block i is derived from the node's identity, its
// index, its immediate predecessor, and a few pseudo-random EARLIER blocks
// (a chain plus long-range parents — a directed acyclic graph). The INTENT is
// that recomputing a probed block forces recomputing its dependency subgraph so
// the rational strategy is to STORE the S bytes — but as the red-team note above
// shows, binding to leaves rather than bytes defeats this. Same identity ⇒ same
// plot, so an owner can regenerate on setup.
//
// HONESTLY LABELED — what this does and does not prove:
//   - It was INTENDED to deliver SPACE-hardness heuristically; the red-team
//     showed it does not (F1). It is NOT a formally depth-robust graph (DRG);
//     the fix is a proven-DRG labeling (Ateniese-style) and/or a memory-hard
//     label function over block BYTES. The TIME half — a VDF meant to bind a
//     fresh epoch challenge to non-parallelisable elapsed work — does not
//     currently bind possession (F2), because its input is public.
//   - No replication proof and no zero-knowledge: it proves "this identity
//     holds a distinct blob of this size," not "this is a unique replica of
//     user data." Elevating held REAL network content to standing (so the
//     Sybil cost and durability funding become one mechanism) is the intended
//     follow-up; the synthetic bond here is the cold-start.
package bond

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"

	"github.com/fxamacker/cbor/v2"

	"github.com/nerolabs/silt/core/manifest"
	"github.com/nerolabs/silt/core/vdf"
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
	Size   int64
	Root   ports.Hash
	blocks [][]byte
	leaves []ports.Hash
}

// Seal plots the identity-bound bond of ~size bytes from a per-identity
// secret (see the package doc: the secret binds the plot to its owner and
// makes each identity's plot distinct). Blocks are generated in order because
// each depends on earlier ones (see plotBlock), so this is the deliberately
// non-trivial "plotting" step; the owner then STORES the result to answer
// challenges cheaply. Same secret ⇒ same plot, so an owner can regenerate.
func Seal(secret []byte, size int64) *Commitment {
	n := NumBlocks(size)
	blocks := make([][]byte, n)
	leaves := make([]ports.Hash, n)
	for i := 0; i < n; i++ {
		b := plotBlock(secret, i, leaves) // reads leaves[0..i-1] already filled
		blocks[i] = b
		leaves[i] = ports.HashBytes(b)
	}
	return &Commitment{Size: size, Root: manifest.MerkleRoot(leaves), blocks: blocks, leaves: leaves}
}

// Blocks exposes the plot blocks so the owner can persist them (ports.
// PlotStore) and reload on restart instead of re-plotting (#93).
func (c *Commitment) Blocks() [][]byte { return c.blocks }

// Reconstruct rebuilds a Commitment from persisted plot blocks, RE-DERIVING
// the leaves and Merkle root from the bytes rather than trusting a stored
// root (B7). It errors if the block count doesn't match size or any block is
// the wrong length — a corrupt or stale plot the caller should discard and
// re-plot. The caller should additionally check the returned Root equals the
// root it persisted, catching silent on-disk corruption.
func Reconstruct(size int64, blocks [][]byte) (*Commitment, error) {
	n := NumBlocks(size)
	if len(blocks) != n {
		return nil, fmt.Errorf("bond: plot has %d blocks, want %d for size %d", len(blocks), n, size)
	}
	leaves := make([]ports.Hash, n)
	for i, b := range blocks {
		if len(b) != BlockSize {
			return nil, fmt.Errorf("bond: plot block %d is %d bytes, want %d", i, len(b), BlockSize)
		}
		leaves[i] = ports.HashBytes(b)
	}
	return &Commitment{Size: size, Root: manifest.MerkleRoot(leaves), blocks: blocks, leaves: leaves}, nil
}

// Answer is a prover's response to a challenge: the probed blocks plus
// their Merkle inclusion proofs against the committed root. For a
// space-TIME answer it also carries the VDF proof that bound the challenge
// to elapsed sequential work (empty for a plain space-only answer).
type Answer struct {
	Indices []int
	Blocks  [][]byte
	Proofs  []manifest.Proof
	// VDF proof (core/vdf) for a space-time answer: it attests that the
	// prover did VDFT sequential squarings over the challenge, and the probed
	// block indices are derived from VDFY — so the prover cannot know which
	// blocks to hold until the sequential work is done. Empty ⇒ space-only.
	VDFY  []byte `cbor:",omitempty"`
	VDFPi []byte `cbor:",omitempty"`
	VDFT  uint64 `cbor:",omitempty"`
}

// Answer builds the space-only response for nonce from held blocks. It
// returns false if the owner no longer holds a probed block (i.e. it cannot
// prove the bond it committed to).
func (c *Commitment) Answer(nonce uint64) (Answer, bool) {
	return c.answer(challengeIndices(c.Root, len(c.leaves), nonce))
}

// answer builds a response over the given block indices.
func (c *Commitment) answer(idxs []int) (Answer, bool) {
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

// AnswerSpaceTime is the proof-of-space-TIME response: it first runs the VDF
// for `delay` sequential squarings over the fresh challenge, then derives the
// probed block indices from the VDF output.
//
// ⚠️ RED-TEAM 2026-08-04 (F2): the intended guarantee below — "a prover cannot
// release the space and re-plot just in time" — is FALSE as shipped. The VDF is
// seeded from challengeSeed(c.Root, nonce), which is PUBLIC, so a zero-resident
// prover runs the VDF, learns the indices, then re-derives exactly those blocks
// (which, per Finding 1, it can rebuild from the leaves alone). The delay does
// not bind possession because nothing about the VDF requires reading the plot.
// Fix (design turn): seed the sampling from a value that requires reading the
// plot BEFORE the VDF. delay == 0 falls back to a space-only answer.
func (c *Commitment) AnswerSpaceTime(nonce uint64, p vdf.Params, delay uint64) (Answer, bool) {
	if delay == 0 {
		return c.Answer(nonce)
	}
	proof, err := vdf.Eval(p, challengeSeed(c.Root, nonce), delay)
	if err != nil {
		return Answer{}, false
	}
	a, ok := c.answer(challengeIndices(c.Root, len(c.leaves), vdfDerivedNonce(proof.Y)))
	if !ok {
		return Answer{}, false
	}
	a.VDFY, a.VDFPi, a.VDFT = proof.Y, proof.Pi, proof.T
	return a, true
}

// Verify checks a space-only answer against ONLY the committed root — no
// ground truth, no held blocks on the verifier's side. Passing requires the
// prover to have produced the exact probed blocks and valid inclusion
// proofs, which it cannot do without holding them.
func Verify(root ports.Hash, size int64, nonce uint64, a Answer) bool {
	return verifyAt(root, size, challengeIndices(root, NumBlocks(size), nonce), a)
}

// VerifySpaceTime checks a proof-of-space-time answer: the VDF must attest the
// required delay over the challenge (freshness + elapsed sequential work), and
// the blocks it derives — cheaply, without redoing the work — must be held.
// It stays O(log n) on the verifier: the whole point of the VDF is that
// checking it is fast even though producing it was slow.
func VerifySpaceTime(root ports.Hash, size int64, nonce uint64, a Answer, p vdf.Params, delay uint64) bool {
	if delay == 0 {
		return Verify(root, size, nonce, a)
	}
	if a.VDFT != delay {
		return false // must attest exactly the required amount of work
	}
	if !vdf.Verify(p, challengeSeed(root, nonce), vdf.Proof{Y: a.VDFY, Pi: a.VDFPi, T: a.VDFT}) {
		return false
	}
	want := challengeIndices(root, NumBlocks(size), vdfDerivedNonce(a.VDFY))
	return verifyAt(root, size, want, a)
}

// verifyAt checks an answer's blocks against the expected probed indices.
func verifyAt(root ports.Hash, size int64, want []int, a Answer) bool {
	n := NumBlocks(size)
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

// challengeSeed binds a VDF challenge to this bond and nonce, so a proof for
// one bond/epoch cannot be replayed for another.
func challengeSeed(root ports.Hash, nonce uint64) []byte {
	b := make([]byte, len(root)+8)
	copy(b, root[:])
	binary.BigEndian.PutUint64(b[len(root):], nonce)
	return b
}

// vdfDerivedNonce turns the VDF output into the block-sampling nonce, so the
// blocks a prover must hold are unknowable until the sequential work is done.
func vdfDerivedNonce(y []byte) uint64 {
	h := sha256.Sum256(append([]byte("silt/bond/st/v1"), y...))
	return binary.BigEndian.Uint64(h[:8])
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
// i mixes the per-identity secret, the index, its predecessor's leaf, and
// plotParents pseudo-random earlier leaves, then expands that label to
// BlockSize. The dependency on earlier blocks is what forces a prover to
// STORE the plot: recomputing block i on demand means recomputing its
// dependency subgraph, which the long-range parents make as costly as holding
// the plot outright. leaves must already hold the finalized leaves of blocks
// 0..i-1.
func plotBlock(secret []byte, i int, leaves []ports.Hash) []byte {
	h := sha256.New()
	h.Write([]byte(plotDomain))
	h.Write(secret)
	var ib [8]byte
	binary.BigEndian.PutUint64(ib[:], uint64(i))
	h.Write(ib[:])
	if i > 0 {
		h.Write(leaves[i-1][:]) // the chain: immediate predecessor
	} else {
		h.Write(make([]byte, len(ports.Hash{}))) // genesis: zero seed
	}
	for _, p := range parentIndices(secret, i) {
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
// [0, i) for block i, from (secret, i). Returns nil for block 0 (no
// predecessors). Repeats are harmless.
func parentIndices(secret []byte, i int) []int {
	if i == 0 {
		return nil
	}
	out := make([]int, plotParents)
	for j := 0; j < plotParents; j++ {
		h := sha256.New()
		h.Write([]byte(plotDomain + "/parent"))
		h.Write(secret)
		var b [16]byte
		binary.BigEndian.PutUint64(b[:8], uint64(i))
		binary.BigEndian.PutUint64(b[8:], uint64(j))
		h.Write(b[:])
		sum := h.Sum(nil)
		out[j] = int(binary.BigEndian.Uint64(sum[:8]) % uint64(i))
	}
	return out
}
