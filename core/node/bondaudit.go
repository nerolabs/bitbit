// Bond audit (T1b, #78): validators challenge each other's identity-bound
// storage bonds over the network, so consensus standing is continuously
// backed by real held storage rather than self-reported serving. This is the
// live half of the mechanism whose primitive (core/bond) and ledger
// (credit.RecordBondChallenge / DecayStale) landed in T1a. Design:
// docs/design/bond-audit.md.
package node

import (
	"github.com/nerolabs/silt/core/bond"
	"github.com/nerolabs/silt/ports"
)

// bondInfo is a peer's advertised bond, learned from gossip (BondRoot /
// BondSize on every message the peer sends).
type bondInfo struct {
	root ports.Hash
	size int64
}

// EnableBond seals this node's identity-bound storage bond of size bytes and
// starts advertising its root on outgoing messages, so peers can challenge it.
// Holding the sealed blob is the cost; a validator must EnableBond to build
// consensus standing. (V1: the bond is held in memory; persisting it to
// pledged disk + a memory-hard seal are the recorded hardening follow-ups —
// see docs/design/bond-audit.md and the core/bond package doc.)
func (n *Node) EnableBond(size int64) {
	n.bond = bond.Seal(n.id, size)
}

// StartBondAudit begins the periodic sweep in which this validator challenges
// the bonds of the validators it knows. Needs a ledger to settle results into.
func (n *Node) StartBondAudit() {
	if n.ledger == nil {
		return
	}
	n.clock.AfterFunc(n.cfg.BondAuditInterval, n.bondAuditTick)
}

// AuditBondsOnce runs a single bond-audit sweep now — no reschedule, no decay.
// The daemon uses StartBondAudit (the loop); this is for deterministic drives
// (sim/tests) and observable manual triggers.
func (n *Node) AuditBondsOnce() { n.bondAuditOnce(uint64(n.clock.Now()) + 1) }

func (n *Node) bondAuditTick() {
	now := uint64(n.clock.Now()) + 1 // +1 so the first tick is never 0 ("unset")
	n.bondAuditOnce(now)
	// Standing must be SUSTAINED: retire any bond not re-proven within
	// BondMaxAge, so a validator that stops answering loses its vote.
	n.ledger.DecayStale(now, uint64(n.cfg.BondMaxAge))
	n.clock.AfterFunc(n.cfg.BondAuditInterval, n.bondAuditTick)
}

// bondAuditOnce challenges every known peer bond once and settles the results
// into the ledger. Exposed (unexported, same package) so tests can drive a
// single deterministic sweep without the self-rescheduling timer.
func (n *Node) bondAuditOnce(now uint64) {
	if n.ledger == nil {
		return
	}
	// Snapshot: the callbacks below mutate nothing here, but a peer could be
	// learned mid-sweep — challenge the set we knew at sweep start.
	type target struct {
		id   ports.NodeID
		info bondInfo
	}
	var targets []target
	for id, info := range n.peerBonds {
		if id == n.id {
			continue
		}
		targets = append(targets, target{id, info})
	}
	for _, t := range targets {
		n.rid++
		nonce := n.rid
		id, info := t.id, t.info
		n.request(id, ports.Message{Kind: ports.MsgBondChallenge, Nonce: nonce},
			func(resp ports.Message, err error) {
				if err != nil {
					return // unreachable this round; DecayStale handles sustained absence
				}
				ans, derr := bond.DecodeAnswer(resp.Data)
				ok := derr == nil && bond.Verify(info.root, info.size, nonce, ans)
				// Replied-but-can't-prove is a FAIL (a liar advertising a bond
				// it doesn't hold) → standing zeroed; a valid answer earns it.
				n.ledger.RecordBondChallenge(id, info.size, ok, now)
			})
	}
}

// answerBondChallenge is the prover side: prove we still hold the bond we
// advertised by answering the challenge from our sealed blob. No bond, or a
// block we no longer hold, yields an empty reply — which the challenger scores
// as a failure.
func (n *Node) answerBondChallenge(msg ports.Message) ports.Message {
	reply := ports.Message{Kind: ports.MsgBondReply}
	if n.bond == nil {
		return reply
	}
	ans, ok := n.bond.Answer(msg.Nonce)
	if !ok {
		return reply
	}
	if data, err := bond.EncodeAnswer(ans); err == nil {
		reply.Data = data
	}
	return reply
}
