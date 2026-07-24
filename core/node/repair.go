// The repair loop — the behavior that makes churn survivable. A
// caretaker node periodically sweeps each file it cares about: probe
// every shard's availability in the swarm, and when a stripe has lost
// more than RepairSlack shards, fetch any k survivors, reconstruct the
// missing shards from parity math, verify each rebuilt shard against
// the Merkle-committed hash, and push the rebuilt shards back out to
// fresh nodes. Redundancy decays with every node death; repair pumps it
// back up. Files stay alive not because nodes are reliable but because
// the loop outruns the failures.
package node

import (
	"github.com/nerolabs/bitbit/core/dht"
	"github.com/nerolabs/bitbit/core/erasure"
	"github.com/nerolabs/bitbit/core/manifest"
	"github.com/nerolabs/bitbit/core/pipeline"
	"github.com/nerolabs/bitbit/ports"
)

// Care makes this node a caretaker of root: the repair loop starts
// unconditionally (a sweep that finds nothing fetchable just retries
// next interval — an earlier version gated the loop on the first
// manifest fetch succeeding, and a node death in that window silently
// disabled repair forever). The warm start fetches the manifest chunks
// now — caretakers ARE the manifest's redundancy in v1 — and announces
// the copies so the swarm can find them: the caretaker is usually
// nowhere near the chunk IDs, so without records planted on the nodes
// that ARE near, its copies are invisible to provider walks.
func (n *Node) Care(reg ports.Registry, root ports.Hash) {
	n.reg = reg
	n.care = append(n.care, root)
	if !n.repairRunning {
		n.repairRunning = true
		n.clock.AfterFunc(n.cfg.RepairInterval, n.repairTick)
	}
	entry, ok, err := reg.Lookup(bg(), root)
	if err != nil || !ok {
		return
	}
	n.fetchAll(entry.ManifestChunks, func(missing []ports.ChunkID) {
		if len(missing) == 0 {
			n.announceAll(entry.ManifestChunks, func() {})
		}
	})
}

// AnnounceHeld re-plants provider records for every chunk in the local
// store. Records live in peers' memories and die with processes — a
// daemon restarting with a disk full of chunks is invisible until it
// re-announces. Call after bootstrap; done receives the chunk count.
func (n *Node) AnnounceHeld(done func(int)) {
	ids, err := n.store.List(bg())
	if err != nil {
		done(0)
		return
	}
	dht.SortByDistance(n.id, ids) // List order is map-random; sort for determinism
	for _, id := range ids {
		n.provs.Add(id, n.id)
	}
	n.announceAll(ids, func() { done(len(ids)) })
}

// announceAll plants "I have chunk H" records on the K closest nodes to
// each chunk ID.
func (n *Node) announceAll(ids []ports.ChunkID, done func()) {
	var next func(i int)
	next = func(i int) {
		if i == len(ids) {
			done()
			return
		}
		id := ids[i]
		n.IterativeFindNode(id, func(closest []ports.NodeID) {
			var send func(j int)
			send = func(j int) {
				if j == len(closest) {
					next(i + 1)
					return
				}
				if closest[j] == n.id {
					send(j + 1)
					return
				}
				n.request(closest[j], ports.Message{Kind: ports.MsgAddProvider, Target: id},
					func(ports.Message, error) { send(j + 1) })
			}
			send(0)
		})
	}
	next(0)
}

// repairTick sweeps all cared-for roots sequentially, then reschedules
// itself. Rescheduling only after the sweep finishes means sweeps never
// overlap, however long probing takes.
func (n *Node) repairTick() {
	roots := append([]ports.Hash(nil), n.care...)
	var nextRoot func(i int)
	nextRoot = func(i int) {
		if i == len(roots) {
			n.clock.AfterFunc(n.cfg.RepairInterval, n.repairTick)
			return
		}
		n.repairRoot(roots[i], func() { nextRoot(i + 1) })
	}
	nextRoot(0)
}

// shardRef locates one stored shard of a file: which stripe, which
// position within it (0..k-1 data, k..n-1 parity), and which Merkle
// leaf it is (for re-attaching storage proofs on redistribution).
type shardRef struct {
	id      ports.ChunkID
	stripe  int
	pos     int
	leafIdx int
}

func (n *Node) repairRoot(root ports.Hash, done func()) {
	entry, ok, err := n.reg.Lookup(bg(), root)
	if err != nil || !ok {
		done()
		return
	}
	// (Re)acquire the manifest each sweep: mostly local cache hits, but
	// a caretaker that missed its warm-start fetch keeps trying rather
	// than sitting out the crisis.
	n.fetchAll(entry.ManifestChunks, func(missing []ports.ChunkID) {
		if len(missing) > 0 {
			done() // swarm can't supply it right now; retry next sweep
			return
		}
		n.repairRootWithManifest(entry, done)
	})
}

func (n *Node) repairRootWithManifest(entry ports.Entry, done func()) {
	m, err := pipeline.LoadManifest(bg(), n.store, entry)
	if err != nil || m.K == 0 || len(m.Chunks) == 0 {
		done()
		return
	}
	p := erasure.Params{K: m.K, N: m.N}
	refs := storedShards(m, p)

	// Manifest chunks first: they have no parity, so the caretaker's
	// local copy is their only spare. Probe REMOTE availability (a
	// local copy would mask the swarm having lost it) and re-seed any
	// chunk the swarm no longer holds.
	n.healManifest(entry.ManifestChunks, 0, func() {
		reachable := make(map[ports.ChunkID]bool, len(refs))
		var probeNext func(i int)
		probeNext = func(i int) {
			if i == len(refs) {
				n.repairStripes(m, p, refs, reachable, 0, done)
				return
			}
			n.probeShard(refs[i].id, true, func(ok bool) {
				reachable[refs[i].id] = ok
				probeNext(i + 1)
			})
		}
		probeNext(0)
	})
}

func (n *Node) healManifest(ids []ports.ChunkID, i int, done func()) {
	if i == len(ids) {
		done()
		return
	}
	id := ids[i]
	next := func() { n.healManifest(ids, i+1, done) }
	n.probeShard(id, false, func(ok bool) {
		if ok {
			next()
			return
		}
		c, err := n.store.Get(bg(), id)
		if err != nil {
			next() // we lost our copy too; nothing to re-seed from
			return
		}
		n.IterativeFindNode(id, func(closest []ports.NodeID) {
			n.placeAt(id, c.Data, nil, closest, n.cfg.Replication, nil, func(placed int) {
				if placed > 0 {
					n.Stats.ShardsRebuilt++
				}
				next()
			})
		})
	})
}

// storedShards lists every shard that physically exists for the file,
// in stripe order. (A short final stripe stores fewer than n — its
// implicit zero shards are math, not storage, and never need repair.)
func storedShards(m *manifest.Manifest, p erasure.Params) []shardRef {
	dataIDs, parityIDs := m.ChunkIDs(), m.ParityIDs()
	var refs []shardRef
	for j := 0; j < p.Stripes(len(dataIDs)); j++ {
		lo, hi := j*p.K, min((j+1)*p.K, len(dataIDs))
		for i, id := range dataIDs[lo:hi] {
			refs = append(refs, shardRef{id: id, stripe: j, pos: i, leafIdx: lo + i})
		}
		for q, id := range parityIDs[j*p.ParityShards() : (j+1)*p.ParityShards()] {
			refs = append(refs, shardRef{
				id: id, stripe: j, pos: p.K + q,
				leafIdx: len(dataIDs) + j*p.ParityShards() + q,
			})
		}
	}
	return refs
}

// probeShard answers "does anyone have this chunk?" via provider
// records confirmed with a cheap HasChunk round-trip. includeLocal
// counts our own store as availability; the manifest-heal path sets it
// false to ask specifically whether the SWARM still holds the chunk.
func (n *Node) probeShard(id ports.ChunkID, includeLocal bool, done func(bool)) {
	n.Stats.Probes++
	if includeLocal {
		if ok, _ := n.store.Has(bg(), id); ok {
			done(true)
			return
		}
	}
	n.resolveProviders(id, func(provs []ports.NodeID) {
		var try func(i int)
		try = func(i int) {
			if i >= len(provs) {
				done(false)
				return
			}
			if provs[i] == n.id {
				try(i + 1)
				return
			}
			n.request(provs[i], ports.Message{Kind: ports.MsgHasChunk, ChunkID: id},
				func(resp ports.Message, err error) {
					if err == nil && resp.Found {
						done(true)
						return
					}
					try(i + 1)
				})
		}
		try(0)
	})
}

// repairStripes walks the stripes, repairing each one whose losses
// exceed the slack.
func (n *Node) repairStripes(m *manifest.Manifest, p erasure.Params, refs []shardRef,
	reachable map[ports.ChunkID]bool, stripe int, done func()) {

	numStripes := p.Stripes(len(m.Chunks))
	if stripe == numStripes {
		done()
		return
	}
	var stripeRefs []shardRef
	missing := 0
	for _, r := range refs {
		if r.stripe == stripe {
			stripeRefs = append(stripeRefs, r)
			if !reachable[r.id] {
				missing++
			}
		}
	}
	next := func() { n.repairStripes(m, p, refs, reachable, stripe+1, done) }
	if missing == 0 || missing <= n.cfg.RepairSlack {
		next()
		return
	}
	n.repairStripe(m, p, stripeRefs, next)
}

// repairStripe fetches whatever shards of the stripe it can get —
// deliberately trying ALL of them, since the probe map may be stale by
// the time we act — reconstructs the rest, verifies every rebuilt shard
// against the manifest's hashes, and re-distributes the missing ones to
// fresh nodes. The caretaker keeps nothing afterward — it's a
// paramedic, not a hoarder. A failed attempt (below k fetchable) is
// counted and simply retried on the next sweep.
func (n *Node) repairStripe(m *manifest.Manifest, p erasure.Params, stripeRefs []shardRef, done func()) {
	leaves := m.Leaves()
	root := m.Root()
	ids := make([]ports.ChunkID, len(stripeRefs))
	realData := 0
	for i, r := range stripeRefs {
		ids[i] = r.id
		if r.pos < p.K {
			realData++
		}
	}
	n.fetchAll(ids, func(unfetched []ports.ChunkID) {
		// Build the stripe from whatever actually arrived.
		shards := make([][]byte, p.N)
		for _, r := range stripeRefs {
			if c, err := n.store.Get(bg(), r.id); err == nil {
				shards[r.pos] = c.Data
			}
		}
		cleanup := func() {
			for _, r := range stripeRefs {
				n.store.Delete(bg(), r.id)
			}
			done()
		}
		if err := erasure.ReconstructStripe(p, shards, realData); err != nil {
			n.Stats.RepairFailures++ // below k fetchable right now; retry next sweep
			cleanup()
			return
		}
		// Trust the math, verify the bytes: every rebuilt shard must
		// hash to the ID the Merkle root committed to.
		for _, r := range stripeRefs {
			if ports.HashBytes(shards[r.pos]) != r.id {
				n.Stats.RepairFailures++
				cleanup()
				return
			}
		}
		n.Stats.Repairs++
		missing := make(map[ports.ChunkID]bool, len(unfetched))
		for _, id := range unfetched {
			missing[id] = true
		}
		var toPlace []shardRef
		for _, r := range stripeRefs {
			if missing[r.id] {
				toPlace = append(toPlace, r)
			}
		}
		var place func(i int)
		place = func(i int) {
			if i == len(toPlace) {
				cleanup()
				return
			}
			r := toPlace[i]
			var proof *ports.StorageProof
			if pr, perr := manifest.Prove(leaves, r.leafIdx); perr == nil {
				proof = &ports.StorageProof{Root: root, Index: pr.Index, Total: pr.Total, Path: pr.Path}
			}
			n.IterativeFindNode(r.id, func(closest []ports.NodeID) {
				n.placeAt(r.id, shards[r.pos], proof, closest, n.cfg.Replication, nil, func(placed int) {
					if placed > 0 {
						n.Stats.ShardsRebuilt++
					}
					place(i + 1)
				})
			})
		}
		place(0)
	})
}
