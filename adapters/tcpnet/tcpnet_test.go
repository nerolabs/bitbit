package tcpnet_test

import (
	"bytes"
	"context"
	"math/rand"
	"testing"
	"time"

	"shardnet/adapters/eventloop"
	"shardnet/adapters/memstore"
	"shardnet/adapters/tcpnet"
	"shardnet/adapters/walltime"
	"shardnet/core/crypto"
	"shardnet/core/node"
	"shardnet/core/pipeline"
	"shardnet/core/registry"
	"shardnet/ports"
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

// The bet from the HANDOFF, settled: the identical core — node, dht,
// pipeline, erasure, crypto — that runs on simnet runs here over real
// TCP sockets on localhost, with only adapters swapped.
func TestScatterOverRealTCP(t *testing.T) {
	const nNodes = 5
	rng := rand.New(rand.NewSource(1))
	cfg := node.DefaultConfig()
	cfg.RequestTimeout = ports.Duration(2 * time.Second) // real networks, real slack
	reg := registry.New()

	var nodes []*realNode
	for i := 0; i < nNodes; i++ {
		var id ports.NodeID
		rng.Read(id[:])
		loop := eventloop.New()
		go loop.Run()
		tr, err := tcpnet.New(loop, id, "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		nd := node.New(id, cfg, walltime.New(loop), tr, memstore.New())
		nodes = append(nodes, &realNode{loop: loop, tr: tr, nd: nd})
	}
	defer func() {
		for _, r := range nodes {
			r.tr.Close()
			r.loop.Stop()
		}
	}()

	// Everyone knows node 0's address; the rest is learned on the wire.
	for i := 1; i < nNodes; i++ {
		nodes[i].tr.AddPeer(nodes[0].nd.ID(), nodes[0].tr.Addr())
	}
	for i := 1; i < nNodes; i++ {
		i := i
		nodes[i].call(t, 10*time.Second, func(done func()) {
			nodes[i].nd.Bootstrap([]ports.NodeID{nodes[0].nd.ID()}, func() { done() })
		})
	}

	// A adds a file locally, scatters it across the swarm, keeps nothing.
	data := make([]byte, 128<<10)
	rng.Read(data)
	a, z := nodes[0], nodes[nNodes-1]
	var root ports.Hash
	a.call(t, 30*time.Second, func(done func()) {
		var err error
		root, err = pipeline.Add(context.Background(), a.nd.Store(), reg, bytes.NewReader(data),
			pipeline.Options{ChunkSize: 8 << 10, Mode: crypto.Convergent})
		if err != nil {
			t.Errorf("add: %v", err)
			done()
			return
		}
		entry, _, _ := reg.Lookup(context.Background(), root)
		m, err := pipeline.LoadManifest(context.Background(), a.nd.Store(), entry)
		if err != nil {
			t.Errorf("load manifest: %v", err)
			done()
			return
		}
		a.nd.Distribute(entry, m, false, func(int) { done() })
	})

	// Z, holding only the root hash, pulls it back through real sockets.
	var out bytes.Buffer
	z.call(t, 60*time.Second, func(done func()) {
		z.nd.NetGet(reg, root, &out, func(err error) {
			if err != nil {
				t.Errorf("netget: %v", err)
			}
			done()
		})
	})
	if !bytes.Equal(out.Bytes(), data) {
		t.Fatal("bytes differ after roundtrip over TCP")
	}
}
