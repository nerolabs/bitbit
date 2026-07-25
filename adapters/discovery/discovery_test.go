package discovery_test

import (
	"path/filepath"
	"testing"

	"github.com/nerolabs/silt/adapters/discovery"
	"github.com/nerolabs/silt/adapters/tcpnet"
	"github.com/nerolabs/silt/ports"
)

func peer(b byte, addr string) tcpnet.Peer {
	return tcpnet.Peer{ID: ports.HashBytes([]byte{b}), Addr: addr}
}

func TestParseFormatRoundtrip(t *testing.T) {
	p := peer(1, "203.0.113.7:4001")
	got, err := discovery.Parse(discovery.Format(p))
	if err != nil {
		t.Fatal(err)
	}
	if got != p {
		t.Fatalf("roundtrip mangled peer: %+v", got)
	}
	if _, err := discovery.Parse("no-at-sign"); err == nil {
		t.Fatal("junk must not parse")
	}
	if _, err := discovery.Parse("nothex@1.2.3.4:1"); err == nil {
		t.Fatal("bad ID must not parse")
	}
}

func TestFilePersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "peers.json")
	if loaded, err := discovery.LoadFile(path); err != nil || len(loaded) != 0 {
		t.Fatalf("missing file: want empty book, got %d peers, err %v", len(loaded), err)
	}
	peers := []tcpnet.Peer{peer(1, "10.0.0.1:4001"), peer(2, "10.0.0.2:4001")}
	if err := discovery.SaveFile(path, peers); err != nil {
		t.Fatal(err)
	}
	loaded, err := discovery.LoadFile(path)
	if err != nil || len(loaded) != 2 || loaded[0] != peers[0] || loaded[1] != peers[1] {
		t.Fatalf("reload: %+v, err %v", loaded, err)
	}
}

func TestParseTXTIgnoresStrangers(t *testing.T) {
	good := discovery.Format(peer(3, "198.51.100.9:4001"))
	got := discovery.ParseTXT([]string{
		"v=spf1 include:example.com ~all", // DNS is full of strangers
		good,
		"another random TXT record",
	})
	if len(got) != 1 || got[0].Addr != "198.51.100.9:4001" {
		t.Fatalf("want exactly the one valid peer, got %+v", got)
	}
}
