package main

import (
	"context"
	"crypto/rand"
	"flag"
	"fmt"
	"os"

	"github.com/nerolabs/silt/adapters/discovery"
	"github.com/nerolabs/silt/core/blindtoken"
	"github.com/nerolabs/silt/core/crypto"
	"github.com/nerolabs/silt/core/link"
	"github.com/nerolabs/silt/core/node"
	"github.com/nerolabs/silt/core/pipeline"
	"github.com/nerolabs/silt/ports"
)

// acquirePublishToken fetches the validators' token-issuer keys, then collects
// k blind signatures into an unlinkable publish token (T3). Runs on the node's
// loop; cont fires once with the token or an error.
func acquirePublishToken(nd *node.Node, validators []ports.NodeID, k int, cont func(*ports.PublishToken, error)) {
	var fetchNext func(i int)
	fetchNext = func(i int) {
		if i >= len(validators) {
			serial, err := blindtoken.NewSerial(rand.Reader)
			if err != nil {
				cont(nil, err)
				return
			}
			nd.AcquireToken(rand.Reader, serial, validators, nd.IssuerKeyOf, k, cont)
			return
		}
		nd.FetchIssuerKey(validators[i], func(error) { fetchNext(i + 1) }) // best-effort; AcquireToken handles shortfall
	}
	fetchNext(0)
}

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
	tokenQuorum := fs.Int("token-quorum", 0, "publisher privacy: acquire a publish token from this many of the -peers (validators) so the publish carries no Publisher identity (0 = off)")
	allowPublisher := fs.Bool("allow-publisher", false, "record this node's durable Publisher identity on the entry (permanent linkage; off by default for privacy — prefer -token-quorum or an ungated publish)")
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

	var validators []ports.NodeID
	if ps, perr := discovery.ParseList(*peers); perr == nil {
		for _, p := range ps {
			validators = append(validators, p.ID)
		}
	}

	var h link.Handle
	var placed int
	err = nil
	if rerr := run(func(done func()) {
		publish := func(tok *ports.PublishToken) {
			opts := pipeline.Options{ChunkSize: *chunkSize, Mode: m}
			switch {
			case tok != nil:
				opts.Token = tok // unlinkable: no Publisher identity
			case *allowPublisher:
				opts.Publisher = e.nd.ID() // opt-in: records permanent linkage
			}
			// Default (neither): publish carries no durable identity — M0-safe
			// on the chain; a credit-gated registry will refuse it, which is
			// the signal to pass -token-quorum or -allow-publisher.
			// Stage stores the chunks + manifest but does NOT register the
			// entry yet; we publish only after a confirmed scatter, so a
			// placement failure never leaves a dangling registry entry that
			// no link reaches (register-after-distribute, #65).
			var aerr error
			var entry ports.Entry
			h, entry, aerr = pipeline.Stage(context.Background(), e.nd.Store(), f, opts)
			if aerr != nil {
				err = aerr
				done()
				return
			}
			mf, merr := pipeline.LoadFull(context.Background(), e.nd.Store(), entry, h)
			if merr != nil {
				err = merr
				done()
				return
			}
			e.nd.Distribute(entry, mf, false, func(p int, derr error) {
				// Publish only on a confirmed scatter; a failed one leaves the
				// registry untouched so no dangling entry survives (#65).
				placed, err = pipeline.RegisterAfterDistribute(context.Background(), reg, entry, p, derr)
				done()
			})
		}
		if *tokenQuorum > 0 {
			acquirePublishToken(e.nd, validators, *tokenQuorum, func(tok *ports.PublishToken, aerr error) {
				if aerr != nil {
					err = aerr
					done()
					return
				}
				publish(tok)
			})
		} else {
			publish(nil)
		}
	}); rerr != nil {
		return rerr
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "scattered %d chunk replicas into the swarm; client keeps nothing\n", placed)
	fmt.Fprintf(os.Stderr, "care link (repair rights, no decryption): %s\n", h.Care())
	fmt.Fprintln(os.Stderr, "note: no caretaker is running for this content yet — its redundancy will")
	fmt.Fprintln(os.Stderr, "decay as nodes churn. Run a daemon with -care <careLink> to repair it")
	fmt.Fprintln(os.Stderr, "(publishing via a daemon's UI caretakes your content automatically).")
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
