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

// delivers wires a receiver on `to`, sends one message from `from`, runs
// the scheduler to quiescence, and reports whether it arrived. It resets
// the handler counter each call so a pair can be probed repeatedly.
func delivers(s *simclock.Scheduler, n *simnet.Network, from, to ports.NodeID) bool {
	got := false
	n.Endpoint(to).SetHandler(func(ports.NodeID, ports.Message) { got = true })
	n.Endpoint(from).Send(to, ports.Message{})
	s.Run()
	return got
}

// A NATed node is un-dialable cold — an unsolicited inbound from a public
// peer has nowhere to land — but it can always dial out, and dialing out
// opens the reverse mapping so the callee can then reply.
func TestNATUndialableColdButOutboundOpensReturnPath(t *testing.T) {
	s, n := setup(1, simnet.Config{LatencyMin: 1, LatencyMax: 1})
	pub, natd := id(1), id(2)
	n.NAT(natd, 1, false)

	if delivers(s, n, pub, natd) {
		t.Fatal("public peer dialed a NATed node cold — NAT should drop unsolicited inbound")
	}
	if !delivers(s, n, natd, pub) {
		t.Fatal("a NATed node must be able to dial out")
	}
	// That outbound opened the reverse mapping: now the public peer can reply.
	if !delivers(s, n, pub, natd) {
		t.Fatal("public peer should reach the NATed node through the mapping its outbound opened")
	}
}

// Peers behind the same NAT are on one private segment and dial directly.
func TestNATSameLANDialsDirect(t *testing.T) {
	s, n := setup(1, simnet.Config{LatencyMin: 1, LatencyMax: 1})
	a, b := id(1), id(2)
	n.NAT(a, 7, false)
	n.NAT(b, 7, false)
	if !delivers(s, n, a, b) {
		t.Fatal("LAN-mates behind the same NAT should reach each other directly")
	}
}

// Two nodes behind different NATs can't dial each other cold; with a relay
// the packet is spliced through (counted), without one it's dropped.
func TestNATCrossNATNeedsRelay(t *testing.T) {
	s, n := setup(1, simnet.Config{LatencyMin: 1, LatencyMax: 1})
	a, b, relay := id(1), id(2), id(9)
	n.NAT(a, 1, false)
	n.NAT(b, 2, false)

	if delivers(s, n, a, b) {
		t.Fatal("two NATed nodes on different LANs dialed each other with no relay")
	}
	if n.Stats.Relayed != 0 {
		t.Fatalf("nothing should have relayed yet, got %d", n.Stats.Relayed)
	}

	n.Relay(relay)
	if !delivers(s, n, a, b) {
		t.Fatal("with a relay, cross-NAT traffic should be spliced through")
	}
	if n.Stats.Relayed != 1 {
		t.Fatalf("expected exactly one relayed delivery, got %d", n.Stats.Relayed)
	}
}

// A relayed delivery must NOT open a direct mapping: the two NATs never
// touched, so a later direct dial still fails until a punch lands.
func TestNATRelayDoesNotOpenDirectPath(t *testing.T) {
	s, n := setup(1, simnet.Config{LatencyMin: 1, LatencyMax: 1})
	a, b, relay := id(1), id(2), id(9)
	n.NAT(a, 1, false)
	n.NAT(b, 2, false)
	n.Relay(relay)

	delivers(s, n, a, b) // goes via the relay
	// b still can't reach a directly — that was a relay hop, not a mapping.
	base := n.Stats.Relayed
	if !delivers(s, n, b, a) {
		t.Fatal("reply should still get through — via the relay")
	}
	if n.Stats.Relayed != base+1 {
		t.Fatal("the reply should also have relayed, not gone direct")
	}
}

// Hole-punching opens a direct path between cone NATs (bytes then bypass
// the relay); against a symmetric NAT the punch fails and traffic stays
// on the relay.
func TestNATHolePunch(t *testing.T) {
	s, n := setup(1, simnet.Config{LatencyMin: 1, LatencyMax: 1})
	a, b, sym, relay := id(1), id(2), id(3), id(9)
	n.NAT(a, 1, false)
	n.NAT(b, 2, false)
	n.NAT(sym, 3, true)
	n.Relay(relay)

	if !n.HolePunch(a, b) {
		t.Fatal("two cone NATs should punch")
	}
	before := n.Stats.Relayed
	if !delivers(s, n, a, b) || !delivers(s, n, b, a) {
		t.Fatal("a punched pair should reach each other directly")
	}
	if n.Stats.Relayed != before {
		t.Fatal("punched traffic went through the relay instead of direct")
	}

	if n.HolePunch(a, sym) {
		t.Fatal("a symmetric NAT can't be punched")
	}
	relBefore := n.Stats.Relayed
	if !delivers(s, n, a, sym) {
		t.Fatal("cross-NAT traffic to the symmetric node should still reach it via the relay")
	}
	if n.Stats.Relayed != relBefore+1 {
		t.Fatal("traffic to a symmetric NAT should fall back to the relay")
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
