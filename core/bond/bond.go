// Package bond is the identity-bound storage commitment that puts a
// PRICE ON IDENTITY. To hold consensus standing (core/credit Reputation),
// a node must maintain a large, per-identity blob it can be challenged on
// at any moment. Two Sybils are two DISTINCT multi-GB blobs that each
// occupy real disk, so N identities cost N×size of real storage — the
// missing Sybil cost the reputation-quorum chain has always assumed but
// never charged (threat-catalog B1/D3).
//
// IDENTITY BINDING (Gate 4b). The plot is sealed from a per-identity SECRET
// (derived from the node's signing key), not its public NodeID, so:
//   - only the identity's owner can generate its plot — an outsider cannot
//     precompute a victim's root to grief it; and
//   - a validator credits a given bond root to at most ONE identity
//     (core/credit's root-owner dedup), so a colluding operator cannot point
//     N identities at ONE shared plot: each identity needs its own distinct
//     plot (distinct secret ⇒ distinct root).
//
// Note this is NOT a proof of CORRECT plotting (no PoRep/SNARK): a verifier
// still trusts the advertised root and only checks the prover can answer
// challenges on it. The COST that makes a short or shared plot uneconomical
// lives in the labeling below, not in the dedup — the dedup is only a
// same-root tiebreak, and the secret is what makes a plot un-grief-able.
//
// The challenge shape mirrors the toy PoR in core/node/por.go, but points
// at an identity-bound bond instead of a shared file, and — crucially —
// the verifier checks the answer against ONLY the committed Merkle root it
// already holds. No ground-truth fetch: verification is O(log n), while
// the prover must hold the probed blocks to answer. An unpredictable
// nonce picks which blocks, so a prover must hold (nearly) all of them —
// cost = Size.
//
// SPACE-HARDNESS — the plot binds BYTES over a depth-robust graph (M0 Sybil
// design turn, fixes red-team F1/F2; docs/design/m0-sybil-bond.md). Each block
// i is a memory-hard label derived from the per-identity secret, the index, and
// the FULL BYTES of its predecessor and its DRSample parents (a proven
// depth-robust graph, Alwen–Blocki–Harsha CCS'17). Because a block depends on
// its parents' 4 KiB bytes — not their 32-byte leaves — a prover cannot store
// only the leaves and recompute on demand: reconstructing block i requires the
// parents' bytes, which require theirs, and depth-robustness makes the pebbling
// cost of recomputing any block Ω(n). So the rational strategy is to STORE the
// S bytes; the charged size equals the resident footprint (closing the earlier
// 1/128 leaves-only gap). Verify never recomputes a block, so it stays O(log n).
//
// SPACE-TIME — the VDF is bound to a plot READ before it runs (fixes F2). The
// per-epoch challenge first samples one plot block (seedIndex), and the VDF is
// seeded from that block's bytes (challengeSeedST): a prover that has released
// the space cannot produce the seed without first re-deriving the block, which
// is the Ω(n) memory-hard recompute above. Tuned so re-plot ≫ one epoch, this
// makes release-and-replay uneconomical — the sequential VDF work then binds
// FRESH elapsed time on top of proven possession. The prover carries the seed
// block plus its inclusion proof so the verifier can recompute the seed and
// check the VDF, all still O(log n).
//
// HONESTLY LABELED — what this does and does not prove:
//   - It delivers SPACE-hardness over a proven depth-robust graph and binds the
//     TIME half to possession; the constant (honest cost vs. Sybil-farm cost,
//     and re-plot ≫ epoch) is the external red-team's target (design §6).
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
		b := plotBlock(secret, i, blocks) // reads blocks[0..i-1] already filled
		blocks[i] = b
		leaves[i] = ports.HashBytes(b)
	}
	return &Commitment{Size: size, Root: manifest.MerkleRoot(leaves), blocks: blocks, leaves: leaves}
}

// Blocks exposes the plot blocks so the owner can persist them (ports.
// PlotStore) and reload on restart instead of re-plotting (#93).
func (c *Commitment) Blocks() [][]byte { return c.blocks }

// ReleaseBlocks drops the resident plot bytes, keeping only the commitment
// (Root, Size) and the 32-byte leaves — the state of an F1/F2 adversary that
// pledged the space, advertised the bond, then FREED the bytes to save disk,
// intending to recompute on demand. Under the byte-binding + read-bound-VDF plot
// (M0 Sybil fix) it can no longer answer: AnswerSpaceTime needs the seed block
// and the sampled blocks it no longer holds, and it cannot cheaply recompute
// them (that is the whole point of the depth-robust labeling). So a released
// commitment fails a live audit — the wire-level consequence the sim asserts.
func (c *Commitment) ReleaseBlocks() { c.blocks = nil }

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
	// SeedBlock + SeedProof bind the VDF to a plot READ done BEFORE the VDF ran
	// (space-time only): the block at seedIndex(root,nonce), with its inclusion
	// proof, whose bytes seed the VDF (challengeSeedST). The verifier recomputes
	// the seed index, checks the proof, and re-derives the seed — so a prover
	// that released the space cannot produce the seed without the Ω(n) recompute.
	// Empty ⇒ space-only.
	SeedBlock []byte         `cbor:",omitempty"`
	SeedProof manifest.Proof `cbor:",omitempty"`
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

// AnswerSpaceTime is the proof-of-space-TIME response. It binds the VDF to a
// plot READ done before the VDF runs (M0 F2 fix): it first reads the block at
// seedIndex(root, nonce) from the plot, seeds the VDF from that block's bytes,
// runs `delay` sequential squarings, then derives the probed block indices from
// the VDF output. Because the seed requires a plot block — and re-deriving that
// block on demand is the Ω(n) depth-robust recompute — a prover that released
// the space cannot cheaply produce the seed, so releasing the space forfeits the
// answer. delay == 0 falls back to a space-only answer.
func (c *Commitment) AnswerSpaceTime(nonce uint64, p vdf.Params, delay uint64) (Answer, bool) {
	if delay == 0 {
		return c.Answer(nonce)
	}
	// Read the seed block (must be possessed BEFORE the VDF) and prove it.
	si := seedIndex(c.Root, len(c.leaves), nonce)
	if si >= len(c.blocks) || c.blocks[si] == nil {
		return Answer{}, false
	}
	seedProof, err := manifest.Prove(c.leaves, si)
	if err != nil {
		return Answer{}, false
	}
	seedBlock := c.blocks[si]
	proof, err := vdf.Eval(p, challengeSeedST(c.Root, nonce, seedBlock), delay)
	if err != nil {
		return Answer{}, false
	}
	a, ok := c.answer(challengeIndices(c.Root, len(c.leaves), vdfDerivedNonce(proof.Y)))
	if !ok {
		return Answer{}, false
	}
	a.SeedBlock, a.SeedProof = seedBlock, seedProof
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
	n := NumBlocks(size)
	// The VDF must be seeded from the plot block at seedIndex, proven held: a
	// prover that released the space cannot present it (F2 fix). Recompute the
	// seed index and check the inclusion proof before trusting the seed block.
	si := seedIndex(root, n, nonce)
	if a.SeedProof.Index != si || a.SeedProof.Total != n {
		return false
	}
	if !manifest.VerifyProof(root, ports.HashBytes(a.SeedBlock), a.SeedProof) {
		return false
	}
	if !vdf.Verify(p, challengeSeedST(root, nonce, a.SeedBlock), vdf.Proof{Y: a.VDFY, Pi: a.VDFPi, T: a.VDFT}) {
		return false
	}
	want := challengeIndices(root, n, vdfDerivedNonce(a.VDFY))
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

// seedIndex picks the single plot block whose bytes seed the space-time VDF,
// from the PUBLIC (root, nBlocks, nonce). Possession of this block is required
// to start the VDF (see challengeSeedST), which is what binds the "time" half
// to held space. Domain-separated from challengeIndices so the seed block and
// the sampled blocks are chosen independently.
func seedIndex(root ports.Hash, nBlocks int, nonce uint64) int {
	h := sha256.New()
	h.Write([]byte("silt/bond/st/v2/seed"))
	h.Write(root[:])
	var nb [8]byte
	binary.BigEndian.PutUint64(nb[:], nonce)
	h.Write(nb[:])
	sum := h.Sum(nil)
	return int(binary.BigEndian.Uint64(sum[:8]) % uint64(nBlocks))
}

// challengeSeedST binds the VDF challenge to this bond, nonce, AND the bytes of
// the seed block — so the VDF cannot even be started without reading the plot
// (F2 fix). A proof for one bond/epoch cannot be replayed for another, and a
// zero-resident prover cannot produce the seed without the Ω(n) recompute.
func challengeSeedST(root ports.Hash, nonce uint64, seedBlock []byte) []byte {
	h := sha256.New()
	h.Write([]byte("silt/bond/st/v2/vdfseed"))
	h.Write(root[:])
	var nb [8]byte
	binary.BigEndian.PutUint64(nb[:], nonce)
	h.Write(nb[:])
	h.Write(seedBlock)
	return h.Sum(nil)
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
	// plotDomain is bumped to v2 with the byte-binding + DRSample labeling (M0
	// Sybil fix). A v1 plot on disk re-plots rather than reloading (the disk
	// format version guards this) because its blocks are the old, leaves-only
	// labeling the red-team broke.
	plotDomain = "silt/bond/plot/v2"
	// plotParents is how many DRSample long-range parents each block depends on,
	// on top of its immediate predecessor. DRSample with a chain already yields a
	// depth-robust graph at indegree 2; extra parents strengthen the pebbling
	// bound at a small plotting-time cost.
	plotParents = 3
)

// plotBlock is the identity-bound, byte-binding, depth-robust block generator.
// Block i mixes the per-identity secret, the index, and the FULL BYTES of its
// immediate predecessor and its DRSample parents, then expands that label to
// BlockSize. Binding to the parents' bytes (not their 32-byte leaves) is what
// forces a prover to STORE the plot: recomputing block i on demand requires the
// parents' bytes, recursively, and the depth-robust graph makes that pebbling
// cost Ω(n). blocks must already hold the finalized bytes of blocks 0..i-1.
func plotBlock(secret []byte, i int, blocks [][]byte) []byte {
	h := sha256.New()
	h.Write([]byte(plotDomain))
	h.Write(secret)
	var ib [8]byte
	binary.BigEndian.PutUint64(ib[:], uint64(i))
	h.Write(ib[:])
	if i > 0 {
		h.Write(blocks[i-1]) // the chain: immediate predecessor's full bytes
	} else {
		h.Write(make([]byte, BlockSize)) // genesis: zero block
	}
	for _, p := range parentIndices(secret, i) {
		h.Write(blocks[p]) // long-range dependencies, full bytes
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

// parentIndices derives plotParents long-range dependency indices in [0, i)
// for block i using DRSample (Alwen–Blocki–Harsha, "Practical Graphs for
// Optimal Side-Channel Resistant Proofs of Work", CCS'17): each parent's
// distance from i is drawn log-uniformly — pick a bucket g in [1, ⌊log2 i⌋],
// then an offset uniformly in (2^(g-1), 2^g] — so short and long edges are both
// well represented, which is what makes the graph provably depth-robust (unlike
// the old flat-uniform choice). Deterministic from (secret, i); returns nil for
// block 0. Repeats are harmless.
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
		out[j] = drSampleParent(i, binary.BigEndian.Uint64(sum[:8]), binary.BigEndian.Uint64(sum[8:16]))
	}
	return out
}

// drSampleParent returns an earlier block index for block i using the DRSample
// distance distribution. r1 picks the bucket (a power-of-two distance band);
// r2 picks the offset within it. The result is always in [0, i).
func drSampleParent(i int, r1, r2 uint64) int {
	// bucket g ∈ [1, floor(log2(i))]; distance ∈ (2^(g-1), 2^g], clamped to < i.
	maxg := 0
	for (1 << (maxg + 1)) <= i {
		maxg++
	}
	if maxg < 1 {
		maxg = 1 // i == 1: only distance 1 is possible
	}
	g := 1 + int(r1%uint64(maxg)) // in [1, maxg]
	lo := 1 << (g - 1)            // 2^(g-1)
	hi := 1 << g                  // 2^g
	if hi > i {
		hi = i
	}
	span := hi - lo
	if span < 1 {
		span = 1
	}
	dist := lo + int(r2%uint64(span)) + 1 // in (2^(g-1), 2^g], i.e. [lo+1, hi]
	if dist > i {
		dist = i
	}
	return i - dist
}
