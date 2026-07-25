package sim

import (
	"reflect"
	"strings"
	"testing"
)

// The M9 acceptance test: a bounded network fills gracefully (spill-over
// placement), spreads stripes for availability (anti-affinity), and
// every node knows roughly how big the whole network is without asking
// anyone to count.
func TestCapacityFillsAndEstimates(t *testing.T) {
	o := DefaultCapacityOpts()
	res, err := Capacity(41, o)
	if err != nil {
		t.Fatalf("seed %d: %v\n%s", res.Seed, err, strings.Join(res.Timeline, "\n"))
	}
	if res.FilesStored < 5 {
		t.Fatalf("seed %d: only %d files fully placed — spill-over not working?\n%s",
			res.Seed, res.FilesStored, strings.Join(res.Timeline, "\n"))
	}
	if res.FilesDegraded == 0 && res.FilesStored == o.MaxFiles {
		t.Fatalf("seed %d: network never filled — scenario too roomy to prove bounds", res.Seed)
	}
	if float64(res.TrueUsed) < 0.5*float64(res.TrueTotal) {
		t.Fatalf("seed %d: network only %.0f%% full at saturation — placement wastes capacity",
			res.Seed, 100*float64(res.TrueUsed)/float64(res.TrueTotal))
	}
	// Column placement makes anti-affinity structural WITHIN a column (one
	// shard of each stripe per column), but under capacity pressure a full
	// column spills onto the next-closest node, which may already hold
	// another column — so a stripe can end up with a few shards on one
	// host. Spreading columns across distinct hosts/domains under spill is
	// failure-domain-aware placement (Phase 1 #6), not yet built. The red
	// line that must still hold: no node may hold k shards of one stripe,
	// or it could reconstruct that stripe's data on its own.
	if res.WorstOverlap >= o.Erasure.K {
		t.Fatalf("seed %d: a node holds %d shards of one stripe (>= k=%d) — concentrated enough to reconstruct; placement broken",
			res.Seed, res.WorstOverlap, o.Erasure.K)
	}
	if res.EstimateRatio < 0.25 || res.EstimateRatio > 4 {
		t.Fatalf("seed %d: median capacity estimate %.2fx truth — estimator broken", res.Seed, res.EstimateRatio)
	}
	if !res.Retrieved {
		t.Fatalf("seed %d: first file unretrievable from filled network", res.Seed)
	}
	t.Logf("\n%s\n%s", strings.Join(res.Timeline, "\n"), res)
}

func TestCapacityIsDeterministic(t *testing.T) {
	o := DefaultCapacityOpts()
	o.Nodes = 25
	o.MaxFiles = 10
	r1, err1 := Capacity(9, o)
	r2, err2 := Capacity(9, o)
	if err1 != nil || err2 != nil {
		t.Fatalf("errs: %v / %v", err1, err2)
	}
	if !reflect.DeepEqual(r1, r2) {
		t.Fatalf("same seed diverged:\n%+v\n%+v", r1, r2)
	}
}
