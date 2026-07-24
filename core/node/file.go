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

	"github.com/nerolabs/bitbit/core/dht"
	"github.com/nerolabs/bitbit/core/erasure"
	"github.com/nerolabs/bitbit/core/manifest"
	"github.com/nerolabs/bitbit/core/pipeline"
	"github.com/nerolabs/bitbit/ports"
)

// Distribute pushes every chunk of a locally-added file — manifest
// chunks, data shards, parity shards — onto the cfg.Replication closest
// nodes to each chunk's ID, making them providers. If keepLocal is
// false the local copies are deleted afterward: the publisher walks
// away and the swarm alone carries the file.
// done receives the number of chunk-replica placements that succeeded.
func (n *Node) Distribute(entry ports.Entry, m *manifest.Manifest, keepLocal bool, done func(placed int)) {
	leaves := m.Leaves()
	root := m.Root()
	ids := append(append([]ports.ChunkID{}, entry.ManifestChunks...), leaves...)
	placed := 0
	var next func(i int)
	next = func(i int) {
		if i == len(ids) {
			done(placed)
			return
		}
		id := ids[i]
		c, err := n.store.Get(bg(), id)
		if err != nil { // convergent dedup can mean a chunk was already shipped and deleted
			next(i + 1)
			return
		}
		// Shards travel with their Merkle inclusion proof so hosts can
		// answer storage challenges. Manifest chunks aren't tree leaves
		// and go bare.
		var proof *ports.StorageProof
		if li := i - len(entry.ManifestChunks); li >= 0 {
			if p, perr := manifest.Prove(leaves, li); perr == nil {
				proof = &ports.StorageProof{Root: root, Index: p.Index, Total: p.Total, Path: p.Path}
			}
		}
		n.IterativeFindNode(id, func(closest []ports.NodeID) {
			targets := n.pickTargets(closest)
			n.storeAt(id, c.Data, proof, targets, 0, &placed, func() {
				if !keepLocal {
					n.store.Delete(bg(), id)
				}
				next(i + 1)
			})
		})
	}
	next(0)
}

// pickTargets filters self out of a closest-nodes list and caps it at
// the replication factor.
func (n *Node) pickTargets(closest []ports.NodeID) []ports.NodeID {
	targets := make([]ports.NodeID, 0, n.cfg.Replication)
	for _, t := range closest {
		if t != n.id {
			targets = append(targets, t)
			if len(targets) == n.cfg.Replication {
				break
			}
		}
	}
	return targets
}

func (n *Node) storeAt(id ports.ChunkID, data []byte, proof *ports.StorageProof, targets []ports.NodeID, i int, placed *int, done func()) {
	if i == len(targets) {
		done()
		return
	}
	msg := ports.Message{Kind: ports.MsgStoreChunk, ChunkID: id, Data: data, Proof: proof}
	n.request(targets[i], msg, func(resp ports.Message, err error) {
		if err == nil && resp.OK {
			*placed++
		}
		n.storeAt(id, data, proof, targets, i+1, placed, done)
	})
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
func (n *Node) NetGet(reg ports.Registry, root ports.Hash, w io.Writer, done func(error)) {
	entry, ok, err := reg.Lookup(bg(), root)
	if err != nil || !ok {
		done(fmt.Errorf("netget %s: %w", root, ports.ErrNoSuchEntry))
		return
	}
	n.fetchAll(entry.ManifestChunks, func(missing []ports.ChunkID) {
		if len(missing) > 0 {
			done(fmt.Errorf("netget: %d of %d manifest chunks unreachable", len(missing), len(entry.ManifestChunks)))
			return
		}
		m, err := pipeline.LoadManifest(bg(), n.store, entry)
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
				done(pipeline.Get(bg(), n.store, reg, root, w))
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
