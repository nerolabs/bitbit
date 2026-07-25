package sim

import (
	"reflect"
	"strings"
	"testing"
)

// The M12 acceptance test: reputation-gated quorum consensus commits an
// established node's entry to every replica, refuses a nobody, syncs a
// latecomer, and serves retrieval from a purely local replica.
func TestConsensusCommitsAndGatekeeps(t *testing.T) {
	o := DefaultConsensusOpts()
	res, err := Consensus(51, o)
	if err != nil {
		t.Fatalf("seed %d: %v\n%s", res.Seed, err, strings.Join(res.Timeline, "\n"))
	}
	if res.Committed != 1 {
		t.Fatalf("seed %d: %d blocks committed, want 1", res.Seed, res.Committed)
	}
	if res.ReplicasInSync < o.Established {
		t.Fatalf("seed %d: only %d replicas in sync (want at least the %d validators)\n%s",
			res.Seed, res.ReplicasInSync, o.Established, strings.Join(res.Timeline, "\n"))
	}
	if !res.NobodyRejected {
		t.Fatalf("seed %d: a zero-reputation node wrote to the chain", res.Seed)
	}
	if res.LatecomerSynced != 1 {
		t.Fatalf("seed %d: latecomer synced %d blocks, want 1", res.Seed, res.LatecomerSynced)
	}
	if !res.Retrieved {
		t.Fatalf("seed %d: retrieval via local replica failed", res.Seed)
	}
	t.Logf("\n%s\n%s", strings.Join(res.Timeline, "\n"), res)
}

func TestConsensusIsDeterministic(t *testing.T) {
	o := DefaultConsensusOpts()
	o.Nodes = 12
	o.Established = 5
	o.FileSize = 32 << 10
	r1, err1 := Consensus(13, o)
	r2, err2 := Consensus(13, o)
	if err1 != nil || err2 != nil {
		t.Fatalf("errs: %v / %v", err1, err2)
	}
	if !reflect.DeepEqual(r1, r2) {
		t.Fatalf("same seed diverged:\n%+v\n%+v", r1, r2)
	}
}
