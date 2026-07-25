// Package dht is textbook Kademlia, kept pure: XOR distance, k-bucket
// routing tables, and the iterative lookup as a standalone state machine
// that never touches a network. The node package drives these over the
// Transport port. See docs/math/04-kademlia.md.
package dht

import (
	"sort"

	"github.com/nerolabs/silt/ports"
)

// Distance is the Kademlia metric: XOR of the two IDs, compared as a
// big-endian 256-bit integer. It is a genuine metric (d(a,a)=0,
// symmetric, triangle inequality) with one magic extra property:
// d(a,b) = d(a,c) implies b = c — every point sees every other point at
// a distinct distance, so "the k closest nodes to X" is unambiguous.
func Distance(a, b ports.Hash) [32]byte {
	var d [32]byte
	for i := range d {
		d[i] = a[i] ^ b[i]
	}
	return d
}

// Closer reports whether a is strictly closer to target than b is.
func Closer(target ports.Hash, a, b ports.NodeID) bool {
	for i := 0; i < len(target); i++ {
		da, db := a[i]^target[i], b[i]^target[i]
		if da != db {
			return da < db
		}
	}
	return false
}

// BucketIndex returns which k-bucket id falls into from self's point of
// view: the index of the highest differing bit, 0..255 (255 = differ in
// the first bit = the far half of the ID space). Returns -1 for self.
func BucketIndex(self, id ports.NodeID) int {
	for i := 0; i < len(self); i++ {
		if x := self[i] ^ id[i]; x != 0 {
			hi := 0 // position of highest set bit within the byte
			for x > 1 {
				x >>= 1
				hi++
			}
			return (len(self)-1-i)*8 + hi
		}
	}
	return -1
}

// SortByDistance sorts ids by ascending XOR distance to target, in place.
func SortByDistance(target ports.Hash, ids []ports.NodeID) {
	sort.Slice(ids, func(i, j int) bool { return Closer(target, ids[i], ids[j]) })
}
