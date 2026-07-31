// Takedown enforcement on a node. A node no-ops on a denied root: it
// won't accept its chunks, serve them, prove them, announce them, or
// repair the file. See docs/safety-denylist.md.
//
// Governance note baked into the shape of this code: there is NO
// built-in list and NO privileged authority. A node's denials come only
// from (a) the on-chain revocation records its own replica has committed
// by reputation quorum, and (b) a local list its OPERATOR chose to load.
// The software ships the mechanism; it never ships a list and never
// phones home. Whoever runs the network runs the policy — the project
// that publishes this code does not.
package node

import (
	"github.com/nerolabs/silt/core/denylist"
	"github.com/nerolabs/silt/ports"
)

// SetDenylist gives the node an operator-loaded denylist (may be nil).
func (n *Node) SetDenylist(d *denylist.Set) { n.denylist = d }

// isDenied is the node's effective takedown check: the union of the
// operator's local list and the roots its chain replica has committed as
// revoked. Neither source is controlled by any central party.
func (n *Node) isDenied(root ports.Hash) bool {
	if n.denylist.Denied(root) {
		return true
	}
	return n.chain != nil && n.chain.Revoked(root)
}

// chunkDenied reports whether a held chunk belongs to a denied root
// (known via the storage proof the chunk arrived with).
func (n *Node) chunkDenied(id ports.ChunkID) bool {
	p, ok := n.proofs[id]
	return ok && n.isDenied(p.Root)
}

// EnforceDenylist sweeps everything the node holds and physically drops
// chunks belonging to denied roots, forgetting their provider records
// too. Called after a revocation commits or the operator's list changes,
// it turns a policy decision into deleted bytes. Returns how many chunks
// were purged.
func (n *Node) EnforceDenylist() int {
	purged := 0
	for id, p := range n.proofs {
		if n.isDenied(p.Root) {
			n.dropHosted(id) // bytes + proof gone; serving already no-ops
			purged++
		}
	}
	return purged
}
