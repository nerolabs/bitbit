// Objective fork-choice wiring (M0 consensus D2 / red-team F6). These turn a
// validator's replica from the subjective reputation view onto the objective,
// on-chain PoST-bond view: the chain verifies bond registrations with the same
// space-time primitive the audit loop uses (EnableObjectiveChain), and a node
// mints its own registration from its held bond (RegisterBondReg). With MinBond
// set, fork-choice weight, quorum, and eligibility then become a function of the
// chain — identical on every replica — so honest replicas can no longer diverge.
package node

import (
	"github.com/nerolabs/silt/core/bond"
	"github.com/nerolabs/silt/core/chain"
	"github.com/nerolabs/silt/core/vdf"
	"github.com/nerolabs/silt/ports"
)

// EnableObjectiveChain injects the space-time bond verifier into this node's
// replica so on-chain BondRegs are re-checked against the real bond primitive
// (bond.VerifySpaceTime, the same check the audit loop runs). Call after
// EnableChain. It only changes behavior when the chain's Config.MinBond > 0 —
// otherwise the replica stays on the legacy reputation path.
func (n *Node) EnableObjectiveChain() {
	if n.chain == nil {
		return
	}
	delay := n.cfg.BondVDFDelay
	k := n.cfg.BondLabelSamples
	n.chain.SetBondVerifier(func(pk []byte, root ports.Hash, size int64, nonce uint64, answer []byte) bool {
		ans, err := bond.DecodeAnswer(answer)
		if err != nil {
			return false
		}
		// pk is the authoritative validator key from BondReg.Validator (the chain
		// verifies the ed25519 signature over it). Labels are recomputed from
		// H(pk, n), so a plot sealed for another identity or size fails (G2).
		return bond.VerifySpaceTime(pk, root, size, nonce, ans, vdf.Default(), delay, k)
	})
}

// RegisterBondReg builds this node's signed on-chain bond registration for the
// position following prev: its space-time proof answered for BondRegNonce(prev)
// and signed by its key. Returns false if the node holds no bond. A bonded
// proposer includes the result in a block so the validator enters (or renews in)
// the objective set; the fresh per-position nonce stops the proof being replayed.
func (n *Node) RegisterBondReg(prev ports.Hash) (chain.BondReg, bool) {
	if n.bond == nil || n.signer == nil {
		return chain.BondReg{}, false
	}
	ans, ok := n.bond.AnswerSpaceTime(chain.BondRegNonce(prev), vdf.Default(), n.cfg.BondVDFDelay, n.cfg.BondLabelSamples)
	if !ok {
		return chain.BondReg{}, false
	}
	answer, err := bond.EncodeAnswer(ans)
	if err != nil {
		return chain.BondReg{}, false
	}
	return chain.NewBondReg(n.signer, n.bond.Root, n.bond.Size, answer, prev, n.domainID), true
}

// SubmitBondRenewal broadcasts a fresh self-signed bond registration to peers so
// whichever one proposes next folds it into a block — the NON-PROPOSER renewal
// path (H2 / red-team RT-2). It is the liveness half of a bond TTL: an attest-only
// validator that still holds its plot can answer the fresh challenge and so keeps
// its objective standing without ever proposing, while a validator that RELEASED
// its plot (the release-and-coast attack) cannot produce the proof and decays out.
// No-op off the objective path or with no bond/signer. Fire-and-forget: a dropped
// submission is retried on the next sweep, and one inclusion resets the TTL clock.
func (n *Node) SubmitBondRenewal(peers []ports.NodeID) {
	if n.chain == nil || !n.chain.Objective() || n.bond == nil || n.signer == nil {
		return
	}
	head, _ := n.chain.Head()
	reg, ok := n.RegisterBondReg(head)
	if !ok {
		return
	}
	raw := bondRegEncode(reg)
	for _, p := range peers {
		if p == n.id {
			continue
		}
		n.request(p, ports.Message{Kind: ports.MsgSubmitBondReg, Data: raw}, func(ports.Message, error) {})
	}
}
