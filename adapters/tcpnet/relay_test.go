package tcpnet_test

// End-to-end proof of the relay path at the transport level, on
// localhost, in CI: a "NATed" peer is simply one whose direct address
// nobody is given — it registers with a relay and is reached only
// through it. The second test is the canonical cross-network case:
// BOTH peers NATed, every byte of both directions relayed, and the
// pinned end-to-end TLS still authenticating each frame's sender.

import (
	"testing"
	"time"

	"github.com/nerolabs/silt/adapters/eventloop"
	"github.com/nerolabs/silt/adapters/identity"
	"github.com/nerolabs/silt/adapters/relay"
	"github.com/nerolabs/silt/adapters/tcpnet"
	"github.com/nerolabs/silt/ports"
)

func newLoop(t *testing.T) *eventloop.Loop {
	t.Helper()
	loop := eventloop.New()
	go loop.Run()
	t.Cleanup(loop.Stop)
	return loop
}

func register(t *testing.T, ident *identity.Identity, relayID ports.NodeID, relayAddr string, tr *tcpnet.Transport) *relay.Client {
	t.Helper()
	rc, err := relay.NewClient(ident, relayID, relayAddr, tr.RelayInbound, nil)
	if err != nil {
		t.Fatal(err)
	}
	ready := make(chan error, 1)
	go rc.Run(func(err error) { ready <- err })
	t.Cleanup(rc.Close)
	select {
	case err := <-ready:
		if err != nil {
			t.Fatalf("relay registration: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("relay registration timed out")
	}
	tr.SetAdvertise(rc.Addr())
	return rc
}

func TestRelayedTransportRoundTrip(t *testing.T) {
	identR, identA, identB := identity.FromSeed(21), identity.FromSeed(22), identity.FromSeed(23)
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
		// B learned A's direct address from the relayed envelope; the
		// reply goes straight back, no relay needed for this leg.
		trB.Send(from, ports.Message{Kind: ports.MsgFindNodeReply, RID: msg.RID})
	})
	reply := make(chan ports.Message, 1)
	trA.SetHandler(func(from ports.NodeID, msg ports.Message) {
		if from == identB.NodeID() {
			reply <- msg
		}
	})

	// A knows B only by its relay address — exactly what B advertises.
	trA.AddPeer(identB.NodeID(), rc.Addr())
	if err := trA.Send(identB.NodeID(), ports.Message{Kind: ports.MsgFindNode, RID: 7}); err != nil {
		t.Fatal(err)
	}
	select {
	case msg := <-reply:
		if msg.RID != 7 {
			t.Fatalf("reply RID = %d, want 7", msg.RID)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no reply through the relay")
	}
}

func TestBothNATedRoundTrip(t *testing.T) {
	identR, identA, identB := identity.FromSeed(31), identity.FromSeed(32), identity.FromSeed(33)
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

	register(t, identA, identR.NodeID(), srv.Addr(), trA)
	rcB := register(t, identB, identR.NodeID(), srv.Addr(), trB)

	trB.SetHandler(func(from ports.NodeID, msg ports.Message) {
		// A's envelope advertised A's relay address, so this reply is
		// relayed too — the full NATed↔NATed exchange.
		trB.Send(from, ports.Message{Kind: ports.MsgFindNodeReply, RID: msg.RID})
	})
	reply := make(chan ports.Message, 1)
	trA.SetHandler(func(from ports.NodeID, msg ports.Message) {
		if from == identB.NodeID() {
			reply <- msg
		}
	})

	trA.AddPeer(identB.NodeID(), rcB.Addr())
	if err := trA.Send(identB.NodeID(), ports.Message{Kind: ports.MsgFindNode, RID: 42}); err != nil {
		t.Fatal(err)
	}
	select {
	case msg := <-reply:
		if msg.RID != 42 {
			t.Fatalf("reply RID = %d, want 42", msg.RID)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no reply when both peers are NATed")
	}
}
