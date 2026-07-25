package simnet_test

import (
	"testing"

	"github.com/nerolabs/silt/adapters/simclock"
	"github.com/nerolabs/silt/adapters/simnet"
	"github.com/nerolabs/silt/ports"
)

func id(b byte) ports.NodeID { return ports.HashBytes([]byte{b}) }

func setup(seed int64, cfg simnet.Config) (*simclock.Scheduler, *simnet.Network) {
	s := simclock.New()
	return s, simnet.New(s, seed, cfg)
}

func TestDeliveryWithLatency(t *testing.T) {
	s, n := setup(1, simnet.Config{LatencyMin: 10 * ports.Millisecond, LatencyMax: 10 * ports.Millisecond})
	a, b := n.Endpoint(id(1)), n.Endpoint(id(2))
	var gotFrom ports.NodeID
	var at ports.Time
	b.SetHandler(func(from ports.NodeID, msg ports.Message) { gotFrom, at = from, s.Now() })
	a.SetHandler(func(ports.NodeID, ports.Message) {})
	if err := a.Send(id(2), ports.Message{Kind: ports.MsgFindNode}); err != nil {
		t.Fatal(err)
	}
	s.Run()
	if gotFrom != id(1) {
		t.Fatal("message not delivered or wrong sender")
	}
	if at != ports.Time(10*ports.Millisecond) {
		t.Fatalf("delivered at %d, want 10ms", at)
	}
}

func TestLossIsSeededAndDeterministic(t *testing.T) {
	counts := func(seed int64) (delivered, dropped int) {
		s, n := setup(seed, simnet.Config{LatencyMin: 1, LatencyMax: 1, Loss: 0.3})
		a, b := n.Endpoint(id(1)), n.Endpoint(id(2))
		b.SetHandler(func(ports.NodeID, ports.Message) {})
		_ = a
		for i := 0; i < 1000; i++ {
			n.Endpoint(id(1)).Send(id(2), ports.Message{})
		}
		s.Run()
		return n.Stats.Delivered, n.Stats.Dropped
	}
	d1, x1 := counts(7)
	d2, x2 := counts(7)
	if d1 != d2 || x1 != x2 {
		t.Fatalf("same seed diverged: %d/%d vs %d/%d", d1, x1, d2, x2)
	}
	if x1 < 200 || x1 > 400 {
		t.Fatalf("dropped %d of 1000 at loss=0.3 — suspicious", x1)
	}
}

func TestPartitionAndHeal(t *testing.T) {
	s, n := setup(1, simnet.Config{LatencyMin: 1, LatencyMax: 1})
	a, b := n.Endpoint(id(1)), n.Endpoint(id(2))
	_ = a
	got := 0
	b.SetHandler(func(ports.NodeID, ports.Message) { got++ })
	n.Partition(id(1)) // 1 alone vs everyone
	n.Endpoint(id(1)).Send(id(2), ports.Message{})
	s.Run()
	if got != 0 {
		t.Fatal("message crossed a partition")
	}
	n.ClearPartition()
	n.Endpoint(id(1)).Send(id(2), ports.Message{})
	s.Run()
	if got != 1 {
		t.Fatal("message not delivered after heal")
	}
}

func TestKillDropsInFlight(t *testing.T) {
	s, n := setup(1, simnet.Config{LatencyMin: 10 * ports.Millisecond, LatencyMax: 10 * ports.Millisecond})
	a, b := n.Endpoint(id(1)), n.Endpoint(id(2))
	_ = a
	got := 0
	b.SetHandler(func(ports.NodeID, ports.Message) { got++ })
	n.Endpoint(id(1)).Send(id(2), ports.Message{}) // in flight...
	n.Kill(id(2))                                  // ...dies before delivery
	s.Run()
	if got != 0 {
		t.Fatal("dead node received a message")
	}
	n.Restart(id(2))
	n.Endpoint(id(1)).Send(id(2), ports.Message{})
	s.Run()
	if got != 1 {
		t.Fatal("restarted node should receive")
	}
}
