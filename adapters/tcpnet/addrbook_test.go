package tcpnet_test

// The two-slot address book (#27 polish): a peer may be known by a
// direct host:port AND a relay:R@host:port form at once. The dialer
// prefers direct (no third hop), falls back to the relay in the same
// delivery, and drops a direct address only when the relay fallback
// PROVES it stale by reaching the peer. Envelopes also gossip a node's
// own -relay service so a NATed peer can discover a relay unconfigured.

import (
	"net"
	"testing"
	"time"

	"github.com/nerolabs/silt/adapters/identity"
	"github.com/nerolabs/silt/adapters/relay"
	"github.com/nerolabs/silt/adapters/tcpnet"
	"github.com/nerolabs/silt/ports"
)

// deadAddr returns a loopback address that refuses connections: bind,
// note the port, close — nothing listens there afterwards.
func deadAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return addr
}

func awaitReply(t *testing.T, ch <-chan ports.Message, wantRID uint64, what string) {
	t.Helper()
	select {
	case msg := <-ch:
		if msg.RID != wantRID {
			t.Fatalf("%s: reply RID = %d, want %d", what, msg.RID, wantRID)
		}
	case <-time.After(10 * time.Second):
		t.Fatal(what + ": no reply")
	}
}

// TestDirectPreferredOverRelay: B is reachable directly AND known by a
// relay form pointing at a dead relay. If the dialer preferred the
// relay, the exchange would fail; preferring direct, it succeeds.
func TestDirectPreferredOverRelay(t *testing.T) {
	identA, identB := identity.FromSeed(61), identity.FromSeed(62)
	trA, err := tcpnet.New(newLoop(t), identA, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer trA.Close()
	trB, err := tcpnet.New(newLoop(t), identB, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer trB.Close()

	trB.SetHandler(func(from ports.NodeID, msg ports.Message) {
		trB.Send(from, ports.Message{Kind: ports.MsgFindNodeReply, RID: msg.RID})
	})
	reply := make(chan ports.Message, 1)
	trA.SetHandler(func(from ports.NodeID, msg ports.Message) {
		if from == identB.NodeID() {
			reply <- msg
		}
	})

	trA.AddPeer(identB.NodeID(), relay.Addr(identity.FromSeed(63).NodeID(), deadAddr(t)))
	trA.AddPeer(identB.NodeID(), trB.Addr()) // both slots filled
	if err := trA.Send(identB.NodeID(), ports.Message{Kind: ports.MsgFindNode, RID: 11}); err != nil {
		t.Fatal(err)
	}
	awaitReply(t, reply, 11, "direct-preferred exchange")
}

// TestRelayFallbackForgetsStaleDirect: A holds a dead direct address
// and a live relay form for NATed B. The delivery must fall back to the
// relay — and, having reached B that way, drop the stale direct address
// so later dials skip the dead detour.
func TestRelayFallbackForgetsStaleDirect(t *testing.T) {
	identR, identA, identB := identity.FromSeed(64), identity.FromSeed(65), identity.FromSeed(66)
	srv, err := relay.Serve("127.0.0.1:0", identR, relay.Config{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	trA, err := tcpnet.New(newLoop(t), identA, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer trA.Close()
	trB, err := tcpnet.New(newLoop(t), identB, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer trB.Close()

	rc := register(t, identB, identR.NodeID(), srv.Addr(), trB)
	trB.SetHandler(func(from ports.NodeID, msg ports.Message) {
		trB.Send(from, ports.Message{Kind: ports.MsgFindNodeReply, RID: msg.RID})
	})
	reply := make(chan ports.Message, 1)
	trA.SetHandler(func(from ports.NodeID, msg ports.Message) {
		if from == identB.NodeID() {
			reply <- msg
		}
	})

	stale := deadAddr(t)
	trA.AddPeer(identB.NodeID(), stale)
	trA.AddPeer(identB.NodeID(), rc.Addr())
	if err := trA.Send(identB.NodeID(), ports.Message{Kind: ports.MsgFindNode, RID: 12}); err != nil {
		t.Fatal(err)
	}
	awaitReply(t, reply, 12, "relay fallback")

	for _, p := range trA.Peers() {
		if p.ID == identB.NodeID() && p.Addr == stale {
			t.Fatalf("stale direct address %s survived a successful relay fallback", stale)
		}
	}
}

// TestLearnKeepsSlotsSeparate: an mDNS-learned direct address must
// survive the peer stamping its relay form on envelopes (the old
// one-slot book lost whichever arrived first).
func TestLearnKeepsSlotsSeparate(t *testing.T) {
	identA, identB := identity.FromSeed(67), identity.FromSeed(68)
	trA, err := tcpnet.New(newLoop(t), identA, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer trA.Close()
	trB, err := tcpnet.New(newLoop(t), identB, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer trB.Close()

	// A knows B's direct address (as mDNS would teach it).
	trA.AddPeer(identB.NodeID(), trB.Addr())
	// B believes it is NATed and stamps a relay form on everything.
	relayForm := relay.Addr(identity.FromSeed(69).NodeID(), "203.0.113.9:4002")
	trB.SetAdvertise(relayForm)

	got := make(chan struct{}, 1)
	trA.SetHandler(func(from ports.NodeID, msg ports.Message) {
		if from == identB.NodeID() {
			got <- struct{}{}
		}
	})
	trB.AddPeer(identA.NodeID(), trA.Addr())
	if err := trB.Send(identA.NodeID(), ports.Message{Kind: ports.MsgFindNode, RID: 13}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-got:
	case <-time.After(10 * time.Second):
		t.Fatal("message never arrived")
	}

	var haveDirect, haveRelay bool
	for _, p := range trA.Peers() {
		if p.ID != identB.NodeID() {
			continue
		}
		haveDirect = haveDirect || p.Addr == trB.Addr()
		haveRelay = haveRelay || p.Addr == relayForm
	}
	if !haveDirect || !haveRelay {
		t.Fatalf("book lost a slot: direct=%v relay=%v peers=%v", haveDirect, haveRelay, trA.Peers())
	}
}

// TestRelayServiceGossip: a node offering -relay stamps the service on
// its envelopes; anyone it talks to learns it as a candidate relay.
func TestRelayServiceGossip(t *testing.T) {
	identA, identB := identity.FromSeed(71), identity.FromSeed(72)
	trA, err := tcpnet.New(newLoop(t), identA, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer trA.Close()
	trB, err := tcpnet.New(newLoop(t), identB, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer trB.Close()

	trB.SetRelayService("203.0.113.7:4002")
	got := make(chan struct{}, 1)
	trA.SetHandler(func(from ports.NodeID, msg ports.Message) {
		if from == identB.NodeID() {
			got <- struct{}{}
		}
	})
	trB.AddPeer(identA.NodeID(), trA.Addr())
	if err := trB.Send(identA.NodeID(), ports.Message{Kind: ports.MsgFindNode, RID: 14}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-got:
	case <-time.After(10 * time.Second):
		t.Fatal("message never arrived")
	}

	relays := trA.KnownRelays()
	if len(relays) != 1 || relays[0].ID != identB.NodeID() || relays[0].Addr != "203.0.113.7:4002" {
		t.Fatalf("KnownRelays = %v, want [{B 203.0.113.7:4002}]", relays)
	}
}
