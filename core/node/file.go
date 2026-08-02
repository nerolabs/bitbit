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
	"github.com/nerolabs/silt/core/por"
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
// number of chunk-replica placements that succeeded and, per tenet B7 (no
// optimistic operations), a non-nil error if the file was not placed
// durably enough to be retrievable: any MANIFEST chunk (or any chunk of an
// uncoded file, which carries no parity) that landed on no node, OR any
// erasure STRIPE left with fewer placed shards than reconstruction needs
// (#64). A link is unretrievable in all three cases, so the caller must NOT
// register/return one for it.
func (n *Node) Distribute(entry ports.Entry, m *manifest.Manifest, keepLocal bool, porKey *por.Key, done func(placed int, err error)) {
	n.distributeFrom(n.store, entry, m, keepLocal, porKey, done)
}

// DistributeFrom scatters a file staged in an external scratch store —
// how the daemon's UI publishes without the staging ever touching the
// node's storage pledge (the M9 rule: pledges bound hosting, not
// staging). The scratch copies are deleted as they ship.
func (n *Node) DistributeFrom(src ports.ChunkStore, entry ports.Entry, m *manifest.Manifest, porKey *por.Key, done func(placed int, err error)) {
	n.distributeFrom(src, entry, m, false, porKey, done)
}

func (n *Node) distributeFrom(src ports.ChunkStore, entry ports.Entry, m *manifest.Manifest, keepLocal bool, porKey *por.Key, done func(placed int, err error)) {
	leaves := m.Leaves()
	root := m.Root()
	manifestN := len(entry.ManifestChunks)
	ids := append(append([]ports.ChunkID{}, entry.ManifestChunks...), leaves...)
	placed := 0
	// B7: a chunk we actively tried to place but no node accepted (all
	// candidates full or unreachable) can strand content behind an
	// unretrievable link. We track placement so publish fails loudly instead
	// of returning a dangling link. (A convergent-dedup SKIP — chunk already
	// in the swarm, not re-shipped — never reaches placeAt, so it can't trip
	// this.)
	var distErr error
	// shardPlaced[i] records whether ids[i] landed on at least one node, so
	// after distribution we can verify every erasure stripe kept enough
	// placed shards to be reconstructable (#64) — the data-shard analogue of
	// the required-chunk (manifest / uncoded) check below.
	shardPlaced := make([]bool, len(ids))
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
		key      ports.Hash
		members  []int // indices into ids
		column   bool  // a coded column (domain-spread); vs manifest/uncoded
		required bool  // no redundancy (manifest chunk or uncoded data): every one MUST place, or the file is unretrievable (B7)
	}
	var groups []group
	for i := 0; i < manifestN; i++ {
		groups = append(groups, group{key: ids[i], members: []int{i}, required: true})
	}
	if m.K == 0 {
		for i := manifestN; i < len(ids); i++ {
			groups = append(groups, group{key: ids[i], members: []int{i}, required: true})
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

	// A redundancy-free chunk (manifest / uncoded data) gets no second copy,
	// so a single transient placement failure — common on the relay path once
	// the nearest nodes cap out and load shifts onto NATed hosts — strands the
	// whole file; a coded column landing nowhere costs every stripe one shard.
	// Retry a group that lands NOWHERE, with a fresh lookup each time
	// (reachability may have recovered, or a still-open node surfaces), before
	// failing loud per B7.
	const placeAttempts = 4

	var nextGroup func(g, attempt int)
	nextGroup = func(g, attempt int) {
		if g == len(groups) {
			// B7 / #64: before returning a link, verify every erasure stripe
			// kept enough placed shards to reconstruct. Column placement means
			// a shard-position that landed on no node is missing from EVERY
			// stripe; if that leaves a stripe with fewer stored shards than it
			// needs, the file is unrecoverable and we must fail loud — never
			// register a link for content the swarm can't rebuild.
			if m.K != 0 && distErr == nil {
				if s, cnt, stored, need := understockedStripe(m, manifestN, shardPlaced); s >= 0 {
					distErr = fmt.Errorf("stripe %d unrecoverable: only %d of %d shards placed, need %d (network full or unreachable)",
						s, cnt, stored, need)
				}
			}
			n.logf(ports.LogInfo, "file distributed", "root", root, "chunks", len(ids), "placements", placed)
			done(placed, distErr)
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
			groupPlaced := 0
			var nextMember func(k int)
			nextMember = func(k int) {
				if k == len(grp.members) {
					// A group that landed nowhere: retry with a fresh lookup,
					// then decide. A REQUIRED group (manifest chunk / uncoded
					// data, no parity) failing is fatal on its own — fail loud
					// (B7). A coded COLUMN landing nowhere isn't necessarily
					// fatal (a stripe survives losing up to n-k shards), so we
					// retry it too but let the per-stripe check above be judge.
					if groupPlaced == 0 && (grp.required || grp.column) {
						if attempt+1 < placeAttempts {
							nextGroup(g, attempt+1)
							return
						}
						if grp.required && distErr == nil {
							kind := "manifest"
							if g >= manifestN {
								kind = "data"
							}
							distErr = fmt.Errorf("%s chunk %s placed on no node after %d attempts (network full or unreachable)",
								kind, ids[grp.members[0]], placeAttempts)
						}
					}
					nextGroup(g+1, 0)
					return
				}
				id := ids[grp.members[k]]
				c, err := src.Get(bg(), id)
				if err != nil { // convergent dedup can mean it already shipped
					nextMember(k + 1)
					return
				}
				// Shards travel with their Merkle inclusion proof (so hosts
				// can answer storage challenges) tagged with their column,
				// plus per-block PoR authenticators so an auditor can later
				// verify possession WITHOUT fetching the bytes; manifest
				// chunks aren't tree leaves, go bare, and aren't audited.
				var proof *ports.StorageProof
				if li := grp.members[k] - manifestN; li >= 0 {
					if p, perr := manifest.Prove(leaves, li); perr == nil {
						proof = &ports.StorageProof{Root: root, Index: p.Index,
							Total: p.Total, Path: p.Path, Column: columnOfLeaf(m, li)}
						if porKey != nil {
							proof.PorTags = porKey.Tags(id[:], c.Data)
						}
					}
				}
				n.placeAt(id, c.Data, proof, candidates, n.cfg.Replication,
					func(target ports.NodeID) {
						placed++
						groupPlaced++
						if grp.column {
							if d := n.domainOf(target); d != 0 {
								usedDomains[d]++
							}
						}
					},
					func(np int) {
						shardPlaced[grp.members[k]] = np > 0
						// Keep the local copy of any redundancy-free chunk or
						// coded shard that still has no home (a retry may yet
						// ship it, or the publish fails loud); otherwise the
						// publisher walks away and the swarm alone carries it.
						if !((grp.required || grp.column) && np == 0) && !keepLocal {
							src.Delete(bg(), id)
						}
						nextMember(k + 1)
					})
			}
			nextMember(0)
		})
	}
	nextGroup(0, 0)
}

// understockedStripe returns the first erasure stripe left with too few
// placed shards to reconstruct (or -1 if every stripe is safe), plus counts
// for a diagnostic. shardPlaced is indexed over ids = [manifest chunks ‖
// data leaves ‖ parity leaves]. A stripe reconstructs from any k of its n
// coded shards; in a short final stripe the k−r missing data positions are
// known-zero padding, free to the decoder, so that stripe needs only its r
// real-data-shard count placed among stored (real-data + parity) shards — a
// full stripe therefore needs k, the last may need fewer.
func understockedStripe(m *manifest.Manifest, manifestN int, shardPlaced []bool) (stripe, placed, stored, need int) {
	dataN, k, nn := len(m.Chunks), m.K, m.N
	parPer := nn - k
	stripes := (dataN + k - 1) / k
	for s := 0; s < stripes; s++ {
		r := k
		if rem := dataN - s*k; rem < r {
			r = rem
		}
		cnt := 0
		for i := s * k; i < s*k+r; i++ {
			if shardPlaced[manifestN+i] {
				cnt++
			}
		}
		for pj := s * parPer; pj < (s+1)*parPer; pj++ {
			if idx := manifestN + dataN + pj; idx < len(shardPlaced) && shardPlaced[idx] {
				cnt++
			}
		}
		if cnt < r {
			return s, cnt, r + parPer, r
		}
	}
	return -1, 0, 0, 0
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
//
// A whole sweep of the provider set can fail *transiently* rather than
// because nobody has the chunk: once the public rendezvous node caps out,
// every byte to a NATed provider funnels through the relay, whose per-peer
// splice slots saturate under concurrent fan-out and return "relay at
// capacity" (#65). Those slots free within moments, so a backed-off re-sweep
// usually succeeds — the fetch-side analogue of the #63 placement retry.
// We re-sweep only when at least one provider failed with a transport error
// (timeout / relay refusal); a sweep where every provider cleanly answered
// "don't have it" is a real miss and retrying it would just burn time.
func (n *Node) fetchFrom(id ports.ChunkID, provs []ports.NodeID, done func(bool)) {
	attempt := 0
	var sweep func()
	sweep = func() {
		if ok, _ := n.store.Has(bg(), id); ok {
			done(true)
			return
		}
		transient := false
		var try func(i int)
		try = func(i int) {
			if i >= len(provs) {
				attempt++
				if transient && attempt < n.cfg.FetchAttempts {
					n.clock.AfterFunc(ports.Duration(attempt)*n.cfg.FetchBackoff, sweep)
					return
				}
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
					if err != nil {
						transient = true // timeout or relay-at-capacity: worth a re-sweep
					}
					try(i + 1)
				})
		}
		try(0)
	}
	sweep()
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
