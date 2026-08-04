// Package bond is the identity-bound storage commitment that puts a
// PRICE ON IDENTITY. To hold consensus standing (core/credit Reputation),
// a node must maintain a large, per-identity blob it can be challenged on
// at any moment. Two Sybils are two DISTINCT multi-GB blobs that each
// occupy real disk, so N identities cost N×size of real storage — the
// missing Sybil cost the reputation-quorum chain has always assumed but
// never charged (threat-catalog B1/D3).
//
// PROOF OF SPACE — the plot is a VERIFIED graph-labeling proof-of-space (M0
// Sybil design turn G2, docs/design/m0-sybil-rebind.md). The plot is sealed
// from a PUBLIC, identity- and size-bound seed
//
//	seed = H("silt/bond/plot/v3" ‖ pk ‖ n)          // pk = validator ed25519 key, n = NumBlocks(size)
//
// folded into BOTH the block labels (plotBlock) and the DRSample parent draws
// (parentIndices). Because the seed folds in n, a PREFIX of an n-block plot is
// NOT a valid m-block plot — the byte-identical-prefix Sybil (G2) is dead at the
// data layer. Because the seed folds in pk, a plot sealed for one identity
// produces labels that fail recomputation under any other identity's key.
//
// The seed is public precisely so the VERIFIER can recompute a label without
// holding the plot: the space-time answer opens k challenged interior nodes,
// each with its predecessor and its DRSample parents (Merkle-proven), and the
// verifier recomputes plotBlock(seed, v, parents) from the opened parent BYTES
// and requires it to equal the opened node (verifyLabels). This turns "valid
// block" into a RECOMPUTABLE PUBLIC PREDICATE, so reused or arbitrary bytes are
// rejectable — the depth-robust labeling now buys real soundness, not just
// honest-prover cost. Identity and size are CHECKED properties of the plot, not
// claimed ones, so N standings require N plots (invariant B1 restored). k is a
// per-network knob (Answer/Verify take it; 0 resolves to DefaultLabelSamples),
// with soundness error ≤ (1-ε)^k against an ε-short prover (design §3).
//
// SPACE-HARDNESS — each block i is a memory-hard label over a proven depth-robust
// graph (DRSample, Alwen–Blocki–Harsha CCS'17): it mixes the seed, the index, and
// the FULL BYTES of its immediate predecessor and its DRSample parents (not their
// 32-byte leaves). Recomputing block i on demand requires the parents' bytes,
// recursively, and depth-robustness makes that pebbling cost Ω(n) — so the
// rational strategy is to STORE the S bytes; the charged size equals the resident
// footprint. Merkle possession sampling (Samples) still catches a prover MISSING
// blocks; the labeling opens (k) catch a prover holding arbitrary/short/reused
// bytes — the two are complementary (design §3.1), not substitutes.
//
// SPACE-TIME — the VDF is bound to a plot READ before it runs (fixes F2). The
// per-epoch challenge first samples one plot block (seedIndex), and the VDF is
// seeded from that block's bytes (challengeSeedST): a prover that has released
// the space cannot produce the seed without first re-deriving the block, which
// is the Ω(n) memory-hard recompute above. Tuned so re-plot ≫ one epoch, this
// makes release-and-replay uneconomical — the sequential VDF work then binds
// FRESH elapsed time on top of proven possession. The VDF output also picks the
// k labeling nodes, so a released prover cannot know which to keep resident until
// the sequential work is done. Verification stays O((Samples + 5k)·log n): no
// fetch, no VDF re-run.
//
// HONESTLY LABELED — what this does and does not prove:
//   - It delivers VERIFIED SPACE-hardness over a proven depth-robust graph and
//     binds the TIME half to possession; the tight ε→k constant through indegree-4
//     DRSample, and the re-plot-≫-epoch floor, are the external red-team's target
//     (design §8, immutable B8: self-marked homework is not adversarial proof).
//   - No replication proof and no zero-knowledge: it proves "this identity holds a
//     distinct blob of this size," not "this is a unique replica of user data."
//     Elevating held REAL network content to standing (so the Sybil cost and
//     durability funding become one mechanism) is the intended follow-up; the
//     synthetic bond here is the cold-start.
package bond

import (
	"bytes"
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
	// Samples is how many independent blocks one challenge probes for POSSESSION.
	// A prover missing a fraction f of its bond slips through with probability
	// (1-f)^Samples, so 20 samples makes even a 10%-short bond fail ~88% of
	// the time and a 30%-short bond fail ~99.9%. This catches a prover MISSING
	// blocks; the labeling opens (DefaultLabelSamples) catch one holding
	// arbitrary/short/reused bytes.
	Samples = 20
	// DefaultLabelSamples is the default number k of labeling-consistency opens
	// per challenge (design §3): each open recomputes one block's label from its
	// opened parent bytes, catching a prover that committed bytes it did not
	// correctly plot for (pk, n). Against an ε-short prover the soundness error is
	// ≤ (1-ε)^k, so k=64 catches a ~30% cheat at ≈2^-33 and a ~50% cheat at 2^-64.
	// It is a per-network EVOLVING knob (node Config.BondLabelSamples); any k<=0
	// passed to Answer/Verify resolves to this, so a zero-config caller (sim/test)
	// still runs the full check rather than silently disabling it.
	DefaultLabelSamples = 64
)

// resolveK maps a caller's k to the effective count, defaulting a non-positive
// value to DefaultLabelSamples so the labeling check can never be silently
// disabled by an unset config.
func resolveK(k int) int {
	if k <= 0 {
		return DefaultLabelSamples
	}
	return k
}

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
// cost the owner carries to keep answering. pk and seed are the public,
// size-bound identity seed the plot was labeled from, retained so the owner
// can build labeling opens and stamp its key on answers.
type Commitment struct {
	Size   int64
	Root   ports.Hash
	pk     []byte
	seed   []byte
	blocks [][]byte
	leaves []ports.Hash
}

// plotSeedN is the PUBLIC, identity- and size-bound plot seed for validator key
// pk and block count n: H("silt/bond/plot/v3" ‖ pk ‖ n). Folding pk binds the
// plot to one identity (a plot for pk_A fails recomputation under pk_B); folding
// n binds it to one size (a prefix of an n-plot is not a valid m-plot). It is
// public so the verifier can recompute labels without holding the plot.
func plotSeedN(pk []byte, n int) []byte {
	h := sha256.New()
	h.Write([]byte(plotDomain))
	h.Write(pk)
	var nb [8]byte
	binary.BigEndian.PutUint64(nb[:], uint64(n))
	h.Write(nb[:])
	return h.Sum(nil)
}

// PlotSeed is plotSeedN keyed by the public size, for callers that hold size
// rather than the block count.
func PlotSeed(pk []byte, size int64) []byte { return plotSeedN(pk, NumBlocks(size)) }

// Seal plots the identity-bound bond of ~size bytes for validator key pk from
// the PUBLIC seed H(pk, n) (see the package doc: the seed binds the plot to its
// owner AND its size, and makes each identity's plot distinct and verifiable).
// Blocks are generated in order because each depends on earlier ones (see
// plotBlock), so this is the deliberately non-trivial "plotting" step; the owner
// then STORES the result to answer challenges cheaply. Same (pk, size) ⇒ same
// plot, so an owner can regenerate.
func Seal(pk []byte, size int64) *Commitment {
	n := NumBlocks(size)
	seed := plotSeedN(pk, n)
	blocks := make([][]byte, n)
	leaves := make([]ports.Hash, n)
	for i := 0; i < n; i++ {
		b := plotBlock(seed, i, n, blocks) // reads blocks[0..i-1] already filled
		blocks[i] = b
		leaves[i] = ports.HashBytes(b)
	}
	return &Commitment{Size: size, Root: manifest.MerkleRoot(leaves), pk: append([]byte(nil), pk...), seed: seed, blocks: blocks, leaves: leaves}
}

// Blocks exposes the plot blocks so the owner can persist them (ports.
// PlotStore) and reload on restart instead of re-plotting (#93).
func (c *Commitment) Blocks() [][]byte { return c.blocks }

// ReleaseBlocks drops the resident plot bytes, keeping only the commitment
// (Root, Size) and the 32-byte leaves — the state of an F1/F2 adversary that
// pledged the space, advertised the bond, then FREED the bytes to save disk,
// intending to recompute on demand. Under the byte-binding + read-bound-VDF plot
// it can no longer answer: AnswerSpaceTime needs the seed block, the sampled
// blocks, AND the labeling opens' parent blocks it no longer holds, and it cannot
// cheaply recompute them (that is the whole point of the depth-robust labeling).
// So a released commitment fails a live audit — the wire-level consequence the
// sim asserts.
func (c *Commitment) ReleaseBlocks() { c.blocks = nil }

// Reconstruct rebuilds a Commitment from persisted plot blocks for validator key
// pk, RE-DERIVING the leaves and Merkle root from the bytes rather than trusting
// a stored root (B7). It errors if the block count doesn't match size or any
// block is the wrong length — a corrupt or stale plot the caller should discard
// and re-plot. The caller should additionally check the returned Root equals the
// root it persisted, catching silent on-disk corruption. (The plot-format version
// guard in the plot store forces a v2→v3 re-plot; Reconstruct re-derives the seed
// so a reloaded plot can still build labeling opens.)
func Reconstruct(pk []byte, size int64, blocks [][]byte) (*Commitment, error) {
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
	return &Commitment{Size: size, Root: manifest.MerkleRoot(leaves), pk: append([]byte(nil), pk...), seed: plotSeedN(pk, n), blocks: blocks, leaves: leaves}, nil
}

// LabelOpen is one labeling-consistency open (design §6): the challenged node's
// block, its immediate predecessor's block, and its DRSample parents' blocks,
// each with a Merkle inclusion proof against the committed root, so the verifier
// can recompute plotBlock(seed, v, {Pred, Parents...}) from the opened BYTES and
// check it equals Node. Pred/PredProof are omitted for the genesis node (v==0),
// which has no predecessor and no parents.
type LabelOpen struct {
	Node         []byte
	NodeProof    manifest.Proof
	Pred         []byte           `cbor:",omitempty"`
	PredProof    manifest.Proof   `cbor:",omitempty"`
	Parents      [][]byte         `cbor:",omitempty"`
	ParentProofs []manifest.Proof `cbor:",omitempty"`
}

// Answer is a prover's response to a challenge: the probed blocks plus
// their Merkle inclusion proofs against the committed root, the k labeling opens,
// and (for a space-TIME answer) the VDF proof that bound the challenge to elapsed
// sequential work.
type Answer struct {
	Indices []int
	Blocks  [][]byte
	Proofs  []manifest.Proof
	// PK is the prover's ed25519 public key. The plot seed is H(PK, n), so the
	// verifier needs PK to recompute labels; the caller BINDS it (the live audit
	// checks sha256(PK)==challenged NodeID, the chain uses BondReg.Validator as
	// the authoritative key). It is already public (NodeID = sha256(PK), gossiped
	// bond roots are already NodeID-linked), so carrying it leaks nothing new.
	PK []byte `cbor:",omitempty"`
	// LabelIndices / LabelBundles are the labeling-consistency opens (design §6):
	// k interior nodes chosen from the effective challenge nonce (vdfDerivedNonce
	// for space-time, the plain nonce for space-only), each opened with its
	// predecessor and DRSample parents so the verifier can recompute the label.
	LabelIndices []int       `cbor:",omitempty"`
	LabelBundles []LabelOpen `cbor:",omitempty"`
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

// Answer builds the space-only response for nonce from held blocks, including k
// labeling opens. It returns false if the owner no longer holds a probed block
// (i.e. it cannot prove the bond it committed to).
func (c *Commitment) Answer(nonce uint64, k int) (Answer, bool) {
	return c.answer(nonce, k)
}

// answer builds a response over the effective challenge nonce: the possession
// samples, the k labeling opens, and the prover's key. Both the space-only path
// (effNonce == the plain nonce) and the space-time path (effNonce ==
// vdfDerivedNonce) funnel through here so the two answer shapes stay identical.
func (c *Commitment) answer(effNonce uint64, k int) (Answer, bool) {
	a := Answer{PK: c.pk}
	// Possession samples.
	idxs := challengeIndices(c.Root, len(c.leaves), effNonce)
	for _, i := range idxs {
		if i >= len(c.blocks) || c.blocks[i] == nil {
			return Answer{}, false
		}
		p, err := manifest.Prove(c.leaves, i)
		if err != nil {
			return Answer{}, false
		}
		a.Indices = append(a.Indices, i)
		a.Blocks = append(a.Blocks, c.blocks[i])
		a.Proofs = append(a.Proofs, p)
	}
	// Labeling opens.
	li, bundles, ok := c.labelOpens(effNonce, resolveK(k))
	if !ok {
		return Answer{}, false
	}
	a.LabelIndices, a.LabelBundles = li, bundles
	return a, true
}

// labelOpens builds the k labeling-consistency opens for the effective nonce:
// for each challenged node v it opens v, its predecessor v-1 (unless v==0), and
// its DRSample parents, each with a Merkle proof. It returns false if the owner
// no longer holds a required block (a released plot).
func (c *Commitment) labelOpens(effNonce uint64, k int) ([]int, []LabelOpen, bool) {
	n := len(c.leaves)
	li := labelIndices(c.Root, n, effNonce, k)
	bundles := make([]LabelOpen, len(li))
	for j, v := range li {
		if v >= len(c.blocks) || c.blocks[v] == nil {
			return nil, nil, false
		}
		np, err := manifest.Prove(c.leaves, v)
		if err != nil {
			return nil, nil, false
		}
		lo := LabelOpen{Node: c.blocks[v], NodeProof: np}
		if v > 0 {
			if c.blocks[v-1] == nil {
				return nil, nil, false
			}
			pp, err := manifest.Prove(c.leaves, v-1)
			if err != nil {
				return nil, nil, false
			}
			lo.Pred, lo.PredProof = c.blocks[v-1], pp
		}
		for _, p := range parentIndices(c.seed, v, n) {
			if p >= len(c.blocks) || c.blocks[p] == nil {
				return nil, nil, false
			}
			pp, err := manifest.Prove(c.leaves, p)
			if err != nil {
				return nil, nil, false
			}
			lo.Parents = append(lo.Parents, c.blocks[p])
			lo.ParentProofs = append(lo.ParentProofs, pp)
		}
		bundles[j] = lo
	}
	return li, bundles, true
}

// AnswerSpaceTime is the proof-of-space-TIME response. It binds the VDF to a
// plot READ done before the VDF runs (M0 F2 fix): it first reads the block at
// seedIndex(root, nonce) from the plot, seeds the VDF from that block's bytes,
// runs `delay` sequential squarings, then derives the probed block indices AND
// the k labeling nodes from the VDF output. Because the seed requires a plot
// block — and re-deriving that block on demand is the Ω(n) depth-robust recompute
// — a prover that released the space cannot cheaply produce the seed, so releasing
// the space forfeits the answer. delay == 0 falls back to a space-only answer.
func (c *Commitment) AnswerSpaceTime(nonce uint64, p vdf.Params, delay uint64, k int) (Answer, bool) {
	if delay == 0 {
		return c.Answer(nonce, k)
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
	a, ok := c.answer(vdfDerivedNonce(proof.Y), k)
	if !ok {
		return Answer{}, false
	}
	a.SeedBlock, a.SeedProof = seedBlock, seedProof
	a.VDFY, a.VDFPi, a.VDFT = proof.Y, proof.Pi, proof.T
	return a, true
}

// Verify checks a space-only answer against ONLY the committed root — no
// ground truth, no held blocks on the verifier's side. Passing requires the
// prover to have produced the exact probed blocks, valid inclusion proofs, AND k
// labeling opens whose recomputed labels match — which it cannot do without
// holding a plot correctly labeled for (pk, size). pk is the authoritative key
// the caller trusts (see Answer.PK); labels are recomputed from H(pk, n).
func Verify(pk []byte, root ports.Hash, size int64, nonce uint64, a Answer, k int) bool {
	return verifyAt(pk, root, size, nonce, a, k)
}

// VerifySpaceTime checks a proof-of-space-time answer: the VDF must attest the
// required delay over the challenge (freshness + elapsed sequential work), the
// blocks it derives — cheaply, without redoing the work — must be held, and the k
// labeling opens must recompute correctly for (pk, size). It stays O(log n) on
// the verifier: the whole point of the VDF is that checking it is fast even
// though producing it was slow.
func VerifySpaceTime(pk []byte, root ports.Hash, size int64, nonce uint64, a Answer, p vdf.Params, delay uint64, k int) bool {
	if delay == 0 {
		return Verify(pk, root, size, nonce, a, k)
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
	return verifyAt(pk, root, size, vdfDerivedNonce(a.VDFY), a, k)
}

// verifyAt checks an answer's possession samples and labeling opens against the
// effective challenge nonce. The possession samples prove the prover holds the
// probed blocks; the labeling opens prove those blocks were correctly labeled for
// (pk, size) — the soundness the pre-G2 scheme lacked.
func verifyAt(pk []byte, root ports.Hash, size int64, effNonce uint64, a Answer, k int) bool {
	n := NumBlocks(size)
	want := challengeIndices(root, n, effNonce)
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
	return verifyLabels(pk, root, n, effNonce, a, k)
}

// verifyLabels is the labeling-consistency check (design §6) — the core of the G2
// fix. For each of the k challenged interior nodes it verifies Merkle inclusion of
// the opened node, predecessor, and DRSample parents, then RECOMPUTES the node's
// label from the opened parent BYTES under the public seed H(pk, n) and requires
// a byte-for-byte match. An attacker committing arbitrary, reused, or wrong-size
// bytes cannot satisfy this for a node it did not correctly plot for (pk, n), so a
// random hit fails deterministically; k independent hits catch an ε-short prover
// with probability ≥ 1-(1-ε)^k. Because the seed is public, the verifier does all
// of this WITHOUT holding the plot.
func verifyLabels(pk []byte, root ports.Hash, n int, effNonce uint64, a Answer, k int) bool {
	kk := resolveK(k)
	li := labelIndices(root, n, effNonce, kk)
	if len(a.LabelIndices) != kk || len(a.LabelBundles) != kk {
		return false
	}
	seed := plotSeedN(pk, n)
	zero := make([]byte, BlockSize)
	for j := 0; j < kk; j++ {
		v := li[j]
		if a.LabelIndices[j] != v {
			return false
		}
		b := a.LabelBundles[j]
		// Node inclusion at v.
		if b.NodeProof.Index != v || b.NodeProof.Total != n {
			return false
		}
		if !manifest.VerifyProof(root, ports.HashBytes(b.Node), b.NodeProof) {
			return false
		}
		// Predecessor inclusion at v-1 (the genesis node has none).
		var pred []byte
		if v > 0 {
			if b.PredProof.Index != v-1 || b.PredProof.Total != n {
				return false
			}
			if !manifest.VerifyProof(root, ports.HashBytes(b.Pred), b.PredProof) {
				return false
			}
			pred = b.Pred
		} else {
			if len(b.Pred) != 0 {
				return false // v==0 carries no predecessor
			}
			pred = zero
		}
		// Parent inclusion at the deterministic DRSample indices.
		pidx := parentIndices(seed, v, n)
		if len(b.Parents) != len(pidx) || len(b.ParentProofs) != len(pidx) {
			return false
		}
		for m, p := range pidx {
			if b.ParentProofs[m].Index != p || b.ParentProofs[m].Total != n {
				return false
			}
			if !manifest.VerifyProof(root, ports.HashBytes(b.Parents[m]), b.ParentProofs[m]) {
				return false
			}
		}
		// The label relation: the committed node MUST be the plot's label for v.
		if !bytes.Equal(b.Node, labelBlock(seed, v, pred, b.Parents)) {
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

// challengeIndices derives which blocks a nonce probes for POSSESSION, from the
// PUBLIC (root, nBlocks, nonce) so prover and verifier compute them identically.
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

// labelIndices derives the k interior nodes for the labeling-consistency check,
// from the PUBLIC (root, nBlocks, nonce) — domain-separated from challengeIndices
// so the labeling nodes and the possession samples are chosen independently.
// Index j depends only on (root, nonce, j), so the first k of any longer draw is
// a prefix: a network can raise k without invalidating shorter checks.
func labelIndices(root ports.Hash, nBlocks int, nonce uint64, k int) []int {
	k = resolveK(k)
	idx := make([]int, k)
	for j := 0; j < k; j++ {
		h := sha256.New()
		h.Write([]byte("silt/bond/label/v3"))
		h.Write(root[:])
		var b [16]byte
		binary.BigEndian.PutUint64(b[:8], nonce)
		binary.BigEndian.PutUint64(b[8:], uint64(j))
		h.Write(b[:])
		sum := h.Sum(nil)
		idx[j] = int(binary.BigEndian.Uint64(sum[:8]) % uint64(nBlocks))
	}
	return idx
}

const (
	// plotDomain is v3 with the PUBLIC identity- and size-bound seed and the
	// verified labeling check (M0 Sybil fix G2, docs/design/m0-sybil-rebind.md).
	// It also namespaces the seed H(plotDomain ‖ pk ‖ n). A v1/v2 plot on disk
	// re-plots rather than reloading (the disk format version guards this)
	// because its blocks are the old labeling the red-team broke.
	plotDomain = "silt/bond/plot/v3"
	// plotParents is how many DRSample long-range parents each block depends on,
	// on top of its immediate predecessor. DRSample with a chain already yields a
	// depth-robust graph at indegree 2; extra parents strengthen the pebbling
	// bound at a small plotting-time cost.
	plotParents = 3
)

// plotBlock is the identity-bound, byte-binding, depth-robust block generator for
// a Seal pass: it resolves block i's predecessor and DRSample parents from the
// blocks array, then labels via labelBlock. blocks must already hold the finalized
// bytes of blocks 0..i-1.
func plotBlock(seed []byte, i, n int, blocks [][]byte) []byte {
	var pred []byte
	if i > 0 {
		pred = blocks[i-1] // the chain: immediate predecessor's full bytes
	} else {
		pred = make([]byte, BlockSize) // genesis: zero block
	}
	var parents [][]byte
	for _, p := range parentIndices(seed, i, n) {
		parents = append(parents, blocks[p]) // long-range dependencies, full bytes
	}
	return labelBlock(seed, i, pred, parents)
}

// labelBlock computes the memory-hard label for node i from the seed, the index,
// and the FULL BYTES of its predecessor and DRSample parents, then expands it to
// BlockSize. This is the RECOMPUTABLE PUBLIC PREDICATE at the heart of the proof:
// the Seal loop calls it with the plot's own bytes, and verifyLabels calls it with
// the opened, Merkle-proven parent bytes — a match proves the committed node is a
// genuine label of the (pk, n) plot. Binding to the parents' 4 KiB bytes (not
// their 32-byte leaves) is what forces a prover to STORE the plot.
func labelBlock(seed []byte, i int, pred []byte, parents [][]byte) []byte {
	h := sha256.New()
	h.Write([]byte(plotDomain))
	h.Write(seed)
	var ib [8]byte
	binary.BigEndian.PutUint64(ib[:], uint64(i))
	h.Write(ib[:])
	h.Write(pred)
	for _, p := range parents {
		h.Write(p)
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
// the old flat-uniform choice). Deterministic from (seed, i, n); returns nil for
// block 0. n is folded in (on top of the seed, which is H(pk, n)) so the draw is
// size-bound: a prefix of an n-plot is not a valid smaller plot. Repeats are
// harmless.
func parentIndices(seed []byte, i, n int) []int {
	if i == 0 {
		return nil
	}
	out := make([]int, plotParents)
	for j := 0; j < plotParents; j++ {
		h := sha256.New()
		h.Write([]byte(plotDomain + "/parent"))
		h.Write(seed)
		var b [24]byte
		binary.BigEndian.PutUint64(b[:8], uint64(i))
		binary.BigEndian.PutUint64(b[8:16], uint64(j))
		binary.BigEndian.PutUint64(b[16:], uint64(n))
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
