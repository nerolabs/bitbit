// Real proof-of-retrieval: storage that can be spot-checked WITHOUT the
// auditor fetching the bytes.
//
// The pieces (see docs/math/05-proof-of-retrieval.md and core/por):
//
//   - Every chunk is distributed WITH its Merkle inclusion proof, so a
//     host can always show "this chunk is leaf i of root R" — binding
//     what it claims to store to a root the whole network agrees on.
//   - Every data/parity shard also travels with its per-block PoR
//     authenticators (core/por Tags), computed by the publisher under a
//     key derived from the file's LAYOUT key. A challenge names a seed and
//     a sample count; the prover aggregates the sampled blocks of its
//     stored bytes + tags into one compact response.
//   - The auditor derives the SAME por key from its care-link
//     (CareHandle.LayoutKey) and verifies the response touching NO data.
//     A prover that kept the tags but dropped the bytes (our liar nodes do
//     exactly that) cannot make the response verify — and the auditor
//     never fetches ground truth to find out. That is the whole point: the
//     verify-without-fetch the toy SHA-256(nonce‖data) scheme lacked.
package node

import (
	"crypto/sha256"
	"encoding/binary"

	"github.com/nerolabs/silt/core/crypto"
	"github.com/nerolabs/silt/core/link"
	"github.com/nerolabs/silt/core/manifest"
	"github.com/nerolabs/silt/core/pipeline"
	"github.com/nerolabs/silt/core/por"
	"github.com/nerolabs/silt/ports"
)

// porKeyDomain binds a file's PoR verification key to its layout key: only
// a care-link holder can derive it (via DerivePorKey), so a storage node —
// which holds chunk bytes and tags but never the layout key — cannot forge
// a proof. Mirrors how link.go derives the layout/content keys.
const porKeyDomain = "silt/por/v1"

// ctOverhead is the byte cost AES-256-GCM adds to a chunk: ciphertext =
// plaintext + this (see core/crypto ConvergentEncrypt/PrivateEncrypt). The
// auditor uses it to recompute a full shard's expected PoR block count from
// the layout's plaintext ChunkSize (por tags are over ciphertext). A guard
// test pins this to the real overhead so it can't drift.
const ctOverhead = 16

// porSampleCount is how many blocks a PoR challenge samples. A larger count
// raises the odds of catching a prover missing any fixed fraction of a
// shard (Shacham–Waters §4.1); it is clamped to the shard's block count, so
// small shards are sampled in full. A tuning knob (Evolving tenet), not a
// law.
const porSampleCount = 128

// DerivePorKey derives a file's PoR verification key from its layout key —
// the publisher (tagging at distribute time) and the auditor (verifying)
// both call this and arrive at the same key, with no key on the wire.
func DerivePorKey(layoutKey [32]byte) *por.Key {
	seed := crypto.DeriveKey(layoutKey, porKeyDomain)
	k, err := por.DeriveKey(seed[:], por.DefaultParams)
	if err != nil {
		panic(err) // DefaultParams is valid; cannot fail
	}
	return k
}

func cloneTags(in [][]byte) [][]byte {
	if len(in) == 0 {
		return nil
	}
	out := make([][]byte, len(in))
	for i, b := range in {
		out[i] = append([]byte(nil), b...)
	}
	return out
}

func copyProof(p ports.StorageProof) ports.StorageProof {
	p.Path = append([]ports.Hash(nil), p.Path...)
	p.PorTags = cloneTags(p.PorTags)
	return p
}

func verifyStorageProof(p ports.StorageProof, leaf ports.ChunkID) bool {
	return manifest.VerifyProof(p.Root, leaf, manifest.Proof{Index: p.Index, Total: p.Total, Path: p.Path})
}

// porChallengeSeed turns the auditor's monotonic request counter into a per-
// sweep BASE seed. The old comment claimed "a prover lacking the bytes cannot
// answer any seed" — RT-1 falsified that: a data-less identity cannot COMPUTE a
// proof, but it can RELAY the proof of an honest holder that can. porProverSeed
// (below) folds the challenged identity into the seed so a relayed proof fails.
func porChallengeSeed(nonce uint64) [32]byte {
	var nb [8]byte
	binary.BigEndian.PutUint64(nb[:], nonce)
	return sha256.Sum256(append([]byte("silt/por/challenge/v1"), nb[:]...))
}

// porProverSeed binds a challenge to the identity being challenged (M0 hardening
// H1 / red-team RT-1, Invariant A): the seed a prover answers is H(base ‖
// proverID), so its coefficients are prover-specific. A data-less identity B
// that relays honest holder A's aggregated (μ, σ) — computed under A's seed —
// fails B's verify, since B is graded under H(base ‖ B) ≠ H(base ‖ A). This
// stops the trivial one-proof-to-N-Sybils relay; the deeper "colluding holder
// recomputes a fresh proof per Sybil" residual is why PoR grants no STANDING at
// all (see credit.Reputation) — plain PoR over shared content is not Sybil-
// resistant (docs/design/m0-hardening-strategy.md §4 S2).
func porProverSeed(base [32]byte, prover ports.NodeID) [32]byte {
	h := sha256.New()
	h.Write([]byte("silt/por/challenge/prover/v2"))
	h.Write(base[:])
	h.Write(prover[:])
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// porChallenge reconstructs the por.Challenge both sides expand from. blocks
// is the shard's block count (len of stored tags); count is clamped to it.
func porChallenge(seed [32]byte, blocks, count int) por.Challenge {
	if count > blocks {
		count = blocks
	}
	return por.Challenge{Seed: seed, Blocks: blocks, Count: count}
}

// answerChallenge is the prover side. An honest holder aggregates its stored
// bytes + tags into a passing proof. A liar kept the tags but ditched the
// bytes: it can still form a response over its (absent) data, but the μ it
// produces cannot satisfy the verification equation — and no fetch is needed
// to expose that.
func (n *Node) answerChallenge(msg ports.Message) ports.Message {
	reply := ports.Message{Kind: ports.MsgChallengeReply}
	stored, hasProof := n.proofs[msg.ChunkID]
	if !hasProof {
		return reply // Found=false: nothing to prove
	}
	p := copyProof(stored)
	reply.Proof = &p
	tags := stored.PorTags
	blocks := len(tags)
	var seed [32]byte
	copy(seed[:], msg.PorSeed)
	c := porChallenge(seed, blocks, msg.PorCount)

	if n.liar {
		// Kept the receipt and the tags, ditched the goods: prove over the
		// data it no longer has (empty), which the tags will not vouch for.
		pr, err := por.Prove(por.DefaultParams, nil, tags, c)
		if err != nil {
			return reply
		}
		reply.Found = true
		reply.PorBlocks = blocks
		reply.PorMu, reply.PorSigma = pr.Mu, pr.Sigma
		return reply
	}

	if n.shrinkLiar && blocks > 1 {
		// F4: keep ONLY block 0, report PorBlocks=1, and prove over a challenge
		// clamped to that single block. Under a naive auditor (which trusts the
		// self-reported count) this passes while holding ~1/blocks of the shard.
		// The fix demands the shard's COMMITTED block count, so this is rejected.
		ck, err := n.store.Get(bg(), msg.ChunkID)
		if err != nil {
			return ports.Message{Kind: ports.MsgChallengeReply}
		}
		bb := por.DefaultParams.SectorsPerBlock * por.SectorBytes
		block0 := ck.Data
		if len(block0) > bb {
			block0 = block0[:bb]
		}
		cShrunk := porChallenge(seed, 1, msg.PorCount)
		pr, err := por.Prove(por.DefaultParams, block0, tags[:1], cShrunk)
		if err != nil {
			return reply
		}
		reply.Found = true
		reply.PorBlocks = 1
		reply.PorMu, reply.PorSigma = pr.Mu, pr.Sigma
		return reply
	}

	ck, err := n.store.Get(bg(), msg.ChunkID)
	if err != nil {
		return ports.Message{Kind: ports.MsgChallengeReply} // lost it; admit it
	}
	pr, err := por.Prove(por.DefaultParams, ck.Data, tags, c)
	if err != nil {
		return ports.Message{Kind: ports.MsgChallengeReply}
	}
	reply.Found = true
	reply.PorBlocks = blocks
	reply.PorMu, reply.PorSigma = pr.Mu, pr.Sigma
	return reply
}

// AuditReport summarizes one audit sweep.
type AuditReport struct {
	Challenges int
	Passed     int
	Failed     int
	NoTruth    int // leaves where no provider produced a passing proof
}

// Audit spot-checks every data/parity shard of root: challenge each
// provider and verify the PoR response against the key derived from the
// care-link — no ground-truth fetch. Results settle into the ledger — rent
// for the honest, slashes for the liars. The auditor needs the manifest (it
// fetches it first) and a ledger to settle into.
func (n *Node) Audit(reg ports.Registry, ch link.CareHandle, done func(AuditReport)) {
	var report AuditReport
	entry, ok, err := reg.Lookup(bg(), ch.Root)
	if err != nil || !ok {
		done(report)
		return
	}
	n.fetchAll(entry.ManifestChunks, func(missing []ports.ChunkID) {
		if len(missing) > 0 {
			done(report)
			return
		}
		m, err := pipeline.LoadLayout(bg(), n.store, entry, ch)
		if err != nil {
			done(report)
			return
		}
		porKey := DerivePorKey(ch.LayoutKey)
		leaves := m.Leaves()
		root := m.Root()
		dataN := len(m.Chunks)
		// EVERY stored shard is full-size: chunk.Split zero-pads the last frame
		// up to ChunkSize (the true payload length rides in the 8-byte frame
		// header), and erasure pads short stripes, so on the wire there is no
		// short tail. The auditor therefore demands the SAME full block count
		// for every leaf — ChunkSize+GCM ciphertext bytes — and a prover cannot
		// shrink the challenge by under-reporting its block count (red-team F4:
		// the old tail-leniency branch accepted any 1..wantFull for the last
		// leaf, which is actually full-size, letting a liar report PorBlocks=1
		// and pass while holding one block).
		want := por.DefaultParams.Blocks(int(m.ChunkSize) + ctOverhead)
		var nextLeaf func(i int)
		nextLeaf = func(i int) {
			if i == len(leaves) {
				done(report)
				return
			}
			// Providers live under the column key for coded shards; resolve
			// the audit there, but challenge the individual shard by id.
			key := ports.Hash(leaves[i])
			if col := columnAt(i, dataN, m.K, m.N); col != noColumn {
				key = colKey(root, col)
			}
			n.auditLeaf(leaves[i], key, porKey, want, &report, func() { nextLeaf(i + 1) })
		}
		nextLeaf(0)
	})
}

// blocksOK cross-checks a prover's reported block count against the count the
// auditor RECOMPUTED for the shard — exactly, for every leaf (red-team F4).
// Every stored shard is full-size (chunk.Split pads the tail), so the count is
// the same wantFull for all leaves. Letting any leaf report 1..wantFull let a
// liar advertise PorBlocks=1 and be challenged on block 0 alone, passing while
// holding a sliver of the shard. The auditor, not the prover, fixes the sample
// space.
func blocksOK(reported, want int) bool {
	return reported >= 1 && reported == want
}

type challengeAnswer struct {
	prover ports.NodeID
	valid  bool // Merkle proof present and verifies for this leaf
	blocks int  // prover-reported block count
	proof  por.Proof
}

func (n *Node) auditLeaf(id ports.ChunkID, key ports.Hash, porKey *por.Key,
	want int, report *AuditReport, done func()) {

	n.resolveProviders(key, func(provs []ports.NodeID) {
		n.rid++ // draw a fresh, deterministic base seed from the request counter
		base := porChallengeSeed(n.rid)
		var answers []challengeAnswer
		var challengeNext func(i int)
		challengeNext = func(i int) {
			if i == len(provs) {
				n.gradeAnswers(id, porKey, base, want, answers, report, done)
				return
			}
			p := provs[i]
			if p == n.id {
				challengeNext(i + 1)
				return
			}
			// Per-prover seed (H1): p is challenged under H(base ‖ p), so a proof
			// relayed from another holder (computed under a different identity's
			// seed) fails p's verify. p uses PorSeed verbatim, so no prover-side
			// change is needed.
			pseed := porProverSeed(base, p)
			n.request(p, ports.Message{Kind: ports.MsgChallenge, ChunkID: id, PorSeed: pseed[:], PorCount: porSampleCount},
				func(resp ports.Message, err error) {
					if err == nil {
						report.Challenges++
						valid := resp.Found && resp.Proof != nil &&
							verifyStorageProof(*resp.Proof, id)
						answers = append(answers, challengeAnswer{
							prover: p, valid: valid, blocks: resp.PorBlocks,
							proof: por.Proof{Mu: resp.PorMu, Sigma: resp.PorSigma},
						})
					}
					challengeNext(i + 1)
				})
		}
		challengeNext(0)
	})
}

// gradeAnswers verifies each PoR response against the auditor's derived key
// — no ground-truth fetch. An answer passes iff its Merkle proof binds the
// shard to the root, its reported block count checks out, and its aggregated
// response satisfies the verification equation. Passing earns rent; failing
// is slashed.
func (n *Node) gradeAnswers(id ports.ChunkID, porKey *por.Key, base [32]byte,
	want int, answers []challengeAnswer, report *AuditReport, done func()) {

	anyValid := false
	for _, a := range answers {
		// Verify over the AUTHORITATIVE block count (want), not the prover's
		// self-reported one — the prover cannot shrink its own sample space (F4) —
		// and under this prover's OWN seed H(base ‖ prover) (H1), so a proof
		// relayed from another identity fails here.
		passed := a.valid && blocksOK(a.blocks, want) &&
			porKey.Verify(id[:], porChallenge(porProverSeed(base, a.prover), want, porSampleCount), a.proof)
		if n.ledger != nil {
			n.ledger.RecordAudit(a.prover, id, passed)
		}
		if passed {
			report.Passed++
			anyValid = true
		} else {
			report.Failed++
		}
	}
	if len(answers) > 0 && !anyValid {
		// Nobody demonstrably held the bytes: retrievability unproven.
		report.NoTruth++
	}
	done()
}
