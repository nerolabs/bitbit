package identity_test

import (
	"crypto/tls"
	"net"
	"path/filepath"
	"testing"

	"github.com/nerolabs/silt/adapters/identity"
)

func TestSaveLoadRoundtrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "identity.pem")
	id1, err := identity.LoadOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	id2, err := identity.LoadOrCreate(path) // second call must load, not mint
	if err != nil {
		t.Fatal(err)
	}
	if id1.NodeID() != id2.NodeID() {
		t.Fatal("identity changed across reload — daemons would lose their reputation on restart")
	}
}

func TestSeedIsDeterministic(t *testing.T) {
	if identity.FromSeed(7).NodeID() != identity.FromSeed(7).NodeID() {
		t.Fatal("same seed, different identity")
	}
	if identity.FromSeed(7).NodeID() == identity.FromSeed(8).NodeID() {
		t.Fatal("different seeds collided")
	}
}

// The core security property: a TLS handshake succeeds only when the
// remote key hashes to the NodeID we intended to reach.
func TestPinnedHandshake(t *testing.T) {
	server := identity.FromSeed(1)
	imposter := identity.FromSeed(2)

	serve := func(id *identity.Identity) net.Listener {
		cert, err := id.Certificate()
		if err != nil {
			t.Fatal(err)
		}
		ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
			Certificates: []tls.Certificate{cert},
			ClientAuth:   tls.RequireAnyClientCert,
			MinVersion:   tls.VersionTLS13,
		})
		if err != nil {
			t.Fatal(err)
		}
		go func() {
			for {
				c, err := ln.Accept()
				if err != nil {
					return
				}
				go func() {
					c.(*tls.Conn).Handshake()
					c.Close()
				}()
			}
		}()
		return ln
	}
	dial := func(addr string, expect *identity.Identity) error {
		clientCert, err := identity.FromSeed(3).Certificate()
		if err != nil {
			t.Fatal(err)
		}
		conn, err := tls.Dial("tcp", addr, &tls.Config{
			Certificates:          []tls.Certificate{clientCert},
			InsecureSkipVerify:    true, // replaced by pinning, not skipped
			VerifyPeerCertificate: identity.VerifyExpected(expect.NodeID()),
			MinVersion:            tls.VersionTLS13,
		})
		if err != nil {
			return err
		}
		return conn.Close()
	}

	honest := serve(server)
	defer honest.Close()
	if err := dial(honest.Addr().String(), server); err != nil {
		t.Fatalf("handshake with the right identity failed: %v", err)
	}

	fake := serve(imposter)
	defer fake.Close()
	if err := dial(fake.Addr().String(), server); err == nil {
		t.Fatal("handshake with an imposter presenting the wrong key must fail")
	}
}
