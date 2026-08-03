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
	"github.com/nerolabs/silt/core/dht"
	"github.com/nerolabs/silt/core/erasure"
	"github.com/nerolabs/silt/core/link"
	"github.com/nerolabs/silt/core/manifest"
	"github.com/nerolabs/silt/core/pipeline"
	"github.com/nerolabs/silt/core/por"
	"github.com/nerolabs/silt/ports"
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
func (n *Node) Care(reg ports.Registry, ch link.CareHandle) {
	n.reg = reg
	n.care = append(n.care, ch)
	if !n.repairRunning {
		n.repairRunning = true
		n.clock.AfterFunc(n.cfg.RepairInterval, n.repairTick)
	}
	entry, ok, err := reg.Lookup(bg(), ch.Root)
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
	// Announce under each chunk's PLACEMENT key — its column key when
	// coded, its own id otherwise — since that's where readers look. A
	// whole column shares one key, so we announce it once.
	seen := make(map[ports.Hash]bool)
	var keys []ports.Hash
	held := 0
	for _, id := range ids {
		if n.chunkDenied(id) {
			continue // don't advertise taken-down content
		}
		key := ports.Hash(id)
		if p, ok := n.proofs[id]; ok {
			key = placementKey(p.Root, id, p.Column)
		}
		n.provs.Add(key, n.id)
		held++
		if !seen[key] {
			seen[key] = true
			keys = append(keys, key)
		}
	}
	n.announceAll(keys, func() { done(held) })
}

// announceAll plants "I have this" records on the K closest nodes to each
// placement key.
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
	handles := append([]link.CareHandle(nil), n.care...)
	var nextRoot func(i int)
	nextRoot = func(i int) {
		if i == len(handles) {
			n.clock.AfterFunc(n.cfg.RepairInterval, n.repairTick)
			return
		}
		n.repairRoot(handles[i], func() { nextRoot(i + 1) })
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

func (n *Node) repairRoot(ch link.CareHandle, done func()) {
	if n.isDenied(ch.Root) {
		done() // taken down: stop keeping it alive
		return
	}
	entry, ok, err := n.reg.Lookup(bg(), ch.Root)
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
		n.repairRootWithLayout(entry, ch, done)
	})
}

// repairRootWithLayout is where the M11 privilege separation earns its
// keep: everything below runs on the LAYOUT alone. A caretaker repairs
// stripes, verifies rebuilt shards against the Merkle root, and re-seeds
// the swarm — while the content keys, and the bytes they unlock, remain
// sealed to it.
func (n *Node) repairRootWithLayout(entry ports.Entry, ch link.CareHandle, done func()) {
	m, err := pipeline.LoadLayout(bg(), n.store, entry, ch)
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
		// shardDoms records the failure domains each shard's providers span,
		// so repairStripes can spot a stripe whose shards have concentrated
		// into too few domains (the dispersion audit) even when the raw
		// count is fine.
		shardDoms := make(map[ports.ChunkID]map[uint64]bool, len(refs))
		var probeNext func(i int)
		probeNext = func(i int) {
			if i == len(refs) {
				n.repairStripes(m, p, refs, reachable, shardDoms, 0, DerivePorKey(ch.LayoutKey), done)
				return
			}
			n.probeShard(refs[i].id, colKey(m.Root(), refs[i].pos), true, func(ok bool, domains map[uint64]bool) {
				reachable[refs[i].id] = ok
				if ok {
					shardDoms[refs[i].id] = domains
				}
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
	n.probeShard(id, id, false, func(ok bool, _ map[uint64]bool) {
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
func storedShards(m *manifest.Layout, p erasure.Params) []shardRef {
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

// probeShard answers "does anyone have this chunk?" and reports the set
// of failure domains that actually HOLD it (each provider confirmed with a
// HasChunk round-trip — a bare provider record isn't trusted, so a stale
// record can't fool the dispersion audit into thinking a column is spread
// when it isn't). The audit uses that set to tell a column living in one
// domain (fragile) from one already across two (safe), and to see the
// difference after it adds a copy. includeLocal counts our own store; the
// manifest-heal path sets it false to ask whether the SWARM still holds it.
func (n *Node) probeShard(id ports.ChunkID, key ports.Hash, includeLocal bool, done func(ok bool, domains map[uint64]bool)) {
	n.Stats.Probes++
	domains := map[uint64]bool{}
	found := false
	if includeLocal {
		if ok, _ := n.store.Has(bg(), id); ok {
			found = true
			if n.domainID != 0 {
				domains[n.domainID] = true
			}
		}
	}
	n.resolveProviders(key, func(provs []ports.NodeID) {
		var try func(i int)
		try = func(i int) {
			if i >= len(provs) {
				done(found, domains)
				return
			}
			if provs[i] == n.id {
				try(i + 1)
				return
			}
			pr := provs[i]
			n.request(pr, ports.Message{Kind: ports.MsgHasChunk, ChunkID: id},
				func(resp ports.Message, err error) {
					if err == nil && resp.Found {
						found = true
						if d := n.domainOf(pr); d != 0 {
							domains[d] = true
						}
					}
					try(i + 1)
				})
		}
		try(0)
	})
}

// repairStripes walks the stripes, repairing each one whose losses exceed
// the slack — and, as the dispersion audit, also re-spreading any stripe
// whose reachable shards have concentrated so far into one failure domain
// that losing that domain would drop it below k (the same n-k budget that
// guards shard loss, applied to domain loss).
func (n *Node) repairStripes(m *manifest.Layout, p erasure.Params, refs []shardRef,
	reachable map[ports.ChunkID]bool, shardDoms map[ports.ChunkID]map[uint64]bool, stripe int, porKey *por.Key, done func()) {

	numStripes := p.Stripes(len(m.Chunks))
	if stripe == numStripes {
		done()
		return
	}
	var stripeRefs []shardRef
	missing := 0
	// exclusive[d] counts this stripe's columns whose only known domain is
	// d — those a failure of domain d would take out. A column already
	// spread across ≥2 domains survives any single one and isn't counted.
	exclusive := map[uint64]int{}
	for _, r := range refs {
		if r.stripe == stripe {
			stripeRefs = append(stripeRefs, r)
			if !reachable[r.id] {
				missing++
			} else if doms := shardDoms[r.id]; len(doms) == 1 {
				for d := range doms {
					exclusive[d]++
				}
			}
		}
	}
	worst, dominant := 0, uint64(0)
	for d, c := range exclusive {
		if c > worst {
			worst, dominant = c, d
		}
	}
	// Losing the dominant domain would cost more than the n-k budget →
	// re-spread its exclusive columns even though the shard count is fine.
	var disperseShards map[ports.ChunkID]bool
	if worst > p.N-p.K {
		disperseShards = map[ports.ChunkID]bool{}
		for _, r := range stripeRefs {
			if doms := shardDoms[r.id]; len(doms) == 1 && doms[dominant] {
				disperseShards[r.id] = true
			}
		}
	}

	next := func() { n.repairStripes(m, p, refs, reachable, shardDoms, stripe+1, porKey, done) }
	if missing > n.cfg.RepairSlack || len(disperseShards) > 0 {
		if len(disperseShards) > 0 && missing <= n.cfg.RepairSlack {
			n.Stats.Dispersals++
			n.logf(ports.LogInfo, "dispersion re-spread", "root", m.Root(), "overexposed", len(disperseShards))
		}
		n.repairStripe(m, p, stripeRefs, disperseShards, dominant, porKey, next)
		return
	}
	// Degraded but still within the repair slack: parity/replication still
	// covers the loss, so reconstructing now would be premature — but narrate
	// it, so an operator who just killed a holder sees the caretaker NOTICE the
	// loss rather than apparent silence (acceptance F2). Repair fires only once
	// losses exceed the slack, which with the default replication takes more
	// than "a couple" of deaths.
	if missing > 0 {
		n.logf(ports.LogInfo, "stripe degraded, within repair slack — watching",
			"root", m.Root(), "stripe", stripe, "missing", missing, "slack", n.cfg.RepairSlack)
	}
	next()
}

// repairStripe fetches whatever shards of the stripe it can get —
// deliberately trying ALL of them, since the probe map may be stale by
// the time we act — reconstructs the rest, verifies every rebuilt shard
// against the manifest's hashes, and re-distributes the missing ones to
// fresh nodes. The caretaker keeps nothing afterward — it's a
// paramedic, not a hoarder. A failed attempt (below k fetchable) is
// counted and simply retried on the next sweep.
func (n *Node) repairStripe(m *manifest.Layout, p erasure.Params, stripeRefs []shardRef, disperseShards map[ports.ChunkID]bool, avoidDomain uint64, porKey *por.Key, done func()) {
	leaves := m.Leaves()
	root := m.Root()
	realData := 0
	for _, r := range stripeRefs {
		if r.pos < p.K {
			realData++
		}
	}
	// What we already hold as a provider (a caretaker can be the closest
	// node to a column key, making it the legitimate host of that column)
	// must survive the paramedic cleanup below — otherwise repair would
	// delete the very shards it is meant to preserve. Only the copies we
	// fetch for THIS repair are temporary.
	heldBefore := make(map[ports.ChunkID]bool, len(stripeRefs))
	for _, r := range stripeRefs {
		if ok, _ := n.store.Has(bg(), r.id); ok {
			heldBefore[r.id] = true
		}
	}
	// Fetch by column: a stripe's shards register under their column key,
	// not their own id, so a plain fetchAll (which resolves by id) would
	// find nothing and every repair would fail to reconstruct.
	n.fetchStripeByColumn(root, stripeRefs, func(unfetched []ports.ChunkID, usedDomains map[uint64]int) {
		// Build the stripe from whatever actually arrived.
		shards := make([][]byte, p.N)
		for _, r := range stripeRefs {
			if c, err := n.store.Get(bg(), r.id); err == nil {
				shards[r.pos] = c.Data
			}
		}
		cleanup := func() {
			for _, r := range stripeRefs {
				if !heldBefore[r.id] { // keep what we host; drop only fetched copies
					n.dropHosted(r.id)
				}
			}
			done()
		}
		if err := erasure.ReconstructStripe(p, shards, realData); err != nil {
			n.Stats.RepairFailures++ // below k fetchable right now; retry next sweep
			n.logf(ports.LogWarn, "repair below k", "root", root, "err", err)
			cleanup()
			return
		}
		// Trust the math, verify the bytes: every rebuilt shard must
		// hash to the ID the Merkle root committed to.
		for _, r := range stripeRefs {
			if ports.HashBytes(shards[r.pos]) != r.id {
				n.Stats.RepairFailures++
				n.logf(ports.LogWarn, "rebuilt shard hash mismatch", "root", root, "shard", r.id)
				cleanup()
				return
			}
		}
		n.Stats.Repairs++
		n.logf(ports.LogInfo, "stripe repaired", "root", root, "missing", len(unfetched))
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
		// Re-seed each rebuilt shard onto its COLUMN — the nodes closest to
		// colKey(root, pos) — so it rejoins the hosts its column-siblings
		// already live on. Within-column anti-affinity is then structural.
		// Steer the rebuilt columns into failure domains the surviving
		// columns aren't already using (usedDomains, seeded from the
		// survivors), so repair rebuilds spread rather than converging onto
		// a few hosts as churn shrinks the network. (r.pos is the shard's
		// column: 0..k-1 data, k..n-1 parity.)
		// Dispersion audit: after rebuilding, place ONE extra copy of each
		// over-exposed column (those living in only the crowded domain) on a
		// domain the stripe isn't already using. The pinned column stays put;
		// this just adds diversity, so next sweep sees it spread across ≥2
		// domains and stops flagging it.
		var toSpread []shardRef
		for _, r := range stripeRefs {
			if disperseShards[r.id] && shards[r.pos] != nil {
				toSpread = append(toSpread, r)
			}
		}
		var spread func(i int)
		spread = func(i int) {
			if i == len(toSpread) {
				cleanup()
				return
			}
			r := toSpread[i]
			var proof *ports.StorageProof
			if pr, perr := manifest.Prove(leaves, r.leafIdx); perr == nil {
				proof = &ports.StorageProof{Root: root, Index: pr.Index, Total: pr.Total, Path: pr.Path, Column: r.pos}
				if porKey != nil {
					proof.PorTags = porKey.Tags(r.id[:], shards[r.pos])
				}
			}
			n.IterativeFindNode(colKey(root, r.pos), func(closest []ports.NodeID) {
				// Steer the extra copy AWAY from the crowded domain specifically,
				// so an over-exposed column gains a holder in a different one.
				candidates := n.preferFreshDomain(closest, map[uint64]int{avoidDomain: 1})
				n.placeAt(r.id, shards[r.pos], proof, candidates, 1, nil,
					func(int) { spread(i + 1) })
			})
		}

		var place func(i int)
		place = func(i int) {
			if i == len(toPlace) {
				spread(0)
				return
			}
			r := toPlace[i]
			var proof *ports.StorageProof
			if pr, perr := manifest.Prove(leaves, r.leafIdx); perr == nil {
				proof = &ports.StorageProof{Root: root, Index: pr.Index, Total: pr.Total, Path: pr.Path, Column: r.pos}
				if porKey != nil {
					proof.PorTags = porKey.Tags(r.id[:], shards[r.pos])
				}
			}
			n.IterativeFindNode(colKey(root, r.pos), func(closest []ports.NodeID) {
				candidates := n.preferFreshDomain(closest, usedDomains)
				n.placeAt(r.id, shards[r.pos], proof, candidates, n.cfg.Replication,
					func(target ports.NodeID) {
						if d := n.domainOf(target); d != 0 {
							usedDomains[d]++
						}
					},
					func(placed int) {
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
