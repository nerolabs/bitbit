package lan

import (
	"net"
	"testing"
	"time"

	"github.com/nerolabs/silt/adapters/tcpnet"
	"github.com/nerolabs/silt/ports"
)

func peer(t *testing.T, b byte, addr string) tcpnet.Peer {
	t.Helper()
	var id ports.Hash
	for i := range id {
		id[i] = b
	}
	return tcpnet.Peer{ID: id, Addr: addr}
}

func TestMarshalParseRoundTrip(t *testing.T) {
	want := peer(t, 0xab, "192.168.1.20:7101")
	got, ok := parse(marshal(want))
	if !ok {
		t.Fatal("parse rejected a packet we marshaled")
	}
	if got.ID != want.ID || got.Addr != want.Addr {
		t.Fatalf("round trip: got %v, want %v", got, want)
	}
}

func TestParseRejectsJunk(t *testing.T) {
	cases := [][]byte{
		[]byte(""),                               // empty
		[]byte("hello world"),                    // no magic
		[]byte(magic + "not-a-peer-string"),      // magic but garbage body
		[]byte(magic + "@192.168.1.1:7101"),      // empty ID
		[]byte("silt-lan/2 " + "x@1.2.3.4:9000"), // wrong version prefix
	}
	for _, c := range cases {
		if _, ok := parse(c); ok {
			t.Errorf("parse accepted junk: %q", c)
		}
	}
}

func TestAdvertiseAddrLoopbackRejected(t *testing.T) {
	if _, err := AdvertiseAddr("127.0.0.1:7101"); err == nil {
		t.Fatal("expected loopback bind to be rejected")
	}
}

func TestAdvertiseAddrConcretePassthrough(t *testing.T) {
	got, err := AdvertiseAddr("203.0.113.7:7101") // TEST-NET-3, a concrete IP
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "203.0.113.7:7101" {
		t.Fatalf("got %q, want the address unchanged", got)
	}
}

func TestAdvertiseAddrUnspecifiedResolvesLAN(t *testing.T) {
	got, err := AdvertiseAddr("0.0.0.0:7101")
	if err != nil {
		t.Skipf("no LAN IPv4 on this host: %v", err) // e.g. an offline sandbox
	}
	host, port, err := net.SplitHostPort(got)
	if err != nil {
		t.Fatalf("result %q is not host:port: %v", got, err)
	}
	if port != "7101" {
		t.Fatalf("port not preserved: got %q", port)
	}
	ip := net.ParseIP(host)
	if ip == nil || ip.IsLoopback() || ip.IsUnspecified() {
		t.Fatalf("resolved to a non-LAN address: %q", host)
	}
}

// TestBeaconRoundTrip is a live multicast test: one beacon should hear
// another and must not report itself. Multicast loopback isn't available in
// every sandbox, so it skips rather than fails when the group can't carry a
// packet between two local sockets.
func TestBeaconRoundTrip(t *testing.T) {
	a := peer(t, 0x11, "192.168.1.11:7101")
	b := peer(t, 0x22, "192.168.1.22:7102")

	heard := make(chan tcpnet.Peer, 8)
	recv := New(b, func(p tcpnet.Peer) { heard <- p })
	if err := recv.Start(); err != nil {
		t.Skipf("multicast unavailable: %v", err)
	}
	defer recv.Close()

	send := New(a, func(tcpnet.Peer) {})
	if err := send.Start(); err != nil {
		t.Skipf("multicast unavailable: %v", err)
	}
	defer send.Close()

	select {
	case got := <-heard:
		if got.ID != a.ID {
			t.Fatalf("heard the wrong peer: got %v, want %v", got, a)
		}
		if got.Addr != a.Addr {
			t.Fatalf("addr mismatch: got %q, want %q", got.Addr, a.Addr)
		}
	case <-time.After(3 * time.Second):
		t.Skip("no multicast delivery in this environment")
	}

	// The receiver must never report its own announcements.
	select {
	case got := <-heard:
		if got.ID == b.ID {
			t.Fatal("beacon reported itself as a peer")
		}
	case <-time.After(200 * time.Millisecond):
	}
}
