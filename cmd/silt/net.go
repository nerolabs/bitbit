package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"math/rand"
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

// cmdNet runs the scatter story over REAL TCP sockets on localhost:
// every node gets its own event loop, wall clock, and listener; the
// core code is byte-for-byte the same code the simulator runs.
func cmdNet(args []string) error {
	if len(args) < 1 || args[0] != "demo" {
		return fmt.Errorf("usage: silt net demo [-nodes N] [-size B] [-seed S]")
	}
	fs := flag.NewFlagSet("net demo", flag.ExitOnError)
	nNodes := fs.Int("nodes", 5, "number of nodes (each with a real TCP listener)")
	size := fs.Int("size", 1<<20, "file size in bytes")
	seed := fs.Int64("seed", 1, "seed for IDs and file content (network timing stays real)")
	fs.Parse(args[1:])

	rng := rand.New(rand.NewSource(*seed))
	cfg := node.DefaultConfig()
	cfg.RequestTimeout = ports.Duration(2 * time.Second)
	reg := registry.New()

	type peer struct {
		loop *eventloop.Loop
		tr   *tcpnet.Transport
		nd   *node.Node
	}
	var peers []*peer
	for i := 0; i < *nNodes; i++ {
		ident := identity.FromSeed(*seed*1000 + int64(i))
		id := ident.NodeID()
		loop := eventloop.New()
		go loop.Run()
		tr, err := tcpnet.New(loop, ident, "127.0.0.1:0")
		if err != nil {
			return err
		}
		nd := node.New(id, cfg, walltime.New(loop), tr, memstore.New())
		peers = append(peers, &peer{loop, tr, nd})
		fmt.Printf("node %d  %s…  listening on %s\n", i, id.String()[:12], tr.Addr())
	}
	defer func() {
		for _, p := range peers {
			p.tr.Close()
			p.loop.Stop()
		}
	}()

	wait := func(p *peer, timeout time.Duration, fn func(done func())) error {
		ch := make(chan struct{})
		p.loop.Post(func() { fn(func() { close(ch) }) })
		select {
		case <-ch:
			return nil
		case <-time.After(timeout):
			return fmt.Errorf("timed out")
		}
	}

	for i := 1; i < *nNodes; i++ {
		peers[i].tr.AddPeer(peers[0].nd.ID(), peers[0].tr.Addr())
		if err := wait(peers[i], 10*time.Second, func(done func()) {
			peers[i].nd.Bootstrap([]ports.NodeID{peers[0].nd.ID()}, func() { done() })
		}); err != nil {
			return fmt.Errorf("bootstrap node %d: %w", i, err)
		}
	}
	fmt.Printf("\nall %d nodes bootstrapped (everyone was told only node 0's address)\n", *nNodes)

	data := make([]byte, *size)
	rng.Read(data)
	a, z := peers[0], peers[*nNodes-1]

	var h link.Handle
	start := time.Now()
	if err := wait(a, 60*time.Second, func(done func()) {
		var err error
		h, err = pipeline.Add(context.Background(), a.nd.Store(), reg, bytes.NewReader(data),
			pipeline.Options{ChunkSize: 64 << 10, Mode: crypto.Convergent})
		if err != nil {
			fmt.Println("add:", err)
			done()
			return
		}
		entry, _, _ := reg.Lookup(context.Background(), h.Root)
		m, merr := pipeline.LoadFull(context.Background(), a.nd.Store(), entry, h)
		if merr != nil {
			fmt.Println("manifest:", merr)
			done()
			return
		}
		a.nd.Distribute(entry, m, false, node.DerivePorKey(h.LayoutKey()), func(placed int, derr error) {
			if derr != nil {
				fmt.Println("distribute:", derr)
				done()
				return
			}
			fmt.Printf("node 0 scattered %d chunk replicas over TCP and deleted its copies (%.0fms)\n",
				placed, time.Since(start).Seconds()*1000)
			done()
		})
	}); err != nil {
		return fmt.Errorf("distribute: %w", err)
	}
	fmt.Printf("link: %s\n\n", h)

	var out bytes.Buffer
	var getErr error
	start = time.Now()
	if err := wait(z, 120*time.Second, func(done func()) {
		z.nd.NetGet(reg, h, &out, func(err error) { getErr = err; done() })
	}); err != nil {
		return fmt.Errorf("netget: %w", err)
	}
	if getErr != nil {
		return fmt.Errorf("netget: %w", getErr)
	}
	if !bytes.Equal(out.Bytes(), data) {
		return fmt.Errorf("bytes differ after TCP roundtrip")
	}
	fmt.Printf("node %d retrieved %d bytes through real sockets in %.0fms — BIT-PERFECT\n",
		*nNodes-1, out.Len(), time.Since(start).Seconds()*1000)
	fmt.Println("\nsame core code as the sim; only the adapters changed. That was the bet.")
	return nil
}
