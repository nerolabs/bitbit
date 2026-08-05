// Command silt is the CLI. In v1 it runs against a local
// content-addressed disk store — but it only speaks through the ports,
// so pointing it at a real network later changes wiring, not logic.
package main

import (
	"context"
	"crypto/rand"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/nerolabs/silt/adapters/diskstore"
	"github.com/nerolabs/silt/adapters/fileregistry"
	"github.com/nerolabs/silt/core/crypto"
	"github.com/nerolabs/silt/core/erasure"
	"github.com/nerolabs/silt/core/link"
	"github.com/nerolabs/silt/core/pipeline"
	"github.com/nerolabs/silt/ports"
)

const usage = `silt — content-addressed, erasure-coded file store (v1: local sim)

Usage:
  silt add <file>  [-store DIR] [-mode convergent|private] [-chunk-size N] [-k K] [-n N]
  silt get <link>  -o <out>    [-store DIR]
  silt info <link-or-care-link> [-store DIR]
  silt ls          [-store DIR]
  silt sim run scatter [-nodes N] [-seed S] [-size B] [-loss P] [-kill K]
  silt sim run churn   [-nodes N] [-seed S] [-kill-frac F] [-waves W] [-caretakers C]
  silt sim run economy [-nodes N] [-seed S] [-freeloaders F] [-fee C]
  silt sim run audit   [-nodes N] [-seed S] [-liars L]
  silt sim run capacity | consensus | bondstanding | takedown  [-nodes N] [-seed S]
  silt net demo        [-nodes N] [-size B]   (real TCP sockets)

  silt id           [-id-seed N | -store DIR] [-listen ADDR]   (print a node's ID without launching it)
  silt chain-status [-store DIR]                               (read-only: head height + hash of a validator's chain)

  silt daemon [-listen ADDR] [-store DIR] [-capacity 2G] [-bootstrap ID@ADDR,...]
                [-dns-seed DOMAIN] [-serve-registry ADDR | -registry REF] [-care CARELINK,...]
                [-validator -min-rep N -quorum Q -attesters ID[,...] -bond 8M]
  silt swarm add <file>  -peers ID@ADDR[,...] -registry REF
  silt swarm get <link>  -o <out> -peers ID@ADDR[,...] -registry REF

  silt client [-store DIR] [-capacity 5G] [-bootstrap ID@ADDR,...]
                [-registry REF] [-ui ADDR]   (desktop app: serves + consumes, opens a browser UI)
  silt genesis [-text]                     (the founding block every fresh network carries)

'add' prints the file's silt link — root (public name) plus key
(private capability); the care link it also prints grants repair
rights without decryption. Files are erasure-coded (default k=10,
n=16): any k shards of each stripe reconstruct it.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "add":
		err = cmdAdd(os.Args[2:])
	case "get":
		err = cmdGet(os.Args[2:])
	case "ls":
		err = cmdLs(os.Args[2:])
	case "info":
		err = cmdInfo(os.Args[2:])
	case "sim":
		err = cmdSim(os.Args[2:])
	case "net":
		err = cmdNet(os.Args[2:])
	case "id":
		err = cmdID(os.Args[2:])
	case "chain-status":
		err = cmdChainStatus(os.Args[2:])
	case "daemon":
		err = cmdDaemon(os.Args[2:])
	case "client":
		err = cmdClient(os.Args[2:])
	case "genesis":
		err = cmdGenesis(os.Args[2:])
	case "swarm":
		err = cmdSwarm(os.Args[2:])
	case "-h", "--help", "help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "silt:", err)
		os.Exit(1)
	}
}

// parseFlexible lets flags appear after the positional argument
// (stdlib flag stops at the first non-flag): parse, keep the leading
// positional, then parse the remainder as flags again.
func parseFlexible(fs *flag.FlagSet, args []string) []string {
	fs.Parse(args)
	var positionals []string
	for fs.NArg() > 0 {
		positionals = append(positionals, fs.Arg(0))
		fs.Parse(fs.Args()[1:])
	}
	return positionals
}

func openBackend(storeDir string) (ports.ChunkStore, ports.Registry, error) {
	store, err := diskstore.Open(storeDir)
	if err != nil {
		return nil, nil, err
	}
	reg, err := fileregistry.Open(filepath.Join(storeDir, "registry.jsonl"))
	if err != nil {
		return nil, nil, err
	}
	return store, reg, nil
}

func cmdAdd(args []string) error {
	fs := flag.NewFlagSet("add", flag.ExitOnError)
	storeDir := fs.String("store", ".silt", "store directory")
	mode := fs.String("mode", "private", "encryption mode: private (default — a random per-file key, so identical content encrypts differently and can't be probed for) or convergent (a plaintext-derived key that dedups identical content across the network, but lets anyone who GUESSES your exact plaintext confirm you stored it — the confirmation attack; M0 H6, only for non-secret/shared data)")
	chunkSize := fs.Int("chunk-size", pipeline.DefaultChunkSize, "chunk size in bytes")
	k := fs.Int("k", erasure.DefaultParams.K, "erasure data shards per stripe")
	n := fs.Int("n", erasure.DefaultParams.N, "erasure total shards per stripe (any k of n recover)")
	pos := parseFlexible(fs, args)
	if len(pos) != 1 {
		return fmt.Errorf("usage: silt add <file> [flags]")
	}

	m, err := crypto.ParseMode(*mode)
	if err != nil {
		return err
	}
	if m == crypto.Convergent {
		fmt.Fprintln(os.Stderr, "warning: -mode convergent dedups identical content but is deterministic — anyone who GUESSES your exact plaintext can confirm you stored it (the confirmation attack). Use it only for non-secret, shared data.")
	}
	f, err := os.Open(pos[0])
	if err != nil {
		return err
	}
	defer f.Close()
	store, reg, err := openBackend(*storeDir)
	if err != nil {
		return err
	}

	h, err := pipeline.Add(context.Background(), store, reg, f, pipeline.Options{
		ChunkSize: *chunkSize,
		Mode:      m,
		Erasure:   erasure.Params{K: *k, N: *n},
		Rand:      rand.Reader, // real entropy at the edge; core stays injectable
	})
	if err != nil {
		return err
	}
	// The share link leads (labelled, on stderr) with the bare link on
	// stdout so `silt add file` stays pipeable; the specialist care link
	// comes after, clearly caveated.
	fmt.Fprintln(os.Stderr, "link to share (lets anyone fetch the file):")
	fmt.Println(h)
	fmt.Fprintf(os.Stderr, "care link (repair only, cannot decrypt): %s\n", h.Care())
	return nil
}

func cmdGet(args []string) error {
	fs := flag.NewFlagSet("get", flag.ExitOnError)
	storeDir := fs.String("store", ".silt", "store directory")
	out := fs.String("o", "", "output file (required)")
	pos := parseFlexible(fs, args)
	if len(pos) != 1 || *out == "" {
		return fmt.Errorf("usage: silt get <link> -o <out> [flags]")
	}
	h, err := link.Parse(pos[0])
	if err != nil {
		if _, cerr := link.ParseCare(pos[0]); cerr == nil {
			return fmt.Errorf("that's a care link — repair/audit only, it cannot decrypt content.\n"+
				"  inspect its structure:   silt info %s\n"+
				"  repair it as a caretaker: silt daemon -care %s\n"+
				"  to fetch the bytes you need the full silt:v1:ROOT:KEY link", pos[0], pos[0])
		}
		return err
	}
	store, reg, err := openBackend(*storeDir)
	if err != nil {
		return err
	}
	f, err := os.Create(*out)
	if err != nil {
		return err
	}
	if err := pipeline.Get(context.Background(), store, reg, h, f); err != nil {
		f.Close()
		os.Remove(*out) // don't leave a partial, unverified file behind
		return err
	}
	return f.Close()
}

// cmdInfo prints the stripe map of a stored file — which shard IDs make
// up each stripe and how many of them can be lost. It exists to make the
// erasure math tangible: pick shards from the printout, delete their
// files from .silt/objects, watch get shrug it off.
func cmdInfo(args []string) error {
	fs := flag.NewFlagSet("info", flag.ExitOnError)
	storeDir := fs.String("store", ".silt", "store directory")
	shards := fs.Bool("shards", false, "list every shard hash per stripe (verbose)")
	pos := parseFlexible(fs, args)
	if len(pos) != 1 {
		return fmt.Errorf("usage: silt info <link-or-care-link> [-shards] [flags]")
	}
	ch, err := link.ParseAnyCare(pos[0])
	if err != nil {
		return err
	}
	store, reg, err := openBackend(*storeDir)
	if err != nil {
		return err
	}
	ctx := context.Background()
	entry, ok, err := reg.Lookup(ctx, ch.Root)
	if err != nil {
		return err
	}
	if !ok {
		return ports.ErrNoSuchEntry
	}
	m, err := pipeline.LoadLayout(ctx, store, entry, ch)
	if err != nil {
		return err
	}

	fmt.Printf("root:       %s\n", ch.Root)
	if full, err := link.Parse(pos[0]); err == nil {
		// A full link additionally opens the content facts.
		if fm, ferr := pipeline.LoadFull(ctx, store, entry, full); ferr == nil {
			fmt.Printf("mode:       %s\n", fm.Mode)
			fmt.Printf("file size:  %d bytes\n", fm.FileSize)
		}
	} else {
		fmt.Println("mode:       (sealed — care link shows structure only)")
	}
	fmt.Printf("chunks:     %d (chunk size %d)\n", len(m.Chunks), m.ChunkSize)
	if m.K == 0 {
		fmt.Println("erasure:    none (uncoded)")
		return nil
	}
	p := erasure.Params{K: m.K, N: m.N}
	dataIDs, parityIDs := m.ChunkIDs(), m.ParityIDs()
	stripes := p.Stripes(len(dataIDs))
	fmt.Printf("erasure:    (k=%d, n=%d) — any %d of each stripe's shards recover it; up to %d losses per stripe are survivable\n",
		m.K, m.N, m.K, p.ParityShards())
	fmt.Printf("stripes:    %d (%d data + %d parity shards)\n", stripes, len(dataIDs), len(parityIDs))
	if !*shards {
		fmt.Println("\nrun with -shards to list every shard hash.")
		return nil
	}
	present := func(id ports.ChunkID) string {
		if ok, _ := store.Has(ctx, id); ok {
			return " "
		}
		return "✗"
	}
	for j := 0; j < stripes; j++ {
		lo := j * p.K
		hi := min(lo+p.K, len(dataIDs))
		fmt.Printf("stripe %d:\n", j)
		for i, id := range dataIDs[lo:hi] {
			fmt.Printf("  %s data   %2d  %s\n", present(id), lo+i, id)
		}
		for q, id := range parityIDs[j*p.ParityShards() : (j+1)*p.ParityShards()] {
			fmt.Printf("  %s parity %2d  %s\n", present(id), q, id)
		}
	}
	return nil
}

func cmdLs(args []string) error {
	fs := flag.NewFlagSet("ls", flag.ExitOnError)
	storeDir := fs.String("store", ".silt", "store directory")
	fs.Parse(args)
	_, reg, err := openBackend(*storeDir)
	if err != nil {
		return err
	}
	entries, err := reg.All(context.Background())
	if err != nil {
		return err
	}
	for _, e := range entries {
		fmt.Printf("%s  %10d bytes  %d manifest chunk(s)\n", e.Root, e.FileSize, len(e.ManifestChunks))
	}
	return nil
}
