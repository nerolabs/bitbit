package node

import "github.com/nerolabs/silt/ports"

// Demand-responsive dispersion: a column of a popular file lives on only
// Replication hosts, so at scale a file drawing a large share of reads
// would hammer those few. Each node counts how often it serves each
// chunk; when one runs hot it PUSHES a few leased cache copies to other
// hosts (spread across failure domains), which announce as providers so
// readers divide across more sources. The copies are leased: if reads
// cool off they expire, so a flash-popular file can't permanently hoard
// capacity. Baseline Replication is the floor; heat is a temporary
// multiplier. HotThreshold 0 disables the whole mechanism.

// LeasedCount reports how many demand-driven cache copies this node holds
// right now — an observability hook for the fan-out mechanism.
func (n *Node) LeasedCount() int { return len(n.leases) }

// bumpDemand records a serve of id, refreshes the lease if this copy is a
// cache copy (it's still wanted), and makes sure the demand loop is awake.
func (n *Node) bumpDemand(id ports.ChunkID) {
	if n.cfg.HotThreshold <= 0 {
		return
	}
	n.serveLoad[id]++
	if _, leased := n.leases[id]; leased {
		n.leases[id] = n.clock.Now().Add(n.cfg.LeaseTTL) // still read → keep it
	}
	n.wakeDemand()
}

// takeLease records a cache copy's expiry and wakes the loop that will
// eventually drop it if reads don't refresh it.
func (n *Node) takeLease(id ports.ChunkID) {
	if n.cfg.HotThreshold <= 0 {
		return
	}
	n.leases[id] = n.clock.Now().Add(n.cfg.LeaseTTL)
	n.wakeDemand()
}

func (n *Node) wakeDemand() {
	if !n.demandRunning {
		n.demandRunning = true
		n.clock.AfterFunc(n.cfg.DemandInterval, n.demandTick)
	}
}

// demandTick fans out chunks that ran hot, decays the serve counts, and
// drops cache copies whose lease has lapsed. It reschedules only while
// there is something hot or leased to manage, so an idle node stops
// ticking entirely (and the deterministic sim can reach quiescence).
func (n *Node) demandTick() {
	now := n.clock.Now()
	for id, cnt := range n.serveLoad {
		if _, leased := n.leases[id]; !leased && cnt >= n.cfg.HotThreshold {
			n.fanOut(id)            // only origin holders fan out; cache copies don't cascade
			delete(n.serveLoad, id) // reset after a fan-out so it must heat up again
			continue
		}
		if half := cnt / 2; half == 0 {
			delete(n.serveLoad, id)
		} else {
			n.serveLoad[id] = half
		}
	}
	for id, exp := range n.leases {
		if now > exp {
			n.store.Delete(bg(), id)
			delete(n.leases, id)
			delete(n.serveLoad, id)
			delete(n.proofs, id)
		}
	}
	if len(n.serveLoad) > 0 || len(n.leases) > 0 {
		n.clock.AfterFunc(n.cfg.DemandInterval, n.demandTick)
	} else {
		n.demandRunning = false
	}
}

// fanOut pushes leased cache copies of a hot chunk to more hosts, spread
// away from this node's own failure domain, so read load divides. The
// copies register under the chunk's placement key (its column key when
// coded), so the same provider walk a reader already does finds them.
func (n *Node) fanOut(id ports.ChunkID) {
	c, err := n.store.Get(bg(), id)
	if err != nil {
		return
	}
	var proof *ports.StorageProof
	key := ports.Hash(id)
	if p, ok := n.proofs[id]; ok {
		pc := copyProof(p)
		proof = &pc
		key = placementKey(p.Root, id, p.Column)
	}
	n.IterativeFindNode(key, func(closest []ports.NodeID) {
		candidates := n.preferFreshDomain(closest, map[uint64]int{n.domainID: 1})
		var placed int
		var try func(i int)
		try = func(i int) {
			if placed >= n.cfg.FanoutReplicas || i >= len(candidates) {
				return
			}
			target := candidates[i]
			if target == n.id {
				try(i + 1)
				return
			}
			msg := ports.Message{Kind: ports.MsgStoreChunk, ChunkID: id, Data: c.Data, Proof: proof, Lease: true}
			n.request(target, msg, func(resp ports.Message, err error) {
				if err == nil && resp.OK {
					placed++
				}
				try(i + 1)
			})
		}
		try(0)
	})
}
