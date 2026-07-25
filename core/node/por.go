// Toy proof-of-retrieval: storage that can be spot-checked.
//
// The pieces (see docs/math/05-proof-of-retrieval.md):
//
//   - Every chunk is distributed WITH its Merkle inclusion proof, so a
//     host can always show "this chunk is leaf i of root R" — binding
//     what it claims to store to a root the whole network agrees on.
//   - A challenge carries a fresh nonce; the answer must include
//     tag = SHA-256(nonce ‖ chunk bytes). The proof can be kept without
//     the data (our liar nodes do exactly that), but the tag cannot be
//     computed without the bytes, and the nonce kills replaying old
//     answers.
//   - The auditor grades tags against ground truth it fetches itself.
//     THAT is the toy part: a real PoR scheme exists precisely to skip
//     that fetch. The seams are all here; only the cryptography that
//     would remove the fetch is missing — deliberately, per HANDOFF.
package node

import (
	"crypto/sha256"
	"encoding/binary"

	"github.com/nerolabs/silt/core/link"
	"github.com/nerolabs/silt/core/manifest"
	"github.com/nerolabs/silt/core/pipeline"
	"github.com/nerolabs/silt/ports"
)

func copyProof(p ports.StorageProof) ports.StorageProof {
	p.Path = append([]ports.Hash(nil), p.Path...)
	return p
}

func verifyStorageProof(p ports.StorageProof, leaf ports.ChunkID) bool {
	return manifest.VerifyProof(p.Root, leaf, manifest.Proof{Index: p.Index, Total: p.Total, Path: p.Path})
}

// challengeTag is the possession token: H(nonce ‖ data). Deterministic
// given the data, uncomputable without it.
func challengeTag(nonce uint64, data []byte) []byte {
	h := sha256.New()
	var nb [8]byte
	binary.BigEndian.PutUint64(nb[:], nonce)
	h.Write(nb[:])
	h.Write(data)
	return h.Sum(nil)
}

// answerChallenge is the prover side. An honest holder returns proof and
// a correct tag. Our liar kept the proof but not the bytes — it answers
// with a tag it cannot make true.
func (n *Node) answerChallenge(msg ports.Message) ports.Message {
	reply := ports.Message{Kind: ports.MsgChallengeReply}
	proof, hasProof := n.proofs[msg.ChunkID]
	if !hasProof {
		return reply // Found=false: nothing to prove
	}
	p := copyProof(proof)
	reply.Proof = &p
	if n.liar {
		reply.Found = true
		reply.Tag = challengeTag(msg.Nonce, []byte("i promise i have it")) // it does not
		return reply
	}
	c, err := n.store.Get(bg(), msg.ChunkID)
	if err != nil {
		return ports.Message{Kind: ports.MsgChallengeReply} // lost it; admit it
	}
	reply.Found = true
	reply.Tag = challengeTag(msg.Nonce, c.Data)
	return reply
}

// AuditReport summarizes one audit sweep.
type AuditReport struct {
	Challenges int
	Passed     int
	Failed     int
	NoTruth    int // leaves where no provider could produce valid bytes
}

// Audit spot-checks every shard of root: challenge each provider, then
// fetch ground truth and grade the tags. Results are settled into the
// ledger — rent for the honest, slashes for the liars. The auditor
// needs the manifest (it fetches it first) and a ledger to settle into.
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
		leaves := m.Leaves()
		var nextLeaf func(i int)
		nextLeaf = func(i int) {
			if i == len(leaves) {
				done(report)
				return
			}
			n.auditLeaf(leaves[i], &report, func() { nextLeaf(i + 1) })
		}
		nextLeaf(0)
	})
}

type challengeAnswer struct {
	prover ports.NodeID
	valid  bool // proof present and verifies for this leaf
	tag    []byte
}

func (n *Node) auditLeaf(id ports.ChunkID, report *AuditReport, done func()) {
	n.resolveProviders(id, func(provs []ports.NodeID) {
		n.rid++ // draw a fresh, deterministic nonce from the request counter
		nonce := n.rid
		var answers []challengeAnswer
		var challengeNext func(i int)
		challengeNext = func(i int) {
			if i == len(provs) {
				n.gradeAnswers(id, nonce, answers, report, done)
				return
			}
			p := provs[i]
			if p == n.id {
				challengeNext(i + 1)
				return
			}
			n.request(p, ports.Message{Kind: ports.MsgChallenge, ChunkID: id, Nonce: nonce},
				func(resp ports.Message, err error) {
					if err == nil {
						report.Challenges++
						valid := resp.Found && resp.Proof != nil &&
							verifyStorageProof(*resp.Proof, id)
						answers = append(answers, challengeAnswer{prover: p, valid: valid, tag: resp.Tag})
					}
					challengeNext(i + 1)
				})
		}
		challengeNext(0)
	})
}

// gradeAnswers fetches ground truth from the answerers (first one whose
// bytes hash correctly wins — content addressing makes truth cheap to
// recognize) and settles every answer against it.
func (n *Node) gradeAnswers(id ports.ChunkID, nonce uint64, answers []challengeAnswer,
	report *AuditReport, done func()) {

	if len(answers) == 0 {
		done()
		return
	}
	var fetchFrom func(i int)
	fetchFrom = func(i int) {
		if i == len(answers) {
			// Nobody produced real bytes: unverifiable, and everyone
			// claiming otherwise fails.
			report.NoTruth++
			for _, a := range answers {
				if n.ledger != nil {
					n.ledger.RecordAudit(a.prover, id, false)
				}
				report.Failed++
			}
			done()
			return
		}
		n.request(answers[i].prover, ports.Message{Kind: ports.MsgFetchChunk, ChunkID: id},
			func(resp ports.Message, err error) {
				if err != nil || !resp.Found || !(ports.Chunk{ID: id, Data: resp.Data}).Verify() {
					fetchFrom(i + 1)
					return
				}
				trueTag := challengeTag(nonce, resp.Data)
				for _, a := range answers {
					passed := a.valid && bytesEqual(a.tag, trueTag)
					if n.ledger != nil {
						n.ledger.RecordAudit(a.prover, id, passed)
					}
					if passed {
						report.Passed++
					} else {
						report.Failed++
					}
				}
				done()
			})
	}
	fetchFrom(0)
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
