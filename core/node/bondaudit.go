// Bond audit (T1b, #78): validators challenge each other's identity-bound
// storage bonds over the network, so consensus standing is continuously
// backed by real held storage rather than self-reported serving. This is the
// live half of the mechanism whose primitive (core/bond) and ledger
// (credit.RecordBondChallenge / DecayStale) landed in T1a. Design:
// docs/design/bond-audit.md.
package node

import (
	"crypto/ed25519"

	"github.com/nerolabs/silt/core/bond"
	"github.com/nerolabs/silt/core/crypto"
	"github.com/nerolabs/silt/core/vdf"
	"github.com/nerolabs/silt/ports"
)

// bondSecret derives a node's per-identity plot secret from its signing key,
// domain-separated so it never coincides with any other use of the key. The
// plot is sealed from THIS (only the owner has it), which is what binds the
// bond to the identity and stops an outsider precomputing a victim's root.
func bondSecret(signer ed25519.PrivateKey) []byte {
	var seed [32]byte
	copy(seed[:], signer.Seed())
	k := crypto.DeriveKey(seed, "silt/bond/plot/v1")
	return k[:]
}

// bondInfo is a peer's advertised bond, learned from gossip (BondRoot /
// BondSize on every message the peer sends).
type bondInfo struct {
	root ports.Hash
	size int64
}

// EnableBond makes this node hold an identity-bound storage bond of size bytes
// and starts advertising its root on outgoing messages, so peers can challenge
// it. Holding the plot is the cost; a validator must EnableBond to build
// consensus standing. If a plot store is attached (SetPlotStore) and already
// holds this identity's plot, it is RELOADED and re-verified against its
// committed root (B7) — a restart never re-plots (#93); otherwise the plot is
// generated once and persisted. (The plot is still held in memory; a
// disk-backed lazy commitment and moving plotting off the core loop are the
// recorded hardening follow-ups — see the core/bond package doc.)
// It returns whether the plot was RELOADED from disk (true) rather than freshly
// sealed (false), so a restart can honestly say it reused the plot instead of
// logging the identical wording as a first-time seal (acceptance F7).
func (n *Node) EnableBond(signer ed25519.PrivateKey, size int64) (reloaded bool) {
	// Record the identity signer so a node holding a bond can mint an on-chain
	// bond registration (RegisterBondReg, F6) even before it joins consensus. It
	// is the same key EnableChain uses (the node's one identity), so this is
	// consistent whether a validator enables the bond or the chain first.
	if n.signer == nil {
		n.signer = signer
	}
	if n.plotStore != nil {
		if root, blocks, ok, err := n.plotStore.Load(n.id); ok && err == nil {
			if c, rerr := bond.Reconstruct(size, blocks); rerr == nil && c.Root == root {
				n.bond = c // reloaded from disk, re-verified — no re-plot (#93)
				return true
			}
			n.logf(ports.LogWarn, "bond plot reload failed; re-plotting", "id", n.id)
		} else if err != nil {
			n.logf(ports.LogWarn, "bond plot load error; re-plotting", "err", err)
		}
	}
	n.bond = bond.Seal(bondSecret(signer), size)
	if n.plotStore != nil {
		if err := n.plotStore.Save(n.id, n.bond.Root, n.bond.Blocks()); err != nil {
			n.logf(ports.LogWarn, "bond plot persist failed", "err", err)
		}
	}
	return false
}

// StartBondAudit begins the periodic sweep in which this validator challenges
// the bonds of the validators it knows. It fires an immediate first sweep (so
// the node's own standing exists before peers are known — see the self-record
// in bondAuditOnce) and then reschedules. Needs a ledger to settle into.
func (n *Node) StartBondAudit() {
	if n.ledger == nil {
		return
	}
	n.bondAuditTick()
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
	// Record our OWN bond first: we hold it, so asserting it to ourselves is
	// honest self-knowledge — and it is what our local proposer/attester
	// pre-check reads, which a node cannot otherwise satisfy since it never
	// challenges itself (each validator judges by its own ledger). PEERS
	// still verify our bond independently over the wire, so a self-assertion
	// buys nothing with the quorum — only real held storage does.
	if n.bond != nil {
		n.ledger.RecordBondChallenge(n.id, n.bond.Root, n.bond.Size, true, now)
		// Narrate our own standing every sweep so an operator can SEE the
		// earned-standing mechanism the whole of M0 rests on actually working —
		// rising as bonds prove out, decaying if they lapse (acceptance F7).
		n.logf(ports.LogInfo, "standing", "self", n.id, "reputation", n.ledger.Reputation(n.id))
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
		// Piggyback issuer-key discovery on the audit: a validator needs each
		// peer validator's token-issuer key to verify the publish tokens it
		// blind-signed (T3). Peers may not have been up at bootstrap, so fetch
		// lazily and self-heal here (cheap; cached once obtained).
		if n.IssuerKeyOf(t.id) == nil {
			n.FetchIssuerKey(t.id, func(error) {})
		}
		n.rid++
		nonce := n.rid
		id, info := t.id, t.info
		n.request(id, ports.Message{Kind: ports.MsgBondChallenge, Nonce: nonce},
			func(resp ports.Message, err error) {
				if err != nil {
					return // unreachable this round; DecayStale handles sustained absence
				}
				ans, derr := bond.DecodeAnswer(resp.Data)
				ok := derr == nil && bond.VerifySpaceTime(info.root, info.size, nonce, ans, vdf.Default(), n.cfg.BondVDFDelay)
				// Replied-but-can't-prove is a FAIL (a liar advertising a bond
				// it doesn't hold) → standing zeroed; a valid answer earns it.
				// The root binds standing to the plot: a shared root credits
				// only its first owner (credit dedup), so co-located Sybils
				// pointing at one plot earn one bond's standing, not many.
				n.ledger.RecordBondChallenge(id, info.root, info.size, ok, now)
				// Narrate the verdict: a peer proving (or failing to prove) its
				// bond is exactly the "is the trust plane working?" moment an
				// operator needs to see, not infer from a diffed chain (F7).
				n.logf(ports.LogInfo, "bond challenge", "peer", id, "passed", ok, "standing", n.ledger.Reputation(id))
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
	ans, ok := n.bond.AnswerSpaceTime(msg.Nonce, vdf.Default(), n.cfg.BondVDFDelay)
	if !ok {
		return reply
	}
	if data, err := bond.EncodeAnswer(ans); err == nil {
		reply.Data = data
	}
	return reply
}
