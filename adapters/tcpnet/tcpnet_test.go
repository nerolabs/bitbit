package tcpnet_test

import (
	"bytes"
	"context"
	"math/rand"
	"testing"
	"time"

	"github.com/nerolabs/silt/adapters/eventloop"
	"github.com/nerolabs/silt/adapters/identity"
	"github.com/nerolabs/silt/adapters/memstore"
	"github.com/nerolabs/silt/adapters/tcpnet"
	"github.com/nerolabs/silt/adapters/walltime"
	"github.com/nerolabs/silt/core/crypto"
	"github.com/nerolabs/silt/core/link"
	"github.com/nerolabs/silt/core/node"
	"github.com/nerolabs/silt/core/pipeline"
	"github.com/nerolabs/silt/core/registry"
	"github.com/nerolabs/silt/ports"
)

type realNode struct {
	loop *eventloop.Loop
	tr   *tcpnet.Transport
	nd   *node.Node
}

// call runs fn on the node's loop and waits for the returned signal —
// the bridge between test-goroutine land and each node's single thread.
func (r *realNode) call(t *testing.T, timeout time.Duration, fn func(done func())) {
	t.Helper()
	ch := make(chan struct{})
	r.loop.Post(func() { fn(func() { close(ch) }) })
	select {
	case <-ch:
	case <-time.After(timeout):
		t.Fatal("timed out waiting for node operation")
	}
}

// The bet from the HANDOFF, still standing after M10: the identical
// core that runs on simnet runs here over mutual-TLS sockets with
// pinned identities, with only adapters swapped.
func TestScatterOverRealTLS(t *testing.T) {
	const nNodes = 5
	rng := rand.New(rand.NewSource(1))
	cfg := node.DefaultConfig()
	cfg.RequestTimeout = ports.Duration(2 * time.Second) // real networks, real slack
	reg := registry.New()

	var nodes []*realNode
	for i := 0; i < nNodes; i++ {
		ident := identity.FromSeed(int64(100 + i))
		loop := eventloop.New()
		go loop.Run()
		tr, err := tcpnet.New(loop, ident, "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		nd := node.New(ident.NodeID(), cfg, walltime.New(loop), tr, memstore.New())
		nodes = append(nodes, &realNode{loop: loop, tr: tr, nd: nd})
	}
	defer func() {
		for _, r := range nodes {
			r.tr.Close()
			r.loop.Stop()
		}
	}()

	// Everyone knows node 0's identity+address; the rest is learned on
	// the wire, every hop TLS-pinned.
	for i := 1; i < nNodes; i++ {
		nodes[i].tr.AddPeer(nodes[0].nd.ID(), nodes[0].tr.Addr())
	}
	for i := 1; i < nNodes; i++ {
		i := i
		nodes[i].call(t, 10*time.Second, func(done func()) {
			nodes[i].nd.Bootstrap([]ports.NodeID{nodes[0].nd.ID()}, func() { done() })
		})
	}

	data := make([]byte, 128<<10)
	rng.Read(data)
	a, z := nodes[0], nodes[nNodes-1]
	var h link.Handle
	a.call(t, 30*time.Second, func(done func()) {
		var err error
		h, err = pipeline.Add(context.Background(), a.nd.Store(), reg, bytes.NewReader(data),
			pipeline.Options{ChunkSize: 8 << 10, Mode: crypto.Convergent})
		if err != nil {
			t.Errorf("add: %v", err)
			done()
			return
		}
		entry, _, _ := reg.Lookup(context.Background(), h.Root)
		m, err := pipeline.LoadFull(context.Background(), a.nd.Store(), entry, h)
		if err != nil {
			t.Errorf("load manifest: %v", err)
			done()
			return
		}
		a.nd.Distribute(entry, m, false, func(int, error) { done() })
	})

	var out bytes.Buffer
	z.call(t, 60*time.Second, func(done func()) {
		z.nd.NetGet(reg, h, &out, func(err error) {
			if err != nil {
				t.Errorf("netget: %v", err)
			}
			done()
		})
	})
	if !bytes.Equal(out.Bytes(), data) {
		t.Fatal("bytes differ after roundtrip over TLS")
	}

	// Peer exchange happened over the wire: node Z should now know more
	// peers than the single seed it was given.
	if got := len(z.tr.Peers()); got < 3 {
		t.Fatalf("Z's address book has %d peers after traffic, want ≥3 (peer exchange broken?)", got)
	}
}

// An impostor at a known address must get silence: the dialer pins the
// expected identity and the handshake fails against the wrong key.
func TestImpostorGetsNothing(t *testing.T) {
	honest := identity.FromSeed(201)
	impostor := identity.FromSeed(202)

	loopA := eventloop.New()
	go loopA.Run()
	defer loopA.Stop()
	trA, err := tcpnet.New(loopA, identity.FromSeed(200), "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer trA.Close()

	loopB := eventloop.New()
	go loopB.Run()
	defer loopB.Stop()
	trB, err := tcpnet.New(loopB, impostor, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer trB.Close()

	delivered := make(chan struct{}, 1)
	trB.SetHandler(func(ports.NodeID, ports.Message) { delivered <- struct{}{} })

	// A's address book claims HONEST lives at the impostor's address.
	trA.AddPeer(honest.NodeID(), trB.Addr())
	if err := trA.Send(honest.NodeID(), ports.Message{Kind: ports.MsgFindNode}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-delivered:
		t.Fatal("impostor received a message meant for another identity")
	case <-time.After(500 * time.Millisecond):
		// silence: exactly right
	}
}
