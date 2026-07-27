// File-level behaviors: pushing a freshly-added file out into the swarm
// (Distribute) and pulling one back from it (NetGet).
//
// NetGet's trick is reuse: it does the networking — resolve providers,
// fetch chunks, verify each on receipt — into the node's LOCAL store,
// then hands the final assembly (Merkle verification, erasure repair,
// decryption, joining) to pipeline.Get, the exact same code path the
// single-store CLI uses. The network layer moves bytes; the pipeline
// decides if they're true.
package node

import (
	"fmt"
	"io"
	"sort"

	"github.com/nerolabs/silt/core/dht"
	"github.com/nerolabs/silt/core/erasure"
	"github.com/nerolabs/silt/core/link"
	"github.com/nerolabs/silt/core/manifest"
	"github.com/nerolabs/silt/core/pipeline"
	"github.com/nerolabs/silt/ports"
)

// Distribute pushes every chunk of a locally-added file out to the swarm,
// making the receiving nodes providers. An erasure-coded file is placed
// by COLUMN — all shards of one shard-position across every stripe land
// together on the cfg.Replication nodes closest to that column's key
// (hash(root‖col)), so one host holds one shard of each stripe and a
// reader finds a whole column in one lookup. Manifest chunks and uncoded
// files fall back to per-chunk placement under each chunk's own id. If
// keepLocal is false the local copies are deleted afterward: the publisher
// walks away and the swarm alone carries the file. done receives the
// number of chunk-replica placements that succeeded.
func (n *Node) Distribute(entry ports.Entry, m *manifest.Manifest, keepLocal bool, done func(placed int)) {
	n.distributeFrom(n.store, entry, m, keepLocal, done)
}

// DistributeFrom scatters a file staged in an external scratch store —
// how the daemon's UI publishes without the staging ever touching the
// node's storage pledge (the M9 rule: pledges bound hosting, not
// staging). The scratch copies are deleted as they ship.
func (n *Node) DistributeFrom(src ports.ChunkStore, entry ports.Entry, m *manifest.Manifest, done func(placed int)) {
	n.distributeFrom(src, entry, m, false, done)
}

func (n *Node) distributeFrom(src ports.ChunkStore, entry ports.Entry, m *manifest.Manifest, keepLocal bool, done func(placed int)) {
	leaves := m.Leaves()
	root := m.Root()
	manifestN := len(entry.ManifestChunks)
	ids := append(append([]ports.ChunkID{}, entry.ManifestChunks...), leaves...)
	placed := 0
	// usedDomains counts how many of this file's columns already live in
	// each failure domain, so the next column prefers a domain not yet
	// used — spreading columns across AS/rack/geo/operator, not just node
	// IDs, so one domain failing costs a stripe at most one shard.
	usedDomains := map[uint64]int{}

	// Placement groups: each is one DHT key and the chunks that share it.
	// Manifest chunks and uncoded data are one-per-group under their own
	// id; an erasure-coded file's shards group by COLUMN under colKey, so
	// a whole column lands on the same hosts — one shard per stripe each,
	// making anti-affinity structural rather than a placement heuristic.
	type group struct {
		key     ports.Hash
		members []int // indices into ids
		column  bool  // a coded column (domain-spread); vs manifest/uncoded
	}
	var groups []group
	for i := 0; i < manifestN; i++ {
		groups = append(groups, group{key: ids[i], members: []int{i}})
	}
	if m.K == 0 {
		for i := manifestN; i < len(ids); i++ {
			groups = append(groups, group{key: ids[i], members: []int{i}})
		}
	} else {
		byCol := map[int][]int{}
		var cols []int
		for i := manifestN; i < len(ids); i++ {
			col := columnOfLeaf(m, i-manifestN)
			if _, seen := byCol[col]; !seen {
				cols = append(cols, col)
			}
			byCol[col] = append(byCol[col], i)
		}
		sort.Ints(cols) // deterministic order
		for _, col := range cols {
			groups = append(groups, group{key: colKey(root, col), members: byCol[col], column: true})
		}
	}

	var nextGroup func(g int)
	nextGroup = func(g int) {
		if g == len(groups) {
			n.logf(ports.LogInfo, "file distributed", "root", root, "chunks", len(ids), "placements", placed)
			done(placed)
			return
		}
		grp := groups[g]
		n.IterativeFindNode(grp.key, func(closest []ports.NodeID) {
			// Steer a coded column onto a domain no other column has used
			// yet. Manifest chunks and uncoded files place on raw closest —
			// they carry no column anti-affinity to preserve.
			candidates := closest
			if grp.column {
				candidates = n.preferFreshDomain(closest, usedDomains)
			}
			var nextMember func(k int)
			nextMember = func(k int) {
				if k == len(grp.members) {
					nextGroup(g + 1)
					return
				}
				id := ids[grp.members[k]]
				c, err := src.Get(bg(), id)
				if err != nil { // convergent dedup can mean it already shipped
					nextMember(k + 1)
					return
				}
				// Shards travel with their Merkle inclusion proof (so hosts
				// can answer storage challenges) tagged with their column;
				// manifest chunks aren't tree leaves and go bare.
				var proof *ports.StorageProof
				if li := grp.members[k] - manifestN; li >= 0 {
					if p, perr := manifest.Prove(leaves, li); perr == nil {
						proof = &ports.StorageProof{Root: root, Index: p.Index,
							Total: p.Total, Path: p.Path, Column: columnOfLeaf(m, li)}
					}
				}
				n.placeAt(id, c.Data, proof, candidates, n.cfg.Replication,
					func(target ports.NodeID) {
						placed++
						if grp.column {
							if d := n.domainOf(target); d != 0 {
								usedDomains[d]++
							}
						}
					},
					func(int) {
						if !keepLocal {
							src.Delete(bg(), id)
						}
						nextMember(k + 1)
					})
			}
			nextMember(0)
		})
	}
	nextGroup(0)
}

// placeAt walks candidates in order (skipping self) until want have
// accepted or the list runs out. A full node's refusal (ErrStoreFull →
// OK=false) just means the next candidate gets asked — spill-over is
// how a capacity-bounded network fills evenly. accepted (optional)
// fires per accepting node; done receives the acceptance count.
func (n *Node) placeAt(id ports.ChunkID, data []byte, proof *ports.StorageProof,
	candidates []ports.NodeID, want int, accepted func(ports.NodeID), done func(placed int)) {

	placed := 0
	var try func(i int)
	try = func(i int) {
		if placed >= want || i >= len(candidates) {
			done(placed)
			return
		}
		target := candidates[i]
		if target == n.id {
			try(i + 1)
			return
		}
		msg := ports.Message{Kind: ports.MsgStoreChunk, ChunkID: id, Data: data, Proof: proof}
		n.request(target, msg, func(resp ports.Message, err error) {
			if err == nil && resp.OK {
				placed++
				if accepted != nil {
					accepted(target)
				}
			}
			try(i + 1)
		})
	}
	try(0)
}

// resolveProviders finds nodes claiming to hold id: local records plus
// a GetProviders walk toward the key, run to convergence so EVERY
// discoverable record is collected. An earlier version stopped at the
// first record found — and the audit scenario's liars exposed that as a
// real fragility: the one record you stop on may be a fake provider
// while honest replicas sit undiscovered. Results are deduped and
// sorted by distance to the key so retrieval order is deterministic.
func (n *Node) resolveProviders(id ports.ChunkID, done func([]ports.NodeID)) {
	seen := make(map[ports.NodeID]bool)
	var acc []ports.NodeID
	add := func(ids []ports.NodeID) {
		for _, p := range ids {
			if !seen[p] {
				seen[p] = true
				acc = append(acc, p)
			}
		}
	}
	add(n.provs.Get(id))
	w := n.newWalk(ports.MsgGetProviders, id,
		func(ps []ports.NodeID) bool {
			add(ps)
			return false // keep walking: we want all records, not the first
		},
		func([]ports.NodeID) {
			dht.SortByDistance(id, acc)
			done(acc)
		})
	w.step()
}

// fetchFrom pulls one chunk from a known provider set into the local
// store, trying them in order and hash-verifying every byte before it is
// kept (a provider serving garbage is just skipped). Reports whether the
// chunk is now held. Shared by per-chunk and per-column fetch.
func (n *Node) fetchFrom(id ports.ChunkID, provs []ports.NodeID, done func(bool)) {
	if ok, _ := n.store.Has(bg(), id); ok {
		done(true)
		return
	}
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
		n.request(provs[i], ports.Message{Kind: ports.MsgFetchChunk, ChunkID: id},
			func(resp ports.Message, err error) {
				if err == nil && resp.Found {
					c := ports.Chunk{ID: id, Data: resp.Data}
					if c.Verify() && n.store.Put(bg(), c) == nil { // a node that trusts is a bug
						done(true)
						return
					}
				}
				try(i + 1)
			})
	}
	try(0)
}

// FetchChunk gets one chunk into the local store, resolving its providers
// by its own id (manifest chunks and uncoded files). Column shards are
// fetched via fetchColumn instead.
func (n *Node) FetchChunk(id ports.ChunkID, done func(error)) {
	if ok, _ := n.store.Has(bg(), id); ok {
		done(nil)
		return
	}
	n.resolveProviders(id, func(provs []ports.NodeID) {
		n.fetchFrom(id, provs, func(ok bool) {
			if ok {
				done(nil)
				return
			}
			done(fmt.Errorf("chunk %s: no reachable provider (of %d known)", id, len(provs)))
		})
	})
}

// fetchColumn resolves a column's providers once and pulls every shard of
// that column from them — the whole column in a single lookup instead of
// one lookup per shard. Reports which ids couldn't be fetched.
func (n *Node) fetchColumn(root ports.Hash, col int, ids []ports.ChunkID, done func(missing []ports.ChunkID)) {
	n.resolveProviders(colKey(root, col), func(provs []ports.NodeID) {
		var missing []ports.ChunkID
		var next func(i int)
		next = func(i int) {
			if i == len(ids) {
				done(missing)
				return
			}
			n.fetchFrom(ids[i], provs, func(ok bool) {
				if !ok {
					missing = append(missing, ids[i])
				}
				next(i + 1)
			})
		}
		next(0)
	})
}

// fetchStripeByColumn pulls each shard of one stripe from its own
// column's providers (each shard sits in a different column), verifying
// on receipt. Reports which ids couldn't be fetched, plus the failure
// domains the surviving columns live in — so repair can re-seed the
// rebuilt columns into domains the survivors aren't already using.
func (n *Node) fetchStripeByColumn(root ports.Hash, refs []shardRef, done func(unfetched []ports.ChunkID, usedDomains map[uint64]int)) {
	var unfetched []ports.ChunkID
	usedDomains := map[uint64]int{}
	var next func(i int)
	next = func(i int) {
		if i == len(refs) {
			done(unfetched, usedDomains)
			return
		}
		r := refs[i]
		n.resolveProviders(colKey(root, r.pos), func(provs []ports.NodeID) {
			n.fetchFrom(r.id, provs, func(ok bool) {
				if !ok {
					unfetched = append(unfetched, r.id)
				} else {
					for _, p := range provs { // note the surviving column's domain
						if d := n.domainOf(p); d != 0 {
							usedDomains[d]++
							break
						}
					}
				}
				next(i + 1)
			})
		})
	}
	next(0)
}

// columnsOf groups a manifest's shard ids by column (0..n-1), each list
// in stripe order — the shape retrieval and repair fetch in.
func columnsOf(m *manifest.Manifest) map[int][]ports.ChunkID {
	cols := map[int][]ports.ChunkID{}
	for li, id := range m.Leaves() {
		j := columnOfLeaf(m, li)
		cols[j] = append(cols[j], id)
	}
	return cols
}

// fetchAll fetches ids sequentially, reporting which ones could not be
// retrieved. Sequential keeps ordering deterministic and the code
// simple; M3 files are small. (Pipelining is a later optimization.)
func (n *Node) fetchAll(ids []ports.ChunkID, done func(missing []ports.ChunkID)) {
	var missing []ports.ChunkID
	var next func(i int)
	next = func(i int) {
		if i == len(ids) {
			done(missing)
			return
		}
		n.FetchChunk(ids[i], func(err error) {
			if err != nil {
				missing = append(missing, ids[i])
			}
			next(i + 1)
		})
	}
	next(0)
}

// NetGet retrieves the file named root from the swarm and writes it to
// w. Phases: manifest chunks (must all arrive — they have no parity in
// v1), then data shards (misses tolerated), then parity shards for any
// stripe that has misses. Final verification/repair/decryption is
// pipeline.Get against the local store.
func (n *Node) NetGet(reg ports.Registry, h link.Handle, w io.Writer, done func(error)) {
	entry, ok, err := reg.Lookup(bg(), h.Root)
	if err != nil || !ok {
		done(fmt.Errorf("netget %s: %w", h.Root, ports.ErrNoSuchEntry))
		return
	}
	n.fetchAll(entry.ManifestChunks, func(missing []ports.ChunkID) {
		if len(missing) > 0 {
			done(fmt.Errorf("netget: %d of %d manifest chunks unreachable", len(missing), len(entry.ManifestChunks)))
			return
		}
		m, err := pipeline.LoadFull(bg(), n.store, entry, h)
		if err != nil {
			done(fmt.Errorf("netget: %w", err))
			return
		}
		// Whatever ends up in the local store, the pipeline is the judge:
		// it verifies every hash against the root and repairs from parity
		// where it can.
		finish := func() {
			err := pipeline.Get(bg(), n.store, reg, h, w)
			if err == nil {
				n.logf(ports.LogInfo, "file retrieved", "root", h.Root)
			}
			done(err)
		}

		if m.K == 0 { // uncoded: per-chunk, data then parity-on-demand
			n.fetchAll(m.ChunkIDs(), func(missingData []ports.ChunkID) {
				n.fetchAll(parityForMissing(m, missingData), func([]ports.ChunkID) { finish() })
			})
			return
		}

		// Erasure-coded: fetch by column. Pull the k data columns first;
		// only if a data shard is missing do we pull the parity columns and
		// let the pipeline reconstruct. Each column is one provider lookup.
		cols := columnsOf(m)
		fetchCols := func(list []int, after func()) {
			var next func(i int)
			next = func(i int) {
				if i == len(list) {
					after()
					return
				}
				n.fetchColumn(h.Root, list[i], cols[list[i]], func([]ports.ChunkID) { next(i + 1) })
			}
			next(0)
		}
		allData := func() bool {
			for _, id := range m.ChunkIDs() {
				if ok, _ := n.store.Has(bg(), id); !ok {
					return false
				}
			}
			return true
		}
		dataCols := make([]int, m.K)
		for j := range dataCols {
			dataCols[j] = j
		}
		fetchCols(dataCols, func() {
			if allData() {
				finish()
				return
			}
			parityCols := make([]int, 0, m.N-m.K)
			for j := m.K; j < m.N; j++ {
				parityCols = append(parityCols, j)
			}
			fetchCols(parityCols, finish)
		})
	})
}

// parityForMissing returns the parity shard IDs of every stripe that
// lost data chunks — fetched only on demand, since a healthy stripe
// never needs its parity.
func parityForMissing(m *manifest.Manifest, missing []ports.ChunkID) []ports.ChunkID {
	if m.K == 0 || len(missing) == 0 {
		return nil
	}
	lost := make(map[ports.ChunkID]bool, len(missing))
	for _, id := range missing {
		lost[id] = true
	}
	p := erasure.Params{K: m.K, N: m.N}
	dataIDs, parityIDs := m.ChunkIDs(), m.ParityIDs()
	var need []ports.ChunkID
	for j := 0; j < p.Stripes(len(dataIDs)); j++ {
		lo, hi := j*p.K, min((j+1)*p.K, len(dataIDs))
		for _, id := range dataIDs[lo:hi] {
			if lost[id] {
				need = append(need, parityIDs[j*p.ParityShards():(j+1)*p.ParityShards()]...)
				break
			}
		}
	}
	return need
}
