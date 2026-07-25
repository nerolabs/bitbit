// The validator role (M12): a node that keeps a chain replica, judges
// proposals, and helps commit blocks. All chain traffic rides the same
// Transport port as everything else, so the sim exercises consensus
// deterministically and tcpnet carries it over pinned TLS unchanged.
package node

import (
	"crypto/ed25519"
	"errors"
	"fmt"

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
		att := chain.Attest(b, n.signer)
		raw, _ := attEncode(att)
		n.reply(from, msg, ports.Message{Kind: ports.MsgAttestReply, OK: true, Data: raw})
	case ports.MsgCommitBlock:
		b, err := chain.Decode(msg.Data)
		ok := err == nil && n.chain.Append(*b) == nil
		if ok {
			n.Stats.BlocksCommitted++
			if n.onCommit != nil {
				n.onCommit(*b)
			}
		}
		n.reply(from, msg, ports.Message{Kind: ports.MsgCommitAck, OK: ok})
	case ports.MsgGetChain:
		blocks := n.chain.Blocks(msg.Height)
		n.reply(from, msg, ports.Message{Kind: ports.MsgChainReply, OK: true, Data: chain.EncodeBlocks(blocks)})
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
	b := &chain.Block{Height: height, Prev: prev, Entries: []ports.Entry{e}}
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

// SyncChain pulls blocks the local replica is missing from peers —
// how a latecomer (or a restarted daemon) catches up. Every fetched
// block is fully re-validated by Append; a lying peer can waste our
// time but cannot feed us an invalid chain.
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
		_, height := n.chain.Head()
		n.request(peers[i], ports.Message{Kind: ports.MsgGetChain, Height: height},
			func(resp ports.Message, err error) {
				if err == nil && resp.OK {
					if blocks, derr := chain.DecodeBlocks(resp.Data); derr == nil {
						for _, blk := range blocks {
							if n.chain.Append(blk) != nil {
								break
							}
							added++
						}
					}
				}
				ask(i + 1)
			})
	}
	ask(0)
}

// attestations share the block CBOR mode via small wrappers.
func attEncode(a chain.Attestation) ([]byte, error) {
	b := chain.Block{Atts: []chain.Attestation{a}}
	return chain.Encode(&b), nil
}

func attDecode(raw []byte) (chain.Attestation, error) {
	b, err := chain.Decode(raw)
	if err != nil || len(b.Atts) != 1 {
		return chain.Attestation{}, fmt.Errorf("bad attestation payload")
	}
	return b.Atts[0], nil
}
