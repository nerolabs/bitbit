package node

// RED-TEAM / TEST-HARNESS ONLY. This file holds DELIBERATELY BYZANTINE consensus
// behaviour a correct node NEVER performs — the double-sign that honest nodes refuse
// (handleChain / chainrole.go). It exists so the accountability property "a proven
// double-sign costs standing" (D2, #184) can be exercised over the REAL WIRE, not only
// in the in-process sim, and so an external adversary can drive the same attack against
// a deployment to check the defence holds. It is reached only through the daemon's
// loudly-announced `-equivocate` flag; no honest path calls it.

import (
	"fmt"

	"github.com/nerolabs/silt/core/chain"
	"github.com/nerolabs/silt/ports"
)

// advEntry is a synthetic registry entry an equivocating proposer puts in its
// conflicting blocks — distinct labels make the two same-height blocks genuinely
// different (so their hashes differ and the equivocation is provable).
func advEntry(label string) ports.Entry {
	return ports.Entry{
		Root:           ports.HashBytes([]byte("silt/adversary/" + label)),
		ManifestChunks: []ports.ChunkID{ports.HashBytes([]byte("silt/adversary/" + label + "/m"))},
	}
}

// proposeAndCommitTo drives a single PRE-SIGNED block into ONE target's replica: it
// sends the proposal, takes the target's attestation, staples it on, and commits the
// block to that target — WITHOUT appending to this node's own chain. That is the
// primitive an equivocator needs and an honest proposer lacks: it lets one node place
// a conflicting fork onto a specific peer. Adding attestations does not change a
// block's hash (attesters sign the header hash), so a block signed once here keeps a
// stable identity across the propose→commit pair.
//
// The PROPOSAL is the readiness gate: a target that has not yet earned this node's
// standing refuses to attest, and the caller retries. The COMMIT is best-effort — a
// target that already holds this exact block (e.g. on a retry after a partially-placed
// fork) replies not-OK, which is success for our purposes, so we only fail the commit
// on a transport error, never on a not-OK reply.
func (n *Node) proposeAndCommitTo(b *chain.Block, target ports.NodeID, done func(error)) {
	raw := chain.Encode(b)
	n.request(target, ports.Message{Kind: ports.MsgProposeBlock, Data: raw}, func(resp ports.Message, err error) {
		if err != nil {
			done(err)
			return
		}
		if !resp.OK {
			done(fmt.Errorf("target %s refused the proposal at height %d (not yet standing?)", target, b.Height))
			return
		}
		att, aerr := attDecode(resp.Data)
		if aerr != nil {
			done(fmt.Errorf("decode attestation: %w", aerr))
			return
		}
		b.Atts = []chain.Attestation{att}
		n.request(target, ports.Message{Kind: ports.MsgCommitBlock, Data: chain.Encode(b)}, func(_ ports.Message, err2 error) {
			done(err2) // best-effort commit: only a transport error is fatal (already-held ⇒ not-OK ⇒ fine)
		})
	})
}

// Equivocate makes this node DOUBLE-SIGN at height 1 — the Byzantine act honest nodes
// refuse. It builds two DIFFERENT blocks at height 1 on the shared genesis, both
// signed by this node as proposer, and places them on two different honest peers:
//
//   - block X lands on honestX (honestX = [g, X], weight 1);
//   - a conflicting block Y lands on honestYZ, then an extension Z@2 on top, so
//     honestYZ = [g, Y, Z] with weight 2 — strictly heavier than the X-fork.
//
// When honestX later syncs the heavier fork from honestYZ it reconciles across the two
// histories, and chain.FindEquivocations catches this node signing X and Y at the same
// height — a self-verifying equivocation proof — and slashes it (chainrole.go). The
// weight ordering is deterministic (fork-choice is summed qualified-attester weight),
// so the detection is not a race. Requires this node to already hold consensus standing
// with both peers (so its proposals are accepted); callers retry until that is true.
//
// RED-TEAM / TEST-HARNESS ONLY — see the file header.
func (n *Node) Equivocate(honestX, honestYZ ports.NodeID, done func(error)) {
	if n.chain == nil || n.signer == nil {
		done(ErrNoChain)
		return
	}
	genesis := n.chain.Blocks(0)
	if len(genesis) == 0 {
		done(fmt.Errorf("equivocate: no genesis"))
		return
	}
	gh := genesis[0].Hash()

	x := &chain.Block{Version: chain.BlockVersion, Height: 1, Prev: gh, Entries: []ports.Entry{advEntry("X")}}
	chain.Sign(x, n.signer)
	y := &chain.Block{Version: chain.BlockVersion, Height: 1, Prev: gh, Entries: []ports.Entry{advEntry("Y")}}
	chain.Sign(y, n.signer)
	z := &chain.Block{Version: chain.BlockVersion, Height: 2, Prev: y.Hash(), Entries: []ports.Entry{advEntry("Z")}}
	chain.Sign(z, n.signer)

	// X → honestX; then Y and its extension Z → honestYZ (the heavier fork).
	n.proposeAndCommitTo(x, honestX, func(err error) {
		if err != nil {
			done(fmt.Errorf("place X on %s: %w", honestX, err))
			return
		}
		n.proposeAndCommitTo(y, honestYZ, func(err error) {
			if err != nil {
				done(fmt.Errorf("place Y on %s: %w", honestYZ, err))
				return
			}
			n.proposeAndCommitTo(z, honestYZ, func(err error) {
				if err != nil {
					done(fmt.Errorf("extend Z on %s: %w", honestYZ, err))
					return
				}
				done(nil)
			})
		})
	})
}
