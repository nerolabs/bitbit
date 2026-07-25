package main

// The desktop client (M14): a long-lived node that CONSUMES and SERVES
// in the same process — Andrew's "every client is also a serving node".
// It pledges a slice of disk by default (so downloading and contributing
// are the same act), bootstraps via discovery, keeps a link book (the
// files you hold keys for), serves the local web UI, and opens your
// browser to the library. One Go binary; `go build` targets Mac,
// Windows, and Linux from the same source (see build.sh).

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/nerolabs/silt/adapters/capstore"
	"github.com/nerolabs/silt/adapters/discovery"
	"github.com/nerolabs/silt/adapters/diskstore"
	"github.com/nerolabs/silt/adapters/eventloop"
	"github.com/nerolabs/silt/adapters/identity"
	"github.com/nerolabs/silt/adapters/linkbook"
	"github.com/nerolabs/silt/adapters/tcpnet"
	"github.com/nerolabs/silt/adapters/walltime"
	"github.com/nerolabs/silt/core/node"
	"github.com/nerolabs/silt/ports"
)

func cmdClient(args []string) error {
	fs := flag.NewFlagSet("client", flag.ExitOnError)
	home, _ := os.UserHomeDir()
	defDir := filepath.Join(home, ".silt")
	storeDir := fs.String("store", defDir, "client data directory")
	listen := fs.String("listen", "0.0.0.0:0", "TCP listen address for swarm traffic")
	capacity := fs.String("capacity", "5G", "storage you contribute to the network (0 = consume only)")
	bootstrap := fs.String("bootstrap", "", "bootstrap peers: ID@HOST:PORT[,...]")
	dnsSeed := fs.String("dns-seed", "", "domain whose TXT records list bootstrap peers")
	registryURL := fs.String("registry", "", "registry ref for browsing/publishing (ID@https://host:port)")
	uiAddr := fs.String("ui", "127.0.0.1:8090", "local web UI address")
	open := fs.Bool("open", true, "open the library in your browser on start")
	fs.Parse(args)

	if err := os.MkdirAll(*storeDir, 0o755); err != nil {
		return err
	}
	ident, err := identity.LoadOrCreate(filepath.Join(*storeDir, "identity.pem"))
	if err != nil {
		return err
	}
	id := ident.NodeID()

	loop := eventloop.New()
	tr, err := tcpnet.New(loop, ident, *listen)
	if err != nil {
		return err
	}

	// Pledge disk unless told to consume only. A client that contributes
	// is a healthier network than one that only takes.
	var store ports.ChunkStore
	disk, err := diskstore.Open(*storeDir)
	if err != nil {
		return err
	}
	store = disk
	if *capacity != "0" && *capacity != "" {
		pledge, err := parseSize(*capacity)
		if err != nil {
			return err
		}
		store, err = capstore.Open(disk, pledge)
		if err != nil {
			return err
		}
		fmt.Printf("contributing: %s of disk to the network\n", *capacity)
	} else {
		fmt.Println("consume-only: not contributing storage (consider dropping -capacity 0)")
	}

	nd := node.New(id, node.DefaultConfig(), walltime.New(loop), tr, store)

	var reg ports.Registry
	if *registryURL != "" {
		if reg, err = openRegistry(*registryURL); err != nil {
			return err
		}
	}

	links, err := linkbook.Open(filepath.Join(*storeDir, "links.json"))
	if err != nil {
		return err
	}

	fmt.Printf("identity: %s\n", id)
	fmt.Printf("peer:     %s@%s\n", id, tr.Addr())

	// Discovery: flags, DNS, and the persisted address book.
	peersPath := filepath.Join(*storeDir, "peers.json")
	var seeds []ports.NodeID
	addSeeds := func(peers []tcpnet.Peer, src string) {
		for _, p := range peers {
			if p.ID == id {
				continue
			}
			tr.AddPeer(p.ID, p.Addr)
			seeds = append(seeds, p.ID)
		}
		if len(peers) > 0 {
			fmt.Printf("discovery: %d peer(s) via %s\n", len(peers), src)
		}
	}
	if *bootstrap != "" {
		ps, err := discovery.ParseList(*bootstrap)
		if err != nil {
			return err
		}
		addSeeds(ps, "-bootstrap")
	}
	if *dnsSeed != "" {
		if ps, err := discovery.FromDNS(*dnsSeed); err == nil {
			addSeeds(ps, "dns:"+*dnsSeed)
		}
	}
	if ps, err := discovery.LoadFile(peersPath); err == nil {
		addSeeds(ps, "peers.json")
	}
	go func() {
		for range time.Tick(30 * time.Second) {
			discovery.SaveFile(peersPath, tr.Peers())
		}
	}()

	// Serve the UI (library-first).
	var capRep ports.CapacityReporter
	if rep, ok := store.(ports.CapacityReporter); ok {
		capRep = rep
	}
	ui := &uiServer{
		loop: loop, nd: nd, reg: reg, capRep: capRep,
		selfPeer:  fmt.Sprintf("%s@%s", id, tr.Addr()),
		validator: false, started: time.Now(),
		peerCount: func() int { return len(tr.Peers()) },
		links:     links,
	}
	bound, err := ui.serve(*uiAddr)
	if err != nil {
		return err
	}
	url := "http://" + bound + "/library.html"
	fmt.Printf("library:  %s\n", url)

	loop.Post(func() {
		nd.Bootstrap(seeds, func() {
			fmt.Printf("connected (%d peers known)\n", nd.Table().Size())
			nd.AnnounceHeld(func(count int) {
				if count > 0 {
					fmt.Printf("re-announced %d held chunks\n", count)
				}
			})
		})
	})

	if *open {
		go openBrowser(url)
	}
	fmt.Println("running; Ctrl-C to stop")
	loop.Run()
	return nil
}

// openBrowser launches the platform's default browser — the one place
// the client is OS-aware, and it's three lines of os/exec, no native
// toolkit, so the single binary still builds for all three from source.
func openBrowser(url string) {
	time.Sleep(400 * time.Millisecond) // let the listener settle
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
	case "windows":
		cmd, args = "rundll32", []string{"url.dll,FileProtocolHandler"}
	default:
		cmd = "xdg-open"
	}
	exec.Command(cmd, append(args, url)...).Start()
}
