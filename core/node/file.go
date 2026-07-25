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

// Distribute pushes every chunk of a locally-added file — manifest
// chunks, data shards, parity shards — onto the cfg.Replication closest
// nodes to each chunk's ID, making them providers. If keepLocal is
// false the local copies are deleted afterward: the publisher walks
// away and the swarm alone carries the file.
// done receives the number of chunk-replica placements that succeeded.
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
	ids := append(append([]ports.ChunkID{}, entry.ManifestChunks...), leaves...)
	placed := 0
	// stripeHosts counts how many shards of each stripe every node
	// holds, for anti-affinity: one node dying should cost a stripe as
	// little as possible, so shards of the same stripe repel each
	// other — and repel harder from nodes already doubled up.
	stripeHosts := make(map[int]map[ports.NodeID]int)

	var next func(i int)
	next = func(i int) {
		if i == len(ids) {
			done(placed)
			return
		}
		id := ids[i]
		c, err := src.Get(bg(), id)
		if err != nil { // convergent dedup can mean a chunk was already shipped and deleted
			next(i + 1)
			return
		}
		// Shards travel with their Merkle inclusion proof so hosts can
		// answer storage challenges. Manifest chunks aren't tree leaves
		// and go bare.
		var proof *ports.StorageProof
		stripe := -1 // manifest chunks have no stripe
		if li := i - len(entry.ManifestChunks); li >= 0 {
			if p, perr := manifest.Prove(leaves, li); perr == nil {
				proof = &ports.StorageProof{Root: root, Index: p.Index, Total: p.Total, Path: p.Path}
			}
			stripe = stripeOfLeaf(m, li)
		}
		n.IterativeFindNode(id, func(closest []ports.NodeID) {
			candidates := preferAvoiding(closest, stripeHosts[stripe])
			n.placeAt(id, c.Data, proof, candidates, n.cfg.Replication,
				func(target ports.NodeID) {
					placed++
					if stripe >= 0 {
						if stripeHosts[stripe] == nil {
							stripeHosts[stripe] = make(map[ports.NodeID]int)
						}
						stripeHosts[stripe][target]++
					}
				},
				func(int) {
					if !keepLocal {
						src.Delete(bg(), id)
					}
					next(i + 1)
				})
		})
	}
	next(0)
}

// stripeOfLeaf maps a Merkle leaf index (data leaves first, then
// parity, per manifest.Leaves) to its stripe number, or -1 if the
// manifest is uncoded.
func stripeOfLeaf(m *manifest.Manifest, li int) int {
	if m.K == 0 {
		return -1
	}
	if li < len(m.Chunks) {
		return li / m.K
	}
	return (li - len(m.Chunks)) / (m.N - m.K)
}

// preferAvoiding orders candidates by how many shards of this stripe
// they already hold (fewest first; XOR closeness breaks ties by
// preserving the incoming order). Anti-affinity as graded preference,
// not veto: a shard is still better on a doubled-up node than nowhere.
func preferAvoiding(candidates []ports.NodeID, held map[ports.NodeID]int) []ports.NodeID {
	if len(held) == 0 {
		return candidates
	}
	out := append([]ports.NodeID(nil), candidates...)
	sort.SliceStable(out, func(i, j int) bool { return held[out[i]] < held[out[j]] })
	return out
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

// FetchChunk gets one chunk into the local store, trying providers in
// order. Every received chunk is hash-verified before it is kept; a
// provider serving garbage is just skipped.
func (n *Node) FetchChunk(id ports.ChunkID, done func(error)) {
	if ok, _ := n.store.Has(bg(), id); ok {
		done(nil)
		return
	}
	n.resolveProviders(id, func(provs []ports.NodeID) {
		var try func(i int)
		try = func(i int) {
			if i >= len(provs) {
				done(fmt.Errorf("chunk %s: no reachable provider (of %d known)", id, len(provs)))
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
						if c.Verify() { // a node that trusts is a bug
							if n.store.Put(bg(), c) == nil {
								done(nil)
								return
							}
						}
					}
					try(i + 1)
				})
		}
		try(0)
	})
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
		dataIDs := m.ChunkIDs()
		n.fetchAll(dataIDs, func(missingData []ports.ChunkID) {
			parityNeeded := parityForMissing(m, missingData)
			n.fetchAll(parityNeeded, func([]ports.ChunkID) {
				// Whatever arrived, the pipeline is the judge: it
				// verifies every hash against the root and repairs
				// from parity where it can.
				done(pipeline.Get(bg(), n.store, reg, h, w))
			})
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
