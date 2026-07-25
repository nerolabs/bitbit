// Network capacity awareness (M9): every node continuously estimates
// how big the network is and how much storage it holds, from purely
// local knowledge — the density of its own DHT neighborhood (see
// docs/math/06-counting-the-crowd.md) times the average pledge its
// peers gossip on every message.
package node

import (
	"github.com/nerolabs/bitbit/core/dht"
	"github.com/nerolabs/bitbit/ports"
)

// NetEstimate is one node's view of the whole network.
type NetEstimate struct {
	KnownPeers     int     // peers whose capacity we've heard
	EstimatedNodes float64 // density-based network size estimate
	EstimatedTotal int64   // network-wide pledged bytes (estimate)
	EstimatedUsed  int64   // network-wide used bytes (estimate)
	SelfUsed       int64
	SelfTotal      int64
}

// EstimateNetwork combines the size estimate with the gossip sample:
// N_est nodes times the mean pledge of the nodes we've actually heard
// from. Both halves are approximations; both are unbiased enough for
// capacity planning, and every node can compute them alone.
func (n *Node) EstimateNetwork() NetEstimate {
	est := NetEstimate{KnownPeers: len(n.peerCaps)}
	if n.capRep != nil {
		est.SelfUsed, est.SelfTotal = n.capRep.Capacity()
	}
	est.EstimatedNodes = dht.EstimateNetworkSize(n.id, n.table.Closest(n.id, 16), 8)

	// Mean pledge over our sample (self included when pledging).
	var sumUsed, sumTotal int64
	samples := 0
	for _, c := range n.peerCaps {
		sumUsed += c.used
		sumTotal += c.total
		samples++
	}
	if est.SelfTotal > 0 {
		sumUsed += est.SelfUsed
		sumTotal += est.SelfTotal
		samples++
	}
	if samples > 0 {
		est.EstimatedTotal = int64(est.EstimatedNodes * float64(sumTotal) / float64(samples))
		est.EstimatedUsed = int64(est.EstimatedNodes * float64(sumUsed) / float64(samples))
	}
	return est
}

// HeldRoots reports which top-level identifiers this node holds shards
// of, and how many — what a daemon can honestly say about its own
// contents: opaque roots and counts, nothing more (there is nothing
// more it CAN know).
func (n *Node) HeldRoots() map[ports.Hash]int {
	out := make(map[ports.Hash]int)
	for id, p := range n.proofs {
		if ok, _ := n.store.Has(bg(), id); ok {
			out[p.Root]++
		}
	}
	return out
}
