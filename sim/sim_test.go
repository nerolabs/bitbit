package sim

import (
	"testing"

	"github.com/nerolabs/silt/adapters/simnet"
	"github.com/nerolabs/silt/core/node"
	"github.com/nerolabs/silt/ports"
)

func nodeDefaultConfig() node.Config { return node.DefaultConfig() }

// The M3 demo as a test: add on node A, get on node Z, 50 nodes,
// chunks scattered across the swarm, A keeping nothing.
func TestScatter50Nodes(t *testing.T) {
	o := DefaultScatterOpts()
	o.FileSize = 256 << 10 // keep the test snappy
	o.ChunkSize = 8 << 10
	res, err := Scatter(1, o)
	if err != nil {
		t.Fatalf("seed %d: %v", res.Seed, err)
	}
	if !res.Match {
		t.Fatalf("seed %d: retrieved bytes differ", res.Seed)
	}
	if res.Placed == 0 {
		t.Fatalf("seed %d: nothing was distributed", res.Seed)
	}
	t.Logf("\n%s", res)
}

// Same seed ⇒ identical run, down to every counter. This is the
// determinism guarantee the whole sim architecture exists to provide.
func TestScatterIsDeterministic(t *testing.T) {
	o := DefaultScatterOpts()
	o.Nodes = 30
	o.FileSize = 64 << 10
	o.ChunkSize = 4 << 10
	r1, err1 := Scatter(99, o)
	r2, err2 := Scatter(99, o)
	if err1 != nil || err2 != nil {
		t.Fatalf("errs: %v / %v", err1, err2)
	}
	if r1 != r2 {
		t.Fatalf("same seed diverged:\n%+v\n%+v", r1, r2)
	}
}

func TestScatterAcrossSeeds(t *testing.T) {
	o := DefaultScatterOpts()
	o.Nodes = 40
	o.FileSize = 128 << 10
	o.ChunkSize = 8 << 10
	for seed := int64(1); seed <= 5; seed++ {
		res, err := Scatter(seed, o)
		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		if !res.Match {
			t.Fatalf("seed %d: bytes differ", seed)
		}
	}
}

func TestScatterWithPacketLoss(t *testing.T) {
	o := DefaultScatterOpts()
	o.Nodes = 40
	o.FileSize = 128 << 10
	o.ChunkSize = 8 << 10
	o.Net = simnet.Config{
		LatencyMin: 5 * ports.Millisecond,
		LatencyMax: 50 * ports.Millisecond,
		Loss:       0.02,
	}
	res, err := Scatter(3, o)
	if err != nil {
		t.Fatalf("seed %d with 2%% loss: %v", res.Seed, err)
	}
	if !res.Match {
		t.Fatalf("seed %d: bytes differ under loss", res.Seed)
	}
	t.Logf("\n%s", res)
}

func TestScatterSurvivesDeadNodes(t *testing.T) {
	o := DefaultScatterOpts()
	o.Nodes = 50
	o.FileSize = 128 << 10
	o.ChunkSize = 8 << 10
	o.Kill = 5 // 10% of the swarm dies between add and get
	res, err := Scatter(4, o)
	if err != nil {
		t.Fatalf("seed %d with 5 dead nodes: %v", res.Seed, err)
	}
	if !res.Match {
		t.Fatalf("seed %d: bytes differ with dead nodes", res.Seed)
	}
	t.Logf("\n%s", res)
}

func TestClusterBootstrapPopulatesTables(t *testing.T) {
	cl := NewCluster(2, 30, simnet.DefaultConfig(), nodeDefaultConfig())
	for i, nd := range cl.Nodes {
		if nd.Table().Size() < 3 {
			t.Fatalf("node %d knows only %d peers after bootstrap", i, nd.Table().Size())
		}
	}
}
