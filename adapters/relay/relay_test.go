package relay

// The point proven here: "NATed" is not a network condition, it's a
// behavior — a peer that accepts no inbound connections and only dials
// out. The Client below never listens, so the whole registered-splice
// path (register → connect → incoming → accept → ok → bytes) is
// exercised on localhost, in CI, with no NAT in sight. The real-router
// test (two Macs, two networks) then only confirms what these already
// prove.

import (
	"io"
	"net"
	"testing"
	"time"

	"github.com/nerolabs/silt/adapters/identity"
	"github.com/nerolabs/silt/ports"
)

func startClient(t *testing.T, cl *Client) {
	t.Helper()
	ready := make(chan error, 1)
	go cl.Run(func(err error) { ready <- err })
	t.Cleanup(cl.Close)
	select {
	case err := <-ready:
		if err != nil {
			t.Fatalf("registration failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("registration timed out")
	}
}

// TestRegisterReportsObservedAddr is the #27 Phase-1 check: the relay reports
// a registrant its public host:port as observed (STUN-style), and the client
// exposes it via Observed(). A NATed node can't otherwise learn its own
// public endpoint — hole-punching needs it.
func TestRegisterReportsObservedAddr(t *testing.T) {
	identR, identB := identity.FromSeed(20), identity.FromSeed(21)
	srv, err := Serve("127.0.0.1:0", identR, Config{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	cl, err := NewClient(identB, identR.NodeID(), srv.Addr(), func(net.Conn) {}, nil)
	if err != nil {
		t.Fatal(err)
	}
	startClient(t, cl)

	obs := cl.Observed()
	if obs == "" {
		t.Fatal("Observed() empty — relay did not report the registrant's endpoint")
	}
	host, port, err := net.SplitHostPort(obs)
	if err != nil {
		t.Fatalf("observed %q is not host:port: %v", obs, err)
	}
	if host != "127.0.0.1" || port == "" {
		t.Fatalf("observed %q: want a 127.0.0.1:<port> loopback source", obs)
	}
}

func TestSpliceRoundTrip(t *testing.T) {
	identR, identB, identS := identity.FromSeed(1), identity.FromSeed(2), identity.FromSeed(3)
	srv, err := Serve("127.0.0.1:0", identR, Config{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	heard := make(chan []byte, 1)
	cl, err := NewClient(identB, identR.NodeID(), srv.Addr(), func(conn net.Conn) {
		defer conn.Close()
		b := make([]byte, 5)
		if _, err := io.ReadFull(conn, b); err != nil {
			t.Errorf("B read: %v", err)
			return
		}
		heard <- b
		conn.Write([]byte("world"))
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	startClient(t, cl)

	certS, _ := identS.Certificate()
	conn, err := DialThrough(certS, identR.NodeID(), srv.Addr(), identB.NodeID())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, 5)
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.ReadFull(conn, reply); err != nil {
		t.Fatalf("S read: %v", err)
	}
	if string(reply) != "world" {
		t.Fatalf("reply = %q", reply)
	}
	select {
	case b := <-heard:
		if string(b) != "hello" {
			t.Fatalf("B heard %q", b)
		}
	case <-time.After(time.Second):
		t.Fatal("B never heard the payload")
	}
	if srv.Registered() != 1 {
		t.Fatalf("registered = %d, want 1", srv.Registered())
	}
}

func TestUnregisteredTargetRefused(t *testing.T) {
	identR, identS := identity.FromSeed(4), identity.FromSeed(5)
	srv, err := Serve("127.0.0.1:0", identR, Config{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	certS, _ := identS.Certificate()
	var nobody ports.NodeID
	nobody[0] = 0xAA
	if _, err := DialThrough(certS, identR.NodeID(), srv.Addr(), nobody); err == nil {
		t.Fatal("dial to an unregistered target should be refused")
	}
}

// TestAcceptIdentityEnforced: only the identity a connector asked for
// may claim the parked stream — a third party presenting a valid but
// different key gets "no such stream", and the rightful target can
// still claim it afterward.
func TestAcceptIdentityEnforced(t *testing.T) {
	identR, identB, identS, identX := identity.FromSeed(6), identity.FromSeed(7), identity.FromSeed(8), identity.FromSeed(9)
	srv, err := Serve("127.0.0.1:0", identR, Config{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	// Register B by hand so the test controls when the accept happens.
	certB, _ := identB.Certificate()
	bctl, err := dialRelay(certB, identR.NodeID(), srv.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer bctl.Close()
	if err := writeCtrl(bctl, ctrl{Op: "register"}); err != nil {
		t.Fatal(err)
	}
	if fr, err := readCtrl(bctl); err != nil || fr.Op != "ok" {
		t.Fatalf("register: %v %+v", err, fr)
	}

	certS, _ := identS.Certificate()
	dialed := make(chan error, 1)
	go func() {
		conn, err := DialThrough(certS, identR.NodeID(), srv.Addr(), identB.NodeID())
		if conn != nil {
			conn.Close()
		}
		dialed <- err
	}()

	fr, err := readCtrl(bctl)
	if err != nil || fr.Op != "incoming" {
		t.Fatalf("incoming: %v %+v", err, fr)
	}

	// The impostor claims the stream first.
	certX, _ := identX.Certificate()
	xconn, err := dialRelay(certX, identR.NodeID(), srv.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer xconn.Close()
	if err := writeCtrl(xconn, ctrl{Op: "accept", Stream: fr.Stream}); err != nil {
		t.Fatal(err)
	}
	if xfr, err := readCtrl(xconn); err != nil || xfr.Op != "err" {
		t.Fatalf("impostor accept should be refused, got %v %+v", err, xfr)
	}

	// The rightful target still can.
	bacc, err := dialRelay(certB, identR.NodeID(), srv.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer bacc.Close()
	if err := writeCtrl(bacc, ctrl{Op: "accept", Stream: fr.Stream}); err != nil {
		t.Fatal(err)
	}
	if bfr, err := readCtrl(bacc); err != nil || bfr.Op != "ok" {
		t.Fatalf("rightful accept: %v %+v", err, bfr)
	}
	if err := <-dialed; err != nil {
		t.Fatalf("connector should have gotten its pipe: %v", err)
	}
}

func TestSessionCap(t *testing.T) {
	identR, identB, identS := identity.FromSeed(10), identity.FromSeed(11), identity.FromSeed(12)
	srv, err := Serve("127.0.0.1:0", identR, Config{MaxSessions: 1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	hold := make(chan struct{})
	cl, err := NewClient(identB, identR.NodeID(), srv.Addr(), func(conn net.Conn) {
		<-hold // keep the first session open
		conn.Close()
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer close(hold)
	startClient(t, cl)

	certS, _ := identS.Certificate()
	first, err := DialThrough(certS, identR.NodeID(), srv.Addr(), identB.NodeID())
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if _, err := DialThrough(certS, identR.NodeID(), srv.Addr(), identB.NodeID()); err == nil {
		t.Fatal("second session should exceed MaxSessions=1")
	}
}

func TestAddrRoundTrip(t *testing.T) {
	id := identity.FromSeed(13).NodeID()
	s := Addr(id, "203.0.113.7:4002")
	gotID, hostport, ok := SplitAddr(s)
	if !ok || gotID != id || hostport != "203.0.113.7:4002" {
		t.Fatalf("SplitAddr(%q) = %v %q %v", s, gotID, hostport, ok)
	}
	if _, _, ok := SplitAddr("203.0.113.7:4001"); ok {
		t.Fatal("a direct address must not parse as a relay address")
	}
}
