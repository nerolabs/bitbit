package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/nerolabs/silt/core/crypto"
	"github.com/nerolabs/silt/core/link"
	"github.com/nerolabs/silt/core/pipeline"
)

// cmdSwarm publishes to / retrieves from a running daemon swarm using a
// short-lived client node: join, do the thing, leave. The swarm keeps
// the data; the client keeps nothing.
func cmdSwarm(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: silt swarm add|get ... -peers ID@ADDR[,...] -registry URL")
	}
	switch args[0] {
	case "add":
		return swarmAdd(args[1:])
	case "get":
		return swarmGet(args[1:])
	default:
		return fmt.Errorf("unknown swarm command %q (add, get)", args[0])
	}
}

func swarmAdd(args []string) error {
	fs := flag.NewFlagSet("swarm add", flag.ExitOnError)
	peers := fs.String("peers", "", "bootstrap peers: ID@HOST:PORT[,...] (required)")
	regURL := fs.String("registry", "", "registry URL (required)")
	mode := fs.String("mode", "convergent", "encryption mode")
	chunkSize := fs.Int("chunk-size", pipeline.DefaultChunkSize, "chunk size in bytes")
	pos := parseFlexible(fs, args)
	if len(pos) != 1 || *peers == "" || *regURL == "" {
		return fmt.Errorf("usage: silt swarm add <file> -peers ID@ADDR -registry URL [flags]")
	}
	m, err := crypto.ParseMode(*mode)
	if err != nil {
		return err
	}
	f, err := os.Open(pos[0])
	if err != nil {
		return err
	}
	defer f.Close()

	e, run, err := joinSwarm(*peers)
	if err != nil {
		return err
	}
	defer e.close()
	reg, err := openRegistry(*regURL)
	if err != nil {
		return err
	}

	var h link.Handle
	var placed int
	err = nil
	if rerr := run(func(done func()) {
		var aerr error
		h, aerr = pipeline.Add(context.Background(), e.nd.Store(), reg, f, pipeline.Options{
			ChunkSize: *chunkSize,
			Mode:      m,
			Publisher: e.nd.ID(),
		})
		if aerr != nil {
			err = aerr
			done()
			return
		}
		entry, _, _ := reg.Lookup(context.Background(), h.Root)
		mf, merr := pipeline.LoadFull(context.Background(), e.nd.Store(), entry, h)
		if merr != nil {
			err = merr
			done()
			return
		}
		e.nd.Distribute(entry, mf, false, func(p int) { placed = p; done() })
	}); rerr != nil {
		return rerr
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "scattered %d chunk replicas into the swarm; client keeps nothing\n", placed)
	fmt.Fprintf(os.Stderr, "care link (repair rights, no decryption): %s\n", h.Care())
	fmt.Println(h)
	return nil
}

func swarmGet(args []string) error {
	fs := flag.NewFlagSet("swarm get", flag.ExitOnError)
	peers := fs.String("peers", "", "bootstrap peers: ID@HOST:PORT[,...] (required)")
	regURL := fs.String("registry", "", "registry URL (required)")
	out := fs.String("o", "", "output file (required)")
	pos := parseFlexible(fs, args)
	if len(pos) != 1 || *peers == "" || *regURL == "" || *out == "" {
		return fmt.Errorf("usage: silt swarm get <link> -o <out> -peers ID@ADDR -registry URL")
	}
	h, err := link.Parse(pos[0])
	if err != nil {
		return err
	}
	e, run, err := joinSwarm(*peers)
	if err != nil {
		return err
	}
	defer e.close()
	reg, err := openRegistry(*regURL)
	if err != nil {
		return err
	}

	f, err := os.Create(*out)
	if err != nil {
		return err
	}
	var getErr error
	if rerr := run(func(done func()) {
		e.nd.NetGet(reg, h, f, func(err error) { getErr = err; done() })
	}); rerr != nil {
		f.Close()
		os.Remove(*out)
		return rerr
	}
	if getErr != nil {
		f.Close()
		os.Remove(*out)
		return getErr
	}
	return f.Close()
}
