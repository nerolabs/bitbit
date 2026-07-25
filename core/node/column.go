package node

import (
	"encoding/binary"

	"github.com/nerolabs/silt/core/manifest"
	"github.com/nerolabs/silt/ports"
)

// Column placement: the unit of placement, provider records, and
// retrieval is a whole *column* — the shard at one position across every
// stripe — not an individual chunk. A column's shards are placed on the
// nodes closest to colKey(root, j) and register there as providers, so:
//   - one host holds one shard of each stripe (anti-affinity, structural
//     rather than heuristic — one host loss costs a stripe one shard),
//   - a reader finds the whole column in a single provider lookup instead
//     of one per chunk (S lookups collapse to 1 per column).
// Manifest chunks and uncoded files have no stripes, so they keep the
// per-chunk key (the chunk's own id); noColumn marks that case.

const noColumn = -1

// colKey is the DHT key a column's providers register under:
// hash(root ‖ 'c' ‖ j). The 'c' byte is a domain separator so a column
// key can never coincide with a real chunk hash's preimage.
func colKey(root ports.Hash, j int) ports.Hash {
	buf := make([]byte, 0, len(root)+1+8)
	buf = append(buf, root[:]...)
	buf = append(buf, 'c')
	var jb [8]byte
	binary.BigEndian.PutUint64(jb[:], uint64(j))
	buf = append(buf, jb[:]...)
	return ports.HashBytes(buf)
}

// columnAt maps a Merkle leaf index (data leaves in file order, then
// parity in stripe order — see manifest.Leaves) to the shard's column:
// its position 0..n-1 within a stripe. Data leaf i is at column i%k;
// parity leaf p at column k + p%(n-k). Returns noColumn when uncoded
// (k==0), which has no stripes. Works from the raw dimensions so both
// the full manifest (publish) and the content-free layout (audit/repair)
// can call it.
func columnAt(leafIdx, dataN, k, nn int) int {
	if k == 0 {
		return noColumn
	}
	if leafIdx < dataN {
		return leafIdx % k
	}
	return k + (leafIdx-dataN)%(nn-k)
}

// columnOfLeaf is columnAt for a full manifest.
func columnOfLeaf(m *manifest.Manifest, leafIdx int) int {
	return columnAt(leafIdx, len(m.Chunks), m.K, m.N)
}

// placementKey returns the key a chunk registers under and is found by:
// its column key when coded, else its own id.
func placementKey(root ports.Hash, id ports.ChunkID, column int) ports.Hash {
	if column == noColumn {
		return id
	}
	return colKey(root, column)
}
