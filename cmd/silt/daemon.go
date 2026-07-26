package main

import (
	"crypto/rand"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nerolabs/silt/adapters/capstore"
	"github.com/nerolabs/silt/adapters/chainhost"
	"github.com/nerolabs/silt/adapters/chainstore"
	"github.com/nerolabs/silt/adapters/discovery"
	"github.com/nerolabs/silt/adapters/diskstore"
	"github.com/nerolabs/silt/adapters/eventloop"
	"github.com/nerolabs/silt/adapters/fileregistry"
	"github.com/nerolabs/silt/adapters/httpregistry"
	"github.com/nerolabs/silt/adapters/identity"
	"github.com/nerolabs/silt/adapters/lan"
	"github.com/nerolabs/silt/adapters/logfile"
	"github.com/nerolabs/silt/adapters/memstore"
	"github.com/nerolabs/silt/adapters/relay"
	"github.com/nerolabs/silt/adapters/tcpnet"
	"github.com/nerolabs/silt/adapters/walltime"
	"github.com/nerolabs/silt/core/chain"
	"github.com/nerolabs/silt/core/credit"
	"github.com/nerolabs/silt/core/denylist"
	"github.com/nerolabs/silt/core/genesis"
	"github.com/nerolabs/silt/core/link"
	"github.com/nerolabs/silt/core/node"
	"github.com/nerolabs/silt/ports"
)

// cmdDaemon runs a long-lived swarm node: real TCP listener, real disk
// store, wall clock. One daemon per swarm also hosts the registry
// (-serve-registry) — the v1 "single honest instance", now reachable
// over HTTP so separate processes (and machines) can share it.
func cmdDaemon(args []string) error {
	fs := flag.NewFlagSet("daemon", flag.ExitOnError)
	listen := fs.String("listen", "127.0.0.1:0", "TCP listen address for swarm traffic")
	storeDir := fs.String("store", ".silt-daemon", "chunk store directory")
	bootstrap := fs.String("bootstrap", "", "comma-separated peer list: ID@HOST:PORT")
	registryURL := fs.String("registry", "", "URL of the swarm registry (http://host:port)")
	serveRegistry := fs.String("serve-registry", "", "host the registry at this address (persisted in the store dir)")
	idSeed := fs.Int64("id-seed", 0, "derive the identity from a seed (default: persistent keyfile) — for scripted demos")
	care := fs.String("care", "", "comma-separated care links (siltcare:...) to repair — no decryption possible or needed")
	capacity := fs.String("capacity", "5G", "storage pledge, e.g. 2G, 500M (matches the client's default so the node contributes measurable, countable storage; \"\" = unlimited but doesn't count toward network storage)")
	dnsSeed := fs.String("dns-seed", "", "domain whose TXT records list bootstrap peers")
	mdns := fs.Bool("mdns", true, "announce and discover peers on the local network (LAN multicast); needs a non-loopback -listen")
	denylistPath := fs.String("denylist", "", "operator takedown list: a file of denied root hashes to refuse to store/serve (you choose which lists to honor)")
	validator := fs.Bool("validator", false, "keep a chain replica and take part in consensus")
	uiAddr := fs.String("ui", "", "serve the web UI at this address (e.g. 127.0.0.1:8081)")
	attesters := fs.String("attesters", "", "comma-separated validator IDs to gather attestations from")
	quorum := fs.Int("quorum", 1, "attestations required to commit a block")
	minRep := fs.Int64("min-rep", 0, "reputation threshold for proposers/attesters (0 = trusted deployment)")
	debug := fs.Bool("debug", false, "write warn/error and normal-path narration to <store>/debug.log — a failure in the field leaves an artifact")
	relayServe := fs.String("relay", "", "offer relay service at this address (e.g. 0.0.0.0:4002): content-blind ciphertext forwarding for NATed peers, capped; pointless unless this node is publicly reachable")
	relayVia := fs.String("relay-via", "", "RELAYID@HOST:PORT of a relay to lean on if this node turns out to be NATed — peers then reach us through it")
	advertise := fs.String("advertise", "", "publicly dialable HOST:PORT to stamp on outgoing messages — set this on a public box that listens on a wildcard address (a wildcard bind is never advertised on its own)")
	fs.Parse(args)

	// Identity is a keypair: NodeID = SHA-256(public key), persisted so
	// a daemon's reputation survives restarts.
	var ident *identity.Identity
	if *idSeed != 0 {
		ident = identity.FromSeed(*idSeed)
	} else {
		if err := os.MkdirAll(*storeDir, 0o755); err != nil {
			return err
		}
		var err error
		ident, err = identity.LoadOrCreate(filepath.Join(*storeDir, "identity.pem"))
		if err != nil {
			return err
		}
	}
	id := ident.NodeID()

	loop := eventloop.New()
	tr, err := tcpnet.New(loop, ident, *listen)
	if err != nil {
		return err
	}
	if *advertise != "" {
		tr.SetAdvertise(*advertise)
	}
	var store ports.ChunkStore
	disk, err := diskstore.Open(*storeDir)
	if err != nil {
		return err
	}
	store = disk
	if *capacity != "" {
		pledge, err := parseSize(*capacity)
		if err != nil {
			return err
		}
		capped, err := capstore.Open(disk, pledge)
		if err != nil {
			return err
		}
		store = capped
		used, total := capped.Capacity()
		fmt.Printf("pledge: %d / %d bytes used\n", used, total)
	}
	nd := node.New(id, node.Config{
		K: 8, Alpha: 3,
		RequestTimeout:      ports.Duration(2 * time.Second),
		Replication:         3,
		RepairInterval:      ports.Duration(60 * time.Second),
		RepairSlack:         2,
		ReachabilityTimeout: ports.Duration(3 * time.Second),
	}, walltime.New(loop), tr, store)

	// -debug: dlog adds the daemon's own milestones (discovery,
	// bootstrap) to the same artifact the node and transport narrate to.
	var lg *logfile.Sink
	if *debug {
		if lg, err = openDebugLog(*storeDir, tr, nd); err != nil {
			return err
		}
		defer lg.Close()
	}
	dlog := func(event string, kv ...any) {
		if lg != nil {
			lg.Log(ports.LogInfo, event, kv...)
		}
	}
	// obs is lg as a nullable interface (a typed-nil *Sink would pass
	// the adapters' nil checks and then explode).
	var obs ports.Logger
	if lg != nil {
		obs = lg
	}

	// -relay: this daemon offers to forward ciphertext between NATed
	// peers. A capability, not infrastructure: any reachable node can do
	// this, none is special, and no relay is baked into the binary.
	if *relayServe != "" {
		rs, err := relay.Serve(*relayServe, ident, relay.Config{}, obs)
		if err != nil {
			return err
		}
		defer rs.Close()
		fmt.Printf("relay: serving at %s@%s (content-blind forwarding, capped)\n", id, rs.Addr())
	}
	// -relay-via: parsed up front so a typo fails at start, not at the
	// moment we discover we're NATed and need it.
	var viaID ports.NodeID
	var viaAddr string
	if *relayVia != "" {
		ps, err := discovery.ParseList(*relayVia)
		if err != nil || len(ps) != 1 {
			return fmt.Errorf("-relay-via wants one RELAYID@HOST:PORT: %w", err)
		}
		viaID, viaAddr = ps[0].ID, ps[0].Addr
	}

	// Validator role: local chain replica, persisted and re-validated on
	// load; reputation judged from this daemon's own ledger observations.
	var attesterIDs []ports.NodeID
	var chainPath string
	ledger := credit.New(50_000, 0)
	nd0ledger := ledger // wired onto the node below
	if *validator {
		ch := chain.New(chain.Config{
			MinProposerRep: *minRep, MinAttesterRep: *minRep, Quorum: *quorum,
		}, ledger.Reputation)
		chainPath = filepath.Join(*storeDir, "chain.cbor")
		if n, err := chainstore.Replay(chainPath, ch); err != nil {
			fmt.Fprintln(os.Stderr, "chain replay:", err)
		} else if n > 0 {
			fmt.Printf("chain: restored %d block(s) from disk\n", n)
		}
		// Every fresh chain is born carrying the founding manifesto at
		// height 0 (declared, not agreed), and the daemon seeds the
		// genesis file into its own store so the whole swarm always hosts
		// it. Idempotent across restarts: genesis is deterministic and
		// chainstore already restored it if present.
		if ch.Len() == 0 {
			if gb, gh, _, gerr := genesis.Build(store); gerr == nil {
				if err := ch.AppendGenesis(gb); err == nil {
					fmt.Printf("genesis: %s\n", gh)
				}
			} else {
				fmt.Fprintln(os.Stderr, "genesis seed:", gerr)
			}
		}
		nd.EnableChain(ch, ident.Signer())
		nd.OnCommit(func(b chain.Block) {
			fmt.Printf("chain: committed block %d (%d entries, %d attestations)\n",
				b.Height, len(b.Entries), len(b.Atts))
			if err := chainstore.Save(chainPath, ch.Blocks(0)); err != nil {
				fmt.Fprintln(os.Stderr, "chain save:", err)
			}
		})
		for _, s := range strings.Split(*attesters, ",") {
			if strings.TrimSpace(s) == "" {
				continue
			}
			aid, err := ports.ParseHash(strings.TrimSpace(s))
			if err != nil {
				return fmt.Errorf("attester %q: %w", s, err)
			}
			attesterIDs = append(attesterIDs, aid)
		}
	}

	var reg ports.Registry
	switch {
	case *serveRegistry != "" && *validator:
		host := &chainhost.Host{Loop: loop, Node: nd,
			Attesters: attesterIDs, Broadcast: attesterIDs, Quorum: *quorum}
		bound, _, err := httpregistry.ServeTLS(*serveRegistry, ident, host)
		if err != nil {
			return err
		}
		reg = host
		fmt.Printf("registry: chain-backed, serving %s@https://%s (quorum %d)\n", id, bound, *quorum)
	case *serveRegistry != "":
		freg, err := fileregistry.Open(filepath.Join(*storeDir, "registry.jsonl"))
		if err != nil {
			return err
		}
		bound, _, err := httpregistry.ServeTLS(*serveRegistry, ident, freg)
		if err != nil {
			return err
		}
		reg = freg
		fmt.Printf("registry: serving %s@https://%s (persisted in %s)\n", id, bound, *storeDir)
	case *registryURL != "":
		reg, err = openRegistry(*registryURL)
		if err != nil {
			return err
		}
		fmt.Printf("registry: %s\n", *registryURL)
	}

	if *denylistPath != "" {
		f, err := os.Open(*denylistPath)
		if err != nil {
			return err
		}
		dl := denylist.New()
		if err := denylist.LoadInto(dl, f); err != nil {
			f.Close()
			return err
		}
		f.Close()
		nd.SetDenylist(dl)
		if purged := nd.EnforceDenylist(); purged > 0 {
			fmt.Printf("denylist: %d root(s) denied; purged %d held chunk(s)\n", dl.Len(), purged)
		} else {
			fmt.Printf("denylist: honoring %d denied root(s)\n", dl.Len())
		}
	}

	fmt.Printf("peer: %s@%s\n", id, tr.Addr())
	fmt.Printf("store: %s\n", *storeDir)

	if *uiAddr != "" {
		var capRep ports.CapacityReporter
		if rep, ok := store.(ports.CapacityReporter); ok {
			capRep = rep
		}
		ui := &uiServer{
			loop: loop, nd: nd, reg: reg, capRep: capRep,
			selfPeer:  fmt.Sprintf("%s@%s", id, tr.Addr()),
			validator: *validator, started: time.Now(),
			peerCount: func() int { return len(tr.Peers()) },
		}
		bound, err := ui.serve(*uiAddr)
		if err != nil {
			return err
		}
		fmt.Printf("ui: http://%s\n", bound)
	}

	// Discovery, in layers: explicit flag, DNS seed, and the persisted
	// address book from last run.
	peersPath := filepath.Join(*storeDir, "peers.json")
	var seeds []ports.NodeID
	addSeeds := func(peers []tcpnet.Peer, source string) {
		for _, p := range peers {
			if p.ID == id {
				continue
			}
			tr.AddPeer(p.ID, p.Addr)
			seeds = append(seeds, p.ID)
		}
		if len(peers) > 0 {
			fmt.Printf("discovery: %d peer(s) via %s\n", len(peers), source)
			dlog("discovery", "peers", len(peers), "source", source)
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
		} else {
			fmt.Fprintln(os.Stderr, "dns seed:", err)
		}
	}
	if ps, err := discovery.LoadFile(peersPath); err == nil {
		addSeeds(ps, "peers.json (warm restart)")
	}
	// Local-network discovery: announce on the LAN and fold any peer we
	// hear into the routing table. This is the zero-config rung — two nodes
	// in one house find each other with no flags at all.
	if *mdns {
		if adv, err := lan.AdvertiseAddr(tr.Addr()); err != nil {
			fmt.Fprintln(os.Stderr, "mdns: local discovery off —", err)
		} else {
			beacon := lan.New(tcpnet.Peer{ID: id, Addr: adv}, func(p tcpnet.Peer) {
				loop.Post(func() {
					tr.AddPeer(p.ID, p.Addr)
					nd.Bootstrap([]ports.NodeID{p.ID}, func() {})
					fmt.Printf("mdns: discovered %s@%s\n", p.ID, p.Addr)
					dlog("mdns discovered", "peer", p.ID, "addr", p.Addr)
				})
			})
			if err := beacon.Start(); err != nil {
				fmt.Fprintln(os.Stderr, "mdns:", err)
			} else {
				defer beacon.Close()
				fmt.Printf("mdns: announcing %s on the local network\n", adv)
			}
		}
	}
	// Persist the living address book so the next start needs no flags.
	go func() {
		for range time.Tick(30 * time.Second) {
			discovery.SaveFile(peersPath, tr.Peers())
		}
	}()
	// leanOnRelay registers with the -relay-via relay, switches our
	// advertised address to the relay form, and — the important part —
	// RE-bootstraps: the first bootstrap of a NATed node may have come
	// up empty (peers had no way to answer someone with no dialable
	// address), so the join is retried now that every envelope carries
	// an address the swarm can actually reach us at.
	leanOnRelay := func() {
		rc, err := relay.NewClient(ident, viaID, viaAddr, tr.RelayInbound, obs)
		if err != nil {
			fmt.Fprintln(os.Stderr, "relay-via:", err)
			return
		}
		go rc.Run(func(err error) {
			if err != nil {
				fmt.Fprintln(os.Stderr, "relay-via: registration failed:", err)
				return
			}
			loop.Post(func() {
				tr.SetAdvertise(rc.Addr())
				fmt.Printf("relay-via: registered — peers reach us at %s\n", rc.Addr())
				dlog("relay-via registered", "addr", rc.Addr())
				nd.Bootstrap(seeds, func() {
					fmt.Printf("re-bootstrapped through the relay (%d table entries)\n", nd.Table().Size())
					dlog("re-bootstrapped via relay", "table", nd.Table().Size())
					nd.AnnounceHeld(func(int) {})
				})
			})
		})
	}
	nd.SetLedger(nd0ledger)
	loop.Post(func() {
		nd.Bootstrap(seeds, func() {
			fmt.Printf("bootstrapped (%d table entries)\n", nd.Table().Size())
			dlog("bootstrapped", "table", nd.Table().Size())
			// Reachability (AutoNAT): ask a couple of known peers to dial us
			// back. A public node advertises its direct address; a NATed one
			// leans on the -relay-via relay, if given.
			if helpers := nd.Table().Closest(nd.ID(), 3); len(helpers) > 0 {
				nd.CheckReachability(helpers, func(reachable bool) {
					switch {
					case reachable:
						fmt.Println("reachability: public — peers can dial this node directly")
					case *relayVia != "":
						fmt.Println("reachability: no peer could dial back — NATed; leaning on the -relay-via relay")
						leanOnRelay()
					default:
						fmt.Println("reachability: no peer could dial back — this node looks NATed (give it -relay-via RELAYID@HOST:PORT to be reachable across networks)")
					}
				})
			} else if *relayVia != "" {
				// Nobody to ask (a lone node bootstrapping into an empty
				// swarm): assume the conservative answer and take the relay.
				fmt.Println("reachability: no peers to check with — assuming NATed; leaning on the -relay-via relay")
				leanOnRelay()
			}
			if *validator && len(attesterIDs) > 0 {
				nd.SyncChain(attesterIDs, func(added int, _ error) {
					if added > 0 {
						fmt.Printf("chain: synced %d block(s) from peers\n", added)
						chainstore.Save(chainPath, nd.Chain().Blocks(0))
					}
				})
			}
			nd.AnnounceHeld(func(count int) {
				if count > 0 {
					fmt.Printf("re-announced %d held chunks\n", count)
				}
				if reg != nil && *care != "" {
					for _, r := range strings.Split(*care, ",") {
						ch, err := link.ParseAnyCare(strings.TrimSpace(r))
						if err != nil {
							fmt.Fprintln(os.Stderr, "bad -care link:", err)
							continue
						}
						nd.Care(reg, ch)
						fmt.Printf("caretaking %s\n", ch.Root)
					}
				}
			})
		})
	})

	fmt.Println("serving; Ctrl-C to stop")
	loop.Run() // forever
	return nil
}

// openDebugLog wires -debug: everything the node and transport narrate
// lands in a grep-able debug.log next to the store, so a failure in the
// field leaves an artifact. The caller closes the sink.
func openDebugLog(storeDir string, tr *tcpnet.Transport, nd *node.Node) (*logfile.Sink, error) {
	logPath := filepath.Join(storeDir, "debug.log")
	lg, err := logfile.Open(logPath, ports.LogDebug)
	if err != nil {
		return nil, err
	}
	tr.SetLogger(lg)
	nd.SetLogger(lg)
	fmt.Printf("debug: logging to %s\n", logPath)
	return lg, nil
}

// openRegistry accepts either "http://host:port" (plain, trusted
// loopback) or "ID@https://host:port" (TLS pinned to the hosting
// daemon's identity).
func openRegistry(ref string) (ports.Registry, error) {
	if at := strings.Index(ref, "@"); at == 64 {
		hostID, err := ports.ParseHash(ref[:at])
		if err != nil {
			return nil, fmt.Errorf("registry ref %q: %w", ref, err)
		}
		return httpregistry.NewPinnedClient(ref[at+1:], hostID), nil
	}
	// A bare https:// URL can't be verified without the host's identity to
	// pin. This is the common first-run mistake, so name the fix.
	if strings.HasPrefix(ref, "https://") {
		return nil, fmt.Errorf("registry %q is HTTPS but has no pinned identity; a key-pinned registry needs the ID@https://host:port form the daemon prints on start — copy its 'registry:' line verbatim", ref)
	}
	return httpregistry.NewClient(ref), nil
}

// parseSize reads human sizes: 2G, 500M, 64K, or plain bytes.
func parseSize(s string) (int64, error) {
	s = strings.TrimSpace(strings.ToUpper(s))
	mult := int64(1)
	switch {
	case strings.HasSuffix(s, "T"):
		mult, s = 1<<40, strings.TrimSuffix(s, "T")
	case strings.HasSuffix(s, "G"):
		mult, s = 1<<30, strings.TrimSuffix(s, "G")
	case strings.HasSuffix(s, "M"):
		mult, s = 1<<20, strings.TrimSuffix(s, "M")
	case strings.HasSuffix(s, "K"):
		mult, s = 1<<10, strings.TrimSuffix(s, "K")
	}
	var n int64
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil || n <= 0 {
		return 0, fmt.Errorf("bad size %q (want e.g. 2G, 500M)", s)
	}
	return n * mult, nil
}

// ephemeral spins up a short-lived in-memory node joined to a swarm —
// the client side of daemon mode. Its identity is a throwaway keypair;
// clients don't accumulate reputation. Returned run posts fn onto the
// node's loop and waits for completion.
type ephemeral struct {
	nd   *node.Node
	loop *eventloop.Loop
	tr   *tcpnet.Transport
}

func joinSwarm(peers string) (*ephemeral, func(fn func(done func())) error, error) {
	ident, err := identity.Generate(rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	loop := eventloop.New()
	go loop.Run()
	tr, err := tcpnet.New(loop, ident, "127.0.0.1:0")
	if err != nil {
		return nil, nil, err
	}
	nd := node.New(ident.NodeID(), node.Config{
		K: 8, Alpha: 3,
		RequestTimeout: ports.Duration(2 * time.Second),
		Replication:    3,
	}, walltime.New(loop), tr, memstore.New())

	ps, err := discovery.ParseList(peers)
	if err != nil {
		return nil, nil, err
	}
	var seeds []ports.NodeID
	for _, p := range ps {
		tr.AddPeer(p.ID, p.Addr)
		seeds = append(seeds, p.ID)
	}
	run := func(fn func(done func())) error {
		ch := make(chan struct{})
		loop.Post(func() { fn(func() { close(ch) }) })
		select {
		case <-ch:
			return nil
		case <-time.After(5 * time.Minute):
			return fmt.Errorf("swarm operation timed out")
		}
	}
	e := &ephemeral{nd: nd, loop: loop, tr: tr}
	if err := run(func(done func()) { nd.Bootstrap(seeds, func() { done() }) }); err != nil {
		return nil, nil, err
	}
	return e, run, nil
}

func (e *ephemeral) close() {
	e.tr.Close()
	e.loop.Stop()
}
