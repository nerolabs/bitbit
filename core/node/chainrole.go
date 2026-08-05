// The validator role (M12): a node that keeps a chain replica, judges
// proposals, and helps commit blocks. All chain traffic rides the same
// Transport port as everything else, so the sim exercises consensus
// deterministically and tcpnet carries it over pinned TLS unchanged.
package node

import (
	"bytes"
	"crypto/ed25519"
	"errors"
	"fmt"
	"sort"

	"github.com/nerolabs/silt/core/chain"
	"github.com/nerolabs/silt/ports"
)

// EnableChain turns on the validator role: ch is this node's replica,
// priv its signing key (the SAME key whose hash is its NodeID — M10's
// identity doing double duty).
func (n *Node) EnableChain(ch *chain.Chain, priv ed25519.PrivateKey) {
	n.chain = ch
	n.signer = priv
}

// Chain exposes the local replica (dashboards, tests).
func (n *Node) Chain() *chain.Chain { return n.chain }

// CanonicalIssuers is the deterministic, on-chain-bonded issuer set a publisher
// should acquire tokens/credits from for privacy (M0 D3 / F4 §2c): every
// publisher asks the SAME validators, so the subset chosen leaks nothing. Empty
// if there is no chain or no on-chain bonds (caller falls back to its peer list).
func (n *Node) CanonicalIssuers(max int) []ports.NodeID {
	if n.chain == nil {
		return nil
	}
	return n.chain.CanonicalIssuers(max)
}

// handleChain processes validator messages; returns false if the kind
// isn't chain-related.
func (n *Node) handleChain(from ports.NodeID, msg ports.Message) bool {
	if n.chain == nil {
		switch msg.Kind {
		case ports.MsgProposeBlock, ports.MsgCommitBlock, ports.MsgGetChain:
			n.reply(from, msg, ports.Message{Kind: replyKind(msg.Kind), OK: false})
			return true
		}
		return false
	}
	switch msg.Kind {
	case ports.MsgProposeBlock:
		// Attest only what we would accept: same rules, our reputation view.
		b, err := chain.Decode(msg.Data)
		if err != nil || n.chain.ValidateProposal(b) != nil {
			n.reply(from, msg, ports.Message{Kind: ports.MsgAttestReply, OK: false})
			return true
		}
		// Never equivocate: refuse to sign a DIFFERENT block at a height we
		// already attested, even if two competing proposals arrive before
		// either commits. An honest validator's signature at a height is
		// final; this is what makes a double-sign proof (chain.Equivocation)
		// evidence of malice, not an accident.
		if prev, ok := n.attested[b.Height]; ok && prev != b.Hash() {
			n.reply(from, msg, ports.Message{Kind: ports.MsgAttestReply, OK: false})
			return true
		}
		att := chain.Attest(b, n.signer)
		n.attested[b.Height] = b.Hash()
		raw, _ := attEncode(att)
		n.reply(from, msg, ports.Message{Kind: ports.MsgAttestReply, OK: true, Data: raw})
	case ports.MsgCommitBlock:
		b, err := chain.Decode(msg.Data)
		ok := err == nil && n.chain.Append(*b) == nil
		if ok {
			n.Stats.BlocksCommitted++
			n.logf(ports.LogInfo, "block committed", "height", b.Height, "entries", len(b.Entries), "attestations", len(b.Atts), "via", "broadcast")
			if n.onCommit != nil {
				n.onCommit(*b)
			}
		}
		n.reply(from, msg, ports.Message{Kind: ports.MsgCommitAck, OK: ok})
	case ports.MsgGetChain:
		blocks := n.chain.Blocks(msg.Height)
		n.reply(from, msg, ports.Message{Kind: ports.MsgChainReply, OK: true, Data: chain.EncodeBlocks(blocks)})
	case ports.MsgSubmitBondReg:
		// A peer submitted a fresh bond renewal for us to include when we next
		// propose (H2 non-proposer renewal). Queue it only if it verifies for our
		// current head — a stale or forged one is dropped on arrival, and the
		// submitter resubmits on its next renewal sweep. The reg is self-signed and
		// self-verifying (bound to the submitter's own key), so accepting a peer's
		// reg grants standing to the PEER, never to us.
		if n.chain != nil && n.chain.Objective() {
			if reg, err := bondRegDecode(msg.Data); err == nil && n.chain.ValidateBondReg(reg) {
				if n.pendingBondRegs == nil {
					n.pendingBondRegs = make(map[ports.NodeID]chain.BondReg)
				}
				n.pendingBondRegs[reg.ValidatorID()] = reg
			}
		}
		n.reply(from, msg, ports.Message{Kind: ports.MsgSubmitBondRegAck, OK: true})
	default:
		return false
	}
	return true
}

func replyKind(k ports.MsgKind) ports.MsgKind {
	switch k {
	case ports.MsgProposeBlock:
		return ports.MsgAttestReply
	case ports.MsgCommitBlock:
		return ports.MsgCommitAck
	default:
		return ports.MsgChainReply
	}
}

// OnCommit registers a callback fired when a block lands in the local
// replica (daemon logging/persistence).
func (n *Node) OnCommit(fn func(chain.Block)) { n.onCommit = fn }

var ErrNoChain = errors.New("node: validator role not enabled")

// ProposeEntry runs one round of consensus: build a block at the local
// head, sign it, gather attestations from attesters until quorum,
// commit locally, then broadcast the committed block to every replica
// holder in broadcast (validators attest; ALL replicas hear commits).
// done receives nil once the block is in the LOCAL replica and the
// broadcast has been attempted; peers that missed it will sync.
func (n *Node) ProposeEntry(e ports.Entry, attesters, broadcast []ports.NodeID, quorum int, done func(error)) {
	if n.chain == nil {
		done(ErrNoChain)
		return
	}
	prev, height := n.chain.Head()
	n.proposeBlock(&chain.Block{Version: chain.BlockVersion, Height: height, Prev: prev, Entries: []ports.Entry{e}},
		attesters, broadcast, quorum, done)
}

// ProposeRevocation runs consensus on a TAKEDOWN: an append-only block
// that marks roots denied. It needs the same reputation quorum as a
// publication, so removing content is exactly as governed as adding it —
// no single node can take a file down, and the record of the takedown is
// itself immutable and replicated.
func (n *Node) ProposeRevocation(roots []ports.Hash, attesters, broadcast []ports.NodeID, quorum int, done func(error)) {
	if n.chain == nil {
		done(ErrNoChain)
		return
	}
	prev, height := n.chain.Head()
	n.proposeBlock(&chain.Block{Version: chain.BlockVersion, Height: height, Prev: prev, Revocations: roots},
		attesters, broadcast, quorum, done)
}

// proposeBlock signs b, gathers attestations to quorum, commits locally,
// and broadcasts — shared by entry and revocation proposals.
func (n *Node) proposeBlock(b *chain.Block, attesters, broadcast []ports.NodeID, quorum int, done func(error)) {
	// Gather at least what ValidateCommit will demand: with Byzantine quorum sizing
	// (H4) the chain requires 2f+1 over the qualified set, which can exceed the
	// caller's floor. Under-gathering would just fail our own Append; raise it here.
	if req := n.chain.RequiredQuorum(); req > quorum {
		quorum = req
	}
	// Objective fork-choice (F6): a proposer attaches its own live bond
	// registration, so proposing IS registering — an anchor bootstrapping the
	// network records its real bond in its first block, and every validator
	// renews as it proposes. No-op in legacy mode (BondRegs are ignored) and when
	// the node holds no bond.
	if n.chain.Objective() && n.bond != nil {
		if reg, ok := n.RegisterBondReg(b.Prev); ok {
			b.BondRegs = append(b.BondRegs, reg)
		}
	}
	// Fold in peer-submitted renewals (H2 / RT-2): an attest-only validator can't
	// propose, so it SUBMITS its fresh BondReg (MsgSubmitBondReg) and whoever
	// proposes next records it — renewing that validator's TTL clock without it
	// ever proposing. Include only regs still valid for THIS head (ValidateBondReg),
	// so one stale or forged submission can't poison the block; sort by validator
	// id so the block bytes are deterministic. Clear the queue either way — a peer
	// resubmits on its next renewal sweep, so a dropped stale reg is self-healing.
	if n.chain.Objective() && len(n.pendingBondRegs) > 0 {
		fresh := make([]chain.BondReg, 0, len(n.pendingBondRegs))
		for vid, reg := range n.pendingBondRegs {
			if vid != n.id && n.chain.ValidateBondReg(reg) {
				fresh = append(fresh, reg)
			}
		}
		sort.Slice(fresh, func(i, j int) bool {
			a, b := fresh[i].ValidatorID(), fresh[j].ValidatorID()
			return bytes.Compare(a[:], b[:]) < 0
		})
		b.BondRegs = append(b.BondRegs, fresh...)
		n.pendingBondRegs = make(map[ports.NodeID]chain.BondReg)
	}
	// Record any equivocations we detected on-chain, so every replica evicts the
	// culprit from the objective set in lockstep (F2). Drop any already recorded.
	if len(n.pendingSlashes) > 0 {
		var still []chain.Equivocation
		for _, e := range n.pendingSlashes {
			if n.chain.IsSlashed(e.CulpritID()) {
				continue
			}
			b.Slashes = append(b.Slashes, e)
		}
		n.pendingSlashes = still
	}
	chain.Sign(b, n.signer)
	if err := n.chain.ValidateProposal(b); err != nil {
		done(fmt.Errorf("propose: local pre-check: %w", err))
		return
	}
	raw := chain.Encode(b)

	var atts []chain.Attestation
	var ask func(i int)
	ask = func(i int) {
		if len(atts) >= quorum {
			b.Atts = atts
			if err := n.chain.Append(*b); err != nil {
				done(fmt.Errorf("propose: commit rejected by own replica: %w", err))
				return
			}
			n.Stats.BlocksCommitted++
			n.logf(ports.LogInfo, "block committed", "height", b.Height, "entries", len(b.Entries), "attestations", len(atts), "via", "proposal")
			if n.onCommit != nil {
				n.onCommit(*b)
			}
			n.broadcastCommit(b, broadcast, 0, func() { done(nil) })
			return
		}
		if i >= len(attesters) {
			done(fmt.Errorf("propose height %d: %w: %d of %d gathered",
				b.Height, chain.ErrNoQuorum, len(atts), quorum))
			return
		}
		v := attesters[i]
		if v == n.id {
			ask(i + 1)
			return
		}
		n.request(v, ports.Message{Kind: ports.MsgProposeBlock, Data: raw},
			func(resp ports.Message, err error) {
				if err == nil && resp.OK {
					if att, aerr := attDecode(resp.Data); aerr == nil {
						atts = append(atts, att)
					}
				}
				ask(i + 1)
			})
	}
	ask(0)
}

func (n *Node) broadcastCommit(b *chain.Block, validators []ports.NodeID, i int, done func()) {
	if i >= len(validators) {
		done()
		return
	}
	if validators[i] == n.id {
		n.broadcastCommit(b, validators, i+1, done)
		return
	}
	n.request(validators[i], ports.Message{Kind: ports.MsgCommitBlock, Data: chain.Encode(b)},
		func(ports.Message, error) { n.broadcastCommit(b, validators, i+1, done) })
}

// slashEquivocators finds validators who signed a different block at the same
// height across two competing histories (the abandoned fork and the adopted
// one) and slashes each in the local ledger — a proven double-sign costs
// standing (D2). The evidence is self-verifying (chain.VerifyEquivocation,
// inside FindEquivocations), so this cannot be triggered by an honest validator
// signing sequential heights. On-chain inclusion so every replica slashes in
// lockstep is the recorded follow-up; here each validator acts on what it sees.
func (n *Node) slashEquivocators(a, b []chain.Block) {
	if n.ledger == nil {
		return
	}
	for _, e := range chain.FindEquivocations(a, b) {
		n.ledger.SlashEquivocation(e.CulpritID()) // legacy/rep path
		// Queue the proof for on-chain recording so the OBJECTIVE set evicts the
		// culprit in lockstep on every replica (F2), not just this local ledger.
		if n.chain != nil && !n.chain.IsSlashed(e.CulpritID()) {
			n.pendingSlashes = append(n.pendingSlashes, e)
		}
		n.logf(ports.LogWarn, "validator slashed for equivocation", "culprit", e.CulpritID(), "height", e.A.Height)
	}
}

// SyncChain reconciles the local replica against peers — how a latecomer or a
// restarted daemon catches up AND how a partitioned validator heals a fork
// (D2). It fetches each peer's full chain and asks the replica to Reconcile:
// a peer that merely extends us is adopted as a catch-up, a peer on a heavier
// competing fork triggers a reorg, and a peer on an equal-or-lighter history
// is ignored — one uniform path (an equal-length fork, invisible to "give me
// blocks above my head", is exactly why we compare whole chains, not suffixes).
// Every block is fully re-validated inside Reconcile, so a lying peer wastes
// our time but cannot feed us an invalid or foreign chain. (Fetching the whole
// chain each sweep is the simple-correct v1; a genesis-to-head diff is the
// recorded scaling follow-up.)
func (n *Node) SyncChain(peers []ports.NodeID, done func(added int, err error)) {
	if n.chain == nil {
		done(0, ErrNoChain)
		return
	}
	added := 0
	var ask func(i int)
	ask = func(i int) {
		if i >= len(peers) {
			done(added, nil)
			return
		}
		if peers[i] == n.id {
			ask(i + 1)
			return
		}
		n.request(peers[i], ports.Message{Kind: ports.MsgGetChain, Height: 0},
			func(resp ports.Message, err error) {
				if err == nil && resp.OK {
					if full, derr := chain.DecodeBlocks(resp.Data); derr == nil && len(full) > 0 {
						before := n.chain.Len()
						old := n.chain.Blocks(0) // snapshot to catch cross-fork double-signs
						if ok, rerr := n.chain.Reconcile(full); ok {
							if d := n.chain.Len() - before; d > 0 {
								added += d
							}
							n.slashEquivocators(old, n.chain.Blocks(0))
							n.logf(ports.LogInfo, "chain reconciled from peer", "peer", peers[i], "len", n.chain.Len())
						} else if rerr != nil {
							n.logf(ports.LogDebug, "peer chain not adopted", "peer", peers[i], "err", rerr)
						}
					}
				}
				ask(i + 1)
			})
	}
	ask(0)
}

// StartChainSync begins the periodic reconciliation sweep in which this
// validator catches its replica up against the validators it knows — the
// restart/partition recovery path (F1). It fires once now and then reschedules
// on ChainSyncInterval. Catch-up MUST retry rather than fire once at boot: a
// restarted node re-earns its view of peer reputation live via bond audits, so
// the peers whose blocks it needs to adopt may not clear the qualification bar
// the instant the daemon starts — a later sweep, once audits have landed,
// succeeds. seed is the explicit -attesters set (may be empty); every validator
// this node has since learned a bond from is also a target, so a node restarted
// WITHOUT -attesters still catches up. onCatchUp fires (added > 0) after an
// adoption so the caller can persist the grown chain.
func (n *Node) StartChainSync(seed []ports.NodeID, onCatchUp func(added int)) {
	if n.chain == nil || n.chainSyncRunning {
		return
	}
	n.chainSyncRunning = true
	n.chainSyncSeed = seed
	n.chainSyncOnCatchUp = onCatchUp
	n.chainSyncTick()
}

func (n *Node) chainSyncTick() {
	peers := n.syncTargets()
	if len(peers) > 0 {
		n.SyncChain(peers, func(added int, _ error) {
			if added > 0 && n.chainSyncOnCatchUp != nil {
				n.chainSyncOnCatchUp(added)
			}
		})
		// Renew objective standing without proposing (H2 / RT-2): submit a fresh
		// bond proof to the same validator set we reconcile against, so an
		// attest-only validator sustains its TTL-bound standing. No-op off the
		// objective path or with no bond.
		n.SubmitBondRenewal(peers)
	}
	n.clock.AfterFunc(n.cfg.ChainSyncInterval, n.chainSyncTick)
}

// syncTargets is the set of validators to reconcile against this sweep: the
// explicit seed plus every peer we've learned a storage bond from (a bond is
// what a validator advertises), minus ourselves. Learned lazily from gossip, so
// the set fills in as the swarm comes into view — a node restarted with only a
// -bootstrap peer still discovers the rest and catches up.
func (n *Node) syncTargets() []ports.NodeID {
	set := make(map[ports.NodeID]bool, len(n.chainSyncSeed)+len(n.peerBonds))
	for _, id := range n.chainSyncSeed {
		if id != n.id {
			set[id] = true
		}
	}
	for id := range n.peerBonds {
		if id != n.id {
			set[id] = true
		}
	}
	out := make([]ports.NodeID, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	return out
}

// attestations share the block CBOR mode via small wrappers. The wrapper
// is a chain.Block, so it carries the current version like any other —
// chain.Decode (used by attDecode) requires it.
func attEncode(a chain.Attestation) ([]byte, error) {
	b := chain.Block{Version: chain.BlockVersion, Atts: []chain.Attestation{a}}
	return chain.Encode(&b), nil
}

func attDecode(raw []byte) (chain.Attestation, error) {
	b, err := chain.Decode(raw)
	if err != nil || len(b.Atts) != 1 {
		return chain.Attestation{}, fmt.Errorf("bad attestation payload")
	}
	return b.Atts[0], nil
}

// bond renewals ride the same block-CBOR wrapper as attestations, so a submitted
// BondReg (MsgSubmitBondReg) travels the wire without a new codec.
func bondRegEncode(r chain.BondReg) []byte {
	b := chain.Block{Version: chain.BlockVersion, BondRegs: []chain.BondReg{r}}
	return chain.Encode(&b)
}

func bondRegDecode(raw []byte) (chain.BondReg, error) {
	b, err := chain.Decode(raw)
	if err != nil || len(b.BondRegs) != 1 {
		return chain.BondReg{}, fmt.Errorf("bad bondreg payload")
	}
	return b.BondRegs[0], nil
}
