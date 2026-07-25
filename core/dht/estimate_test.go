package dht

import (
	"math/rand"
	"testing"

	"github.com/nerolabs/silt/ports"
)

// The estimator should land within a small constant factor of the true
// network size across a range of sizes — that's all capacity planning
// needs, and it's what the density argument promises.
func TestEstimateNetworkSizeAccuracy(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for _, n := range []int{50, 200, 1000, 5000} {
		ids := make([]ports.NodeID, n)
		for i := range ids {
			rng.Read(ids[i][:])
		}
		// Average over several observers to smooth per-node luck.
		var sum float64
		const observers = 10
		for o := 0; o < observers; o++ {
			self := ids[o]
			peers := make([]ports.NodeID, 0, n-1)
			for _, id := range ids {
				if id != self {
					peers = append(peers, id)
				}
			}
			sum += EstimateNetworkSize(self, peers, 8)
		}
		est := sum / observers
		if est < float64(n)/3 || est > float64(n)*3 {
			t.Fatalf("n=%d: averaged estimate %.0f outside [n/3, 3n]", n, est)
		}
		t.Logf("true %5d → estimated %7.0f", n, est)
	}
}

func TestEstimateDegenerateCases(t *testing.T) {
	var self ports.NodeID
	if got := EstimateNetworkSize(self, nil, 8); got != 1 {
		t.Fatalf("no peers: estimate %v, want 1", got)
	}
	one := ports.HashBytes([]byte("peer"))
	if got := EstimateNetworkSize(self, []ports.NodeID{one}, 8); got <= 0 {
		t.Fatalf("single peer: estimate %v, want positive", got)
	}
}
