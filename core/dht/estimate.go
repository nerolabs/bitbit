package dht

import (
	"math/big"

	"github.com/nerolabs/silt/ports"
)

// EstimateNetworkSize infers how many nodes exist in the whole network
// from nothing but the distances to your m nearest known neighbors —
// counting a crowd you cannot see. See docs/math/06-counting-the-crowd.md.
//
// The idea: node IDs are uniform in a space of 2^256 points. If the
// m-th closest node to you sits at XOR distance d, then a ball of
// radius d around you contains m nodes, so the network's density is
// ≈ m/d nodes per unit of ID space, and the whole space holds
//
//	N ≈ m · 2^256 / d
//
// It only works if peers really are your m nearest — which is exactly
// what a Kademlia node's own neighborhood (self-lookup, close buckets)
// gives it. Accuracy is within a small constant factor, which is all
// capacity planning needs.
func EstimateNetworkSize(self ports.NodeID, peers []ports.NodeID, m int) float64 {
	if len(peers) == 0 {
		return 1 // alone in the universe, as far as we know
	}
	sorted := append([]ports.NodeID(nil), peers...)
	SortByDistance(self, sorted)
	if m > len(sorted) {
		m = len(sorted)
	}
	d := Distance(self, sorted[m-1])
	dInt := new(big.Int).SetBytes(d[:])
	if dInt.Sign() == 0 {
		return float64(len(peers)) + 1
	}
	space := new(big.Int).Lsh(big.NewInt(1), 256)
	n := new(big.Float).Quo(
		new(big.Float).SetInt(new(big.Int).Mul(big.NewInt(int64(m)), space)),
		new(big.Float).SetInt(dInt),
	)
	f, _ := n.Float64()
	return f
}
