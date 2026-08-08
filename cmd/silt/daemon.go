package main

import (
	"crypto/rand"
	"flag"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/nerolabs/silt/adapters/cachestore"
	"github.com/nerolabs/silt/adapters/capstore"
	"github.com/nerolabs/silt/adapters/chainhost"
	"github.com/nerolabs/silt/adapters/chainstore"
	"github.com/nerolabs/silt/adapters/discovery"
	"github.com/nerolabs/silt/adapters/diskissuer"
	"github.com/nerolabs/silt/adapters/diskplot"
	"github.com/nerolabs/silt/adapters/diskproofs"
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
	registryURL := fs.String("registry", "", "registry ref: ID@https://host:port (key-pinned — copy the daemon's 'registry:' line verbatim; a bare http:// or unkeyed https:// is refused)")
	serveRegistry := fs.String("serve-registry", "", "host the registry at this address (persisted in the store dir)")
	idSeed := fs.Int64("id-seed", 0, "derive the identity from a seed (default: persistent keyfile) — for scripted demos")
	care := fs.String("care", "", "comma-separated care links (siltcare:...) to repair — no decryption possible or needed")
	capacity := fs.String("capacity", "5G", "storage pledge, e.g. 2G, 500M (matches the client's default so the node contributes measurable, countable storage; \"\" = unlimited but doesn't count toward network storage)")
	freeload := fs.Bool("freeload", false, "role separation (#47): serve the registry/relay/routing role but REFUSE to store or serve content — for public-infrastructure operators who run a rendezvous registry without being conscripted into hosting arbitrary content. The node still carries DHT routing; it just holds and serves no chunks")
	registryOnly := fs.Bool("registry-only", false, "the LEANEST public-registry role (#47): serve a file-backed registry over HTTPS and construct NO storage node at all — no DHT, chunk store, chain, or caretaker. Unlike -freeload (a full routing node that refuses to host content), this builds nothing but the registry server, so a public-infrastructure operator runs a rendezvous registry at minimal cost. Needs -serve-registry <addr>")
	// Empty default is deliberate: no built-in seed domain (neutral infra,
	// community-run) — see the discovery package doc (#27 Part A).
	dnsSeed := fs.String("dns-seed", "", "domain whose TXT records list bootstrap peers")
	mdns := fs.Bool("mdns", true, "announce and discover peers on the local network (LAN multicast); needs a non-loopback -listen")
	denylistPath := fs.String("denylist", "", "operator takedown list: a file of denied root hashes to refuse to store/serve (you choose which lists to honor)")
	honorRevocations := fs.Bool("honor-chain-revocations", false, "SUBSCRIBE to the chain's on-chain takedowns (M0 F5): also deny roots a quorum has revoked on-chain. Default OFF — following the chain does not impose someone else's takedowns; honoring is a per-operator choice, proportional to who trusts you, never a global switch. The operator-local -denylist is always honored")
	revokeRoot := fs.String("revoke", "", "as a validator, propose an on-chain takedown of this root hash once standing is earned and the root is committed (M0 F5: quorum-gated, existence-checked; honored only by nodes that -honor-chain-revocations)")
	validator := fs.Bool("validator", false, "keep a chain replica and take part in consensus")
	uiAddr := fs.String("ui", "", "serve the web UI at this address (e.g. 127.0.0.1:8081)")
	attesters := fs.String("attesters", "", "comma-separated validator IDs to gather attestations from")
	anchorList := fs.String("anchors", "", "launch-window training wheels: comma-separated anchor validator IDs whose sign-off an immature-network commit also requires (empty = no training wheels)")
	anchorQuorum := fs.Int("anchor-quorum", 0, "anchor attestations an immature-network commit needs (0 = off)")
	matureValidators := fs.Int("mature-validators", 0, "required NAKAMOTO COEFFICIENT (M0 H4): the anchor requirement sheds only once this many bond-DISTINCT operators are needed to reach ⅓ of the bonded weight — cost-to-corrupt, not a head-count, so one operator with many keys can't trip the wheels off (0 = never require anchors)")
	operatorMargin := fs.Int("operator-margin", 1, "operator margin M (M0 C2 / D-C2): the maturity shed discounts the bond-distinct Nakamoto coefficient by M (⌊k̂/M⌋) — since on-chain data carries no operator label, one operator may split a stake across ~M keys, so a splitter must clear mature-validators×M distinct bonds to shed the wheels. LEFT UNSET it defaults to a conservative M>1 for an untrusted objective swarm (safe-by-default, like -min-bond-floor); an explicit 1 = no split margin (single-operator/trusted). M stays a heuristic — unverifiable on-chain (#182)")
	quorum := fs.Int("quorum", 3, "MINIMUM attestations (excluding the proposer) to commit a block — a floor; with -byzantine-quorum the effective requirement rises to the Byzantine threshold over the qualified set. Lower only for a trusted/one-box swarm")
	byzantineQuorum := fs.Bool("byzantine-quorum", false, "size the commit quorum at the Byzantine threshold (M0 H4): the support set becomes a supermajority n−f of the qualified bonded set, so two quorums always share an honest validator (safety as the set grows). LEFT UNSET it defaults ON for an untrusted objective validator; an explicit =false opts out (trusted swarm). Only ever RAISES the bar")
	objective := fs.Bool("objective", true, "DEFAULT-ON for an untrusted validator: consensus fork-choice by OBJECTIVE on-chain bond (F6), so eligibility, quorum, and fork-choice weight are a function of verifiable on-chain bond registrations — identical on every replica — and honest replicas can't diverge under a partition (the M0 consensus denial). Bootstrap a multi-validator quorum with -anchors (the launch set); validators register their real bonds live as they propose. Auto-off for a trusted swarm (-min-rep 0). Pass -objective=false to run the legacy subjective path, which does NOT hold the M0 denial under an adversarial partition")
	minBond := fs.String("min-bond", "1M", "objective mode: the minimum bonded size a validator must prove on-chain to qualify (its -bond must clear this)")
	minRep := fs.Int64("min-rep", 100, "reputation a proposer/attester must have EARNED (bonds+audits) to write — safe default; 0 = trusted deployment (self-commit, unsafe on an open network)")
	bondSize := fs.String("bond", "64M", "storage bond a validator seals to earn consensus standing, proven to peers over time (persisted to disk; a restart reloads it) — a bigger bond earns more standing")
	minBondFloor := fs.String("min-bond-floor", "0", "anti-release floor (M0 F1/F2): a bond smaller than this earns NO standing, because it could be released and re-plotted inside the challenge window. Set it ABOVE (challenge-window × plot-throughput): at ~270 MB/s and this daemon's ~2s window that is ~540 MiB, so a real open deployment sets e.g. 1G. 0 = off (safe only for a trusted/demo swarm)")
	bondAudit := fs.Duration("bond-audit", 60*time.Second, "how often a validator challenges its peers' bonds and refreshes its own standing")
	bondLabelK := fs.Int("bond-label-k", 64, "labeling-consistency opens per bond challenge (M0 Sybil G2): each recomputes one block's label from its DRSample parents, so a prover holding arbitrary/reused/wrong-size bytes (not a real plot for its identity+size) fails. Soundness error ≤ (1-ε)^k against an ε-short prover. A per-network knob — prover and verifier must MATCH (like -bond-vdf), so set it uniformly across the swarm. Lower it only to shrink on-chain proof size, at a soundness cost. 0 = default (64)")
	signedProviders := fs.Bool("signed-providers", true, "self-certifying DHT provider records (M0 H5): a node signs its 'I hold this' announcements with its identity key and re-verifies records served back on lookup, so a node holding the k-closest slots to a key cannot fabricate provider records for identities that never announced. Default ON; =false drops to the legacy unsigned path (trusted/demo swarm only)")
	signedProviderTTL := fs.Duration("signed-provider-ttl", 30*time.Minute, "freshness window stamped on signed provider records (M0 H5): a re-served record older than this is treated as expired, so an eclipsing node can't replay an ancient claim forever")
	dhtDomainCap := fs.Int("dht-domain-cap", 2, "failure-domain diversity cap for DHT eclipse resistance (M0 H5-B): at most this many peers sharing one -domain are kept per routing bucket, and provider records are announced to / resolved from a domain-spread set — so an adversary owning the NodeIDs closest to a key but sitting in one domain (a ~$4 /24 key-surround) can't suppress discovery. Only bites when peers set distinct -domain labels. 0 = off (no diversity constraint)")
	domain := fs.String("domain", "", "this node's failure-domain label (AS / rack / geo — e.g. \"as64500\" or \"us-east-1b\"). Two uses: DHT eclipse-resistance (H5-B, with -dht-domain-cap) AND, for a validator, it is COMMITTED in the bond so the C2 concentration metric counts ADDRESS-DIVERSE participants (A axis / D-C2) — a stake split across many keys in ONE domain cannot fake decentralization; shedding the launch anchors requires distinct domains, not just distinct keys. A WEAK signal (declared, transport-cross-checked, not proven); it prices concentration higher, it does not close the honest-whale residual. Empty = unset (independent).")
	bondTTL := fs.Uint64("bond-ttl", 0, "objective re-challenge cadence (M0 retest G4 / RT-2): objective standing LAPSES this many committed blocks after a validator's latest on-chain bond registration unless it renews with a fresh space-time proof — so a validator that registers once then releases its plot cannot keep voting. LEFT UNSET it defaults ON for an untrusted objective validator (derived cadence); an explicit 0 disables it (standing never expires; safe only for a trusted/demo swarm)")
	requireTokens := fs.Int("require-tokens", 0, "publisher privacy: require every published entry to carry a publish token blind-signed by this many validators, instead of a Publisher identity (0 = off; validators issue tokens)")
	allowPublisher := fs.Bool("allow-publisher", false, "permit entries that carry a durable Publisher identity (records a PERMANENT Publisher→root link on the append-only chain; off by default for privacy/M0 — only for explicitly trusted deployments)")
	blockPeers := fs.String("block-peers", "", "TEST-HARNESS / FIELD-DRILL: comma-separated peer IDs to PARTITION away from — this node drops all messages to/from them, simulating a severed link (#184 partition→heal). HEAL by restarting without the flag (the persisted chain reloads and reconciles). Empty = no partition; a real deployment never sets it")
	forgeBlock := fs.String("forge-block", "", "RED-TEAM / TEST-HARNESS ONLY: propose a block with a FORGED (corrupted) proposer signature to this peer ID, to prove an honest validator rejects it before attesting (#184 forged-block→reject). Never honest")
	lowbondPropose := fs.String("lowbond-propose", "", "RED-TEAM / TEST-HARNESS ONLY: as an under-bonded validator, propose a well-formed block to this peer ID, to prove an honest validator refuses a proposer without a qualifying bond (#184 low-bond→reject). Never honest")
	equivocate := fs.String("equivocate", "", "RED-TEAM / TEST-HARNESS ONLY: run this validator as a Byzantine EQUIVOCATOR. Given two peer validator IDs \"idX,idYZ\", it double-signs at height 1 — placing block X on idX and a heavier conflicting fork (Y,Z) on idYZ — so honest replicas that reconcile the fork catch the double-sign and slash this node (#184, proving the accountability property over the real wire). NEVER use on a real network; a correct node refuses to equivocate")
	wsCheckpoint := fs.String("ws-checkpoint", "", "weak-subjectivity checkpoint HEIGHT:HASH (M0 F-1): a recent trusted committed block this node REFUSES to reorg at or before, regardless of fork weight — the long-range-attack defense that makes the objective maturity latch safe for a fresh/long-offline node. Obtain it out-of-band (the daemon prints `checkpoint: HEIGHT:HASH` for its committed head; cross-check several independent nodes). It must be recent — within ~the bond-TTL window. Empty = genesis-trusting (safe only at launch, on a trusted swarm, or before the network matures)")
	debug := fs.Bool("debug", false, "shorthand for -log debug (the full firehose)")
	logLevel := fs.String("log", "", "write events at or above this level to <store>/debug.log (error|warn|info|debug); info narrates the normal path (placements, commits, repairs) to validate behavior in the field without the debug firehose")
	relayServe := fs.String("relay", "", "offer relay service at this address (e.g. 0.0.0.0:4002): content-blind ciphertext forwarding for NATed peers, capped; pointless unless this node is publicly reachable")
	relayVia := fs.String("relay-via", "", "RELAYID@HOST:PORT of a relay to lean on if this node turns out to be NATed — peers then reach us through it")
	advertise := fs.String("advertise", "", "publicly dialable HOST:PORT to stamp on outgoing messages — set this on a public box that listens on a wildcard address (a wildcard bind is never advertised on its own)")
	cacheSize := fs.String("cache", "", "in-RAM read cache for hot chunks, e.g. 512M (default off) — a cache hit skips the disk read and the per-read hash re-verify")
	carePublished := fs.Bool("care-published", true, "the daemon repairs content published through its own UI, so your own content stays alive as nodes churn (its manifest counts toward this node's pledge); =false to opt out")
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

	// -registry-only (#47): the leanest public-registry role — serve a file-backed
	// registry over HTTPS and construct NO storage node (no DHT, chunk store, chain, or
	// caretaker). This returns before the node, transport, and everything downstream is
	// built, so a rendezvous registry runs at minimal cost. Blocks until the process is
	// stopped.
	if *registryOnly {
		if *serveRegistry == "" {
			return fmt.Errorf("-registry-only needs -serve-registry <addr> (the address to serve the registry on)")
		}
		if err := os.MkdirAll(*storeDir, 0o755); err != nil {
			return err
		}
		freg, err := fileregistry.Open(filepath.Join(*storeDir, "registry.jsonl"))
		if err != nil {
			return err
		}
		bound, shutdown, err := httpregistry.ServeTLS(*serveRegistry, ident, freg)
		if err != nil {
			return err
		}
		defer shutdown()
		fmt.Printf("registry-only: %s serving a file-backed registry at https://%s (no storage node, #47)\n", id, bound)
		fmt.Println("serving; Ctrl-C to stop")
		select {} // block until the process is stopped; the registry server runs in its own goroutine
	}

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
	// -cache: an in-RAM read cache just above disk and below capacity
	// accounting, so hot chunks skip the disk read and the per-read hash
	// re-verify. Off by default; capstore stays outermost so it still
	// reports capacity.
	if *cacheSize != "" {
		budget, err := parseSize(*cacheSize)
		if err != nil {
			return err
		}
		if budget > 0 {
			store = cachestore.Open(store, budget)
			fmt.Printf("cache: %s hot-chunk read cache (hits skip disk + re-verify)\n", *cacheSize)
		}
	}
	if *capacity != "" {
		pledge, err := parseSize(*capacity)
		if err != nil {
			return err
		}
		capped, err := capstore.Open(store, pledge)
		if err != nil {
			return err
		}
		store = capped
		used, total := capped.Capacity()
		fmt.Printf("pledge: %d / %d bytes used\n", used, total)
	}
	// Base on DefaultConfig so new fields are inherited, not silently
	// dropped to their zero value (#71 — this is how demand-dispersion was
	// off in the daemon and the #65 fetch-retry shipped inert). Override
	// only what the daemon genuinely needs to differ on.
	cfg := node.DefaultConfig()
	cfg.RequestTimeout = ports.Duration(2 * time.Second) // patient vs the 500ms default (real WAN)
	cfg.BondAuditInterval = ports.Duration(*bondAudit)
	cfg.BondLabelSamples = *bondLabelK
	// Self-certifying provider records (M0 H5): ON by default so a node can't
	// fabricate DHT provider records for identities that never announced — records
	// are signed by the provider and re-verified on lookup. Records carry an expiry
	// (freshness) tied to the wall clock. -signed-providers=false drops to the
	// legacy unsigned path (a trusted/demo swarm only).
	cfg.RequireSignedProviders = *signedProviders
	cfg.ProviderRecordTTL = ports.Duration(*signedProviderTTL)
	cfg.DHTDomainCap = *dhtDomainCap // failure-domain diversity for eclipse resistance (H5-B)
	cfg.Domain = *domain             // this node's failure-domain label (H5-B DHT diversity + committed in the bond for the A-axis C2 metric)
	// The anti-release floor is SAFE-BY-DEFAULT on the objective/open path (M0
	// retest G4-residual). Shipping the mechanism but defaulting it OFF left a
	// doc-following open validator admitting a sub-floor, releasable bond to full
	// standing — "fixed but off by default" is not fixed. So it gets the same
	// treatment -objective already has: auto-on for an untrusted swarm (-min-rep
	// > 0), and an operator can still opt out EXPLICITLY with -min-bond-floor 0
	// (a trusted/demo swarm, where objective mode is off anyway).
	//
	// The derived value follows the flag's own arithmetic: a plot must be too big
	// to re-plot inside one challenge window, else it can be released and
	// recomputed just-in-time. At the measured ~270 MB/s plot throughput
	// (bond.BenchmarkSeal) and this daemon's ~2s window that is ~540 MiB, so the
	// default carries ~2x margin.
	floorSet, ttlSet, byzSet, marginSet := false, false, false, false
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "min-bond-floor":
			floorSet = true
		case "bond-ttl":
			ttlSet = true
		case "byzantine-quorum":
			byzSet = true
		case "operator-margin":
			marginSet = true
		}
	})
	explicitFloor, ferr := parseSize(*minBondFloor)
	if ferr != nil {
		explicitFloor = 0
	}
	// objectivePath mirrors the decision made later for the chain config (see
	// useObjective): objective fork-choice is the default for an untrusted
	// VALIDATOR, auto-off when trusted. A non-validator claims no consensus
	// standing, so the floor never applies to it.
	objectivePath := *validator && *objective && *minRep > 0
	effFloor, defaulted := effectiveBondFloor(floorSet, explicitFloor, objectivePath)
	if defaulted {
		fmt.Printf("bond: anti-release floor defaulted to %d MiB for this untrusted (objective) swarm — a smaller plot could be released and re-plotted inside the challenge window. Override with -min-bond-floor (0 disables; safe only for a trusted/demo swarm).\n", effFloor>>20)
	}
	// The bond TTL gets the same safe-by-default treatment as the floor (H2 / RT-2):
	// left OFF it admitted release-and-coast — a validator registers a bond once,
	// releases the plot, and keeps voting forever off a single one-time proof. On
	// the objective path it now decays un-renewed standing by default; the paired
	// non-proposer renewal path (node.SubmitBondRenewal, driven by the chain-sync
	// sweep) lets an honest attest-only validator renew without proposing, so the
	// default costs no liveness.
	effTTL, ttlDefaulted := effectiveBondTTL(ttlSet, *bondTTL, objectivePath)
	if ttlDefaulted {
		fmt.Printf("bond: objective re-challenge TTL defaulted to %d blocks for this untrusted (objective) swarm — standing lapses this many blocks after a validator's latest bond proof unless it renews, so a released plot can't keep voting. Override with -bond-ttl (0 disables; safe only for a trusted/demo swarm).\n", effTTL)
	}
	// Byzantine quorum sizing gets the same safe-by-default treatment (H4): a fixed
	// quorum loses quorum-intersection safety as the validator set grows, so an
	// untrusted objective validator sizes the commit quorum at the Byzantine
	// threshold unless the operator opts out. It only ever raises the bar.
	effByz, byzDefaulted := effectiveByzantineQuorum(byzSet, *byzantineQuorum, objectivePath)
	if byzDefaulted {
		fmt.Println("consensus: Byzantine quorum sizing defaulted ON for this untrusted (objective) swarm — a commit needs a supermajority of the qualified bonded set so two quorums always share an honest validator. Override with -byzantine-quorum=false (safe only for a trusted swarm).")
	}
	// The C2 operator margin M gets the same safe-by-default treatment (D-C2 / red-team
	// blind-2026-08-08): shipping the split-defense mechanism but defaulting M=1 left an
	// untrusted objective swarm with ZERO margin against one operator splitting real stake
	// across NodeIDs to fake decentralization — the Invariant-B footgun ("safe config is
	// the default"). It only ever RAISES the bar to shed the training wheels (a splitter
	// must clear mature-validators×M distinct bonds), so an auto-armed M>1 never weakens an
	// existing config. M stays an honest heuristic (on-chain data carries no operator label,
	// #182); this defaults it to a conservative value, tunable per deployment.
	effMargin, marginDefaulted := effectiveOperatorMargin(marginSet, *operatorMargin, objectivePath)
	if marginDefaulted {
		fmt.Printf("consensus: operator-margin defaulted to %d for this untrusted (objective) swarm — the C2 maturity shed discounts the bond-distinct Nakamoto coefficient by M, so one operator splitting real stake across ~M NodeIDs cannot fake the decentralization that sheds the launch anchors. Override with -operator-margin (1 = no split margin; safe only for a trusted/single-operator swarm).\n", effMargin)
	}
	if effFloor > 0 {
		cfg.MinBondBytes = effFloor
		if bsz, _ := parseSize(*bondSize); bsz < effFloor {
			// Fail closed rather than let the operator run a validator that
			// silently earns nothing — mirrors the -bond/-min-bond check below.
			return fmt.Errorf("bond: -bond (%s) is below the anti-release floor (%d MiB), so this validator would earn NO standing: raise -bond, or pass -min-bond-floor 0 to accept sub-floor bonds (trusted/demo swarms only)", *bondSize, effFloor>>20)
		}
	}
	clk := walltime.New(loop)
	nd := node.New(id, cfg, clk, tr, store)
	nd.SetSigner(ident.Signer()) // sign self-certifying provider records (H5), not just chain blocks
	if *blockPeers != "" {
		var blocked []ports.NodeID
		for _, s := range strings.Split(*blockPeers, ",") {
			if s = strings.TrimSpace(s); s != "" {
				bid, berr := ports.ParseHash(s)
				if berr != nil {
					return fmt.Errorf("-block-peers %q: %w", s, berr)
				}
				blocked = append(blocked, bid)
			}
		}
		nd.SetBlockedPeers(blocked)
		fmt.Printf("⚠ PARTITION: -block-peers set — dropping all traffic to/from %d peer(s) (test-harness / field-drill, never a real deployment)\n", len(blocked))
	}
	if *freeload {
		nd.SetFreeload(true)
		fmt.Println("freeload: ON — this node refuses to store or serve content (registry/relay/routing only, #47)")
	}

	// -log/-debug: dlog adds the daemon's own milestones (discovery,
	// bootstrap) to the same artifact the node and transport narrate to.
	level, logOn, err := resolveLogLevel(*logLevel, *debug, *validator)
	if err != nil {
		return err
	}
	var lg *logfile.Sink
	if logOn {
		if lg, err = openLog(*storeDir, level, tr, nd); err != nil {
			return err
		}
		defer lg.Close()
	}
	dlog := func(event string, kv ...any) {
		if lg != nil {
			lg.Log(ports.LogInfo, event, kv...)
		}
	}
	// #69: persist each hosted chunk's storage proof so a restart re-announces
	// coded shards under the right column key (AnnounceHeld, below, reads the
	// reloaded proofs) — otherwise a disk full of content is invisible until
	// re-hosted. Loading now, before bootstrap/announce.
	if pf, perr := diskproofs.Open(filepath.Join(*storeDir, "proofs")); perr != nil {
		return perr
	} else {
		nd.SetProofStore(pf)
		nd.LoadProofs()
	}
	// #93: persist the bond plot so a restart reloads (and re-verifies) it
	// instead of re-plotting the deliberately-expensive dataset. Attach before
	// EnableBond, below, which loads-or-plots through it.
	if pl, perr := diskplot.Open(filepath.Join(*storeDir, "plot")); perr != nil {
		return perr
	} else {
		nd.SetPlotStore(pl)
	}
	// obs is lg as a nullable interface (a typed-nil *Sink would pass
	// the adapters' nil checks and then explode).
	var obs ports.Logger
	if lg != nil {
		obs = lg
	}
	// A task that panics on the node's thread (e.g. handling a malformed
	// frame that reaches a decoder) fails that task, not the daemon — and
	// says so, loudly. Any such panic is a top-severity bug until fixed.
	loop.OnPanic = func(r any) {
		ports.LogIf(obs, ports.LogError, "recovered panic on node thread", "panic", r)
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
		// Gossip the capability on every envelope — but only in a form
		// peers can actually dial: a wildcard-bound relay borrows the
		// -advertise host, and with neither there is nothing worth
		// spreading (the swarm can't be sent "0.0.0.0:4002").
		if svc := dialableRelayAddr(rs.Addr(), *advertise); svc != "" {
			tr.SetRelayService(svc)
			fmt.Printf("relay: gossiping the service — NATed peers can discover %s@%s without -relay-via\n", id, svc)
		} else {
			fmt.Println("relay: wildcard bind and no -advertise — service not gossiped (peers must be told -relay-via by hand)")
		}
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
	ledger := credit.New(50_000, 500_000) // starter grant so a fresh publisher can pay token fees
	nd0ledger := ledger                   // wired onto the node below
	if *validator {
		anchorSet := map[ports.NodeID]bool{}
		for _, s := range strings.Split(*anchorList, ",") {
			if strings.TrimSpace(s) == "" {
				continue
			}
			aid, err := ports.ParseHash(strings.TrimSpace(s))
			if err != nil {
				return fmt.Errorf("anchor %q: %w", s, err)
			}
			anchorSet[aid] = true
		}
		if len(anchorSet) > 0 && *anchorQuorum > 0 {
			fmt.Printf("training wheels: %d anchor(s), %d required, shed at %d independent validators\n",
				len(anchorSet), *anchorQuorum, *matureValidators)
		}
		// Objective fork-choice is the DEFAULT for an untrusted validator (the M0
		// consensus path). A trusted deployment (-min-rep 0, self-commit) does not
		// need it, so it auto-disables there rather than forcing anchor config on a
		// single trusted box.
		useObjective := *objective && *minRep > 0
		var minBondBytes int64
		if useObjective {
			mb, perr := parseSize(*minBond)
			if perr != nil || mb <= 0 {
				return fmt.Errorf("objective consensus needs a positive -min-bond: %q", *minBond)
			}
			minBondBytes = mb
			if len(anchorSet) == 0 || *matureValidators <= 0 {
				fmt.Println("consensus: WARNING — objective fork-choice (the default M0 path) needs -anchors (the launch validator set) and -mature-validators>0 to bootstrap a MULTI-validator quorum; without them such a swarm will not commit. Pass them, or -min-rep 0 for a trusted swarm, or -objective=false for the legacy (non-M0) path.")
			}
		}
		// The objective anti-release floor and re-challenge cadence (retest G4)
		// ride the same knobs as the node-side floor: a sub-floor bond earns no
		// on-chain standing, and standing lapses without a fresh proof within the
		// TTL. cfg.MinBondBytes is 0 unless -min-bond-floor was set.
		var wsCP chain.WSCheckpoint
		if *wsCheckpoint != "" {
			parts := strings.SplitN(*wsCheckpoint, ":", 2)
			if len(parts) != 2 {
				return fmt.Errorf("-ws-checkpoint must be HEIGHT:HASH, got %q", *wsCheckpoint)
			}
			h, herr := strconv.ParseUint(parts[0], 10, 64)
			if herr != nil {
				return fmt.Errorf("-ws-checkpoint height %q: %w", parts[0], herr)
			}
			hash, perr := ports.ParseHash(parts[1])
			if perr != nil {
				return fmt.Errorf("-ws-checkpoint hash %q: %w", parts[1], perr)
			}
			wsCP = chain.WSCheckpoint{Height: h, Hash: hash}
			fmt.Printf("chain: weak-subjectivity checkpoint pinned at %d:%s — a reorg at or before it is refused (F-1)\n", h, hash)
		}
		ch := chain.New(chain.Config{
			MinProposerRep: *minRep, MinAttesterRep: *minRep, Quorum: *quorum,
			ByzantineQuorum: effByz,
			Anchors:         anchorSet, AnchorQuorum: *anchorQuorum, MatureValidators: *matureValidators,
			OperatorMargin: effMargin,
			AllowPublisher: *allowPublisher, MinBond: minBondBytes,
			MinBondBytes: cfg.MinBondBytes, BondTTLBlocks: effTTL,
			WSCheckpoint: wsCP,
		}, ledger.Reputation)
		if *allowPublisher {
			fmt.Println("publisher: durable Publisher entries PERMITTED — publishes may record permanent linkage (trusted deployment)")
		}
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
		if *honorRevocations {
			nd.SetHonorChainRevocations(true) // per-operator opt-in (F5); default OFF
			fmt.Println("takedowns: honoring on-chain revocations (per-operator subscription, F5)")
		}
		if sz, perr := parseSize(*bondSize); perr == nil && sz > 0 {
			if nd.EnableBond(ident.Signer(), sz) {
				// Reloaded the existing plot — a restart reuses it, no re-plot
				// (#93). Say so; logging "sealed" would falsely suggest the
				// expensive one-time plotting ran again (acceptance F7).
				fmt.Printf("bond: reloaded the %s storage bond for consensus standing (no re-plot)\n", *bondSize)
			} else {
				fmt.Printf("bond: sealed a %s storage bond for consensus standing\n", *bondSize)
			}
		}
		// Objective fork-choice (F6): wire the on-chain-bond verifier so
		// registrations are re-checked against the real space-time primitive, and
		// (via proposeBlock) this validator registers its own bond live as it
		// proposes. Must follow EnableBond so a bond exists to register.
		if useObjective {
			if bsz, _ := parseSize(*bondSize); bsz < minBondBytes {
				return fmt.Errorf("objective consensus requires -bond (%s) to clear -min-bond (%s)", *bondSize, *minBond)
			}
			nd.EnableObjectiveChain()
			fmt.Printf("consensus: OBJECTIVE fork-choice (default; on-chain bond ≥ %s); %d anchor(s) bootstrap the launch set, shed at maturity\n",
				*minBond, len(anchorSet))
		} else if *minRep == 0 {
			fmt.Println("consensus: legacy self-commit (trusted deployment, -min-rep 0) — objective fork-choice off")
		} else {
			fmt.Println("consensus: legacy subjective fork-choice (-objective=false) — does NOT hold the M0 denial under an adversarial partition")
		}
		// Publisher privacy (T3): this validator issues blind-signed publish
		// tokens, and (when -require-tokens) the chain accepts only entries that
		// carry one — no Publisher identity on-chain. The issuer key PERSISTS
		// (#93 / §3d): a restart reuses it, so outstanding tokens stay verifiable
		// and peers' cached issuer keys don't go stale.
		if is, ierr := diskissuer.Open(filepath.Join(*storeDir, "issuer")); ierr != nil {
			return fmt.Errorf("token issuer store: %w", ierr)
		} else if issuerKey, kerr := is.LoadOrCreate(rand.Reader); kerr == nil {
			nd.EnableTokenIssuer(issuerKey)
			if *requireTokens > 0 {
				ch.RequireTokens(*requireTokens, nd.IssuerKeyOf)
				fmt.Printf("publish tokens: required (%d validator signatures), issuing\n", *requireTokens)
			}
		} else {
			return fmt.Errorf("token issuer key: %w", kerr)
		}
		nd.OnSlash(func(culprit ports.NodeID, height uint64) {
			fmt.Printf("chain: slashed equivocator %s (double-signed at height %d)\n", culprit, height)
		})
		nd.OnReorg(func(dropped int, newHeight uint64) {
			fmt.Printf("chain: reorged onto a heavier fork (dropped %d block(s), new head height %d)\n", dropped, newHeight)
		})
		nd.OnCommit(func(b chain.Block) {
			fmt.Printf("chain: committed block %d (%d entries, %d attestations)\n",
				b.Height, len(b.Entries), len(b.Atts))
			// C2 — no-quiet-capture concentration, from the COMMITTED bond ledger
			// (not gossip). Operators is the coefficient discounted by the operator
			// margin M; the training wheels are shed only once it clears the maturity bar.
			if ch.Objective() {
				m := ch.C2Metric()
				wheels := "engaged (young network — anchor quorum still required)"
				if ch.EverMature() {
					// One-way latch (F-1): once shed, the anchors never re-arm.
					wheels = "shed permanently (network matured — F-1 one-way latch)"
					if !ch.Mature() {
						wheels = "shed permanently (matured; live decentralization has since dropped — real-bond super-quorum in force, anchors NOT re-armed)"
					}
				}
				fmt.Printf("  C2: nakamoto %d bonds → %d operators (margin ×%d) | cost-to-corrupt %d MiB of %d MiB bonded across %d | concentration HHI %.2f Gini %.2f top %.0f%% | wheels %s\n",
					m.NakamotoBonds, m.NakamotoOperators, m.Margin,
					m.CostToCorruptBytes>>20, m.TotalBondedBytes>>20, m.Participants,
					m.HHI, m.Gini, m.TopShare*100, wheels)
				// Concentration alarm (D-C2 / F-1 follow-up): the honest whale C2 cannot
				// close on-chain, made LOUD out-of-band. A single bond at/above the ⅓
				// Byzantine capture fraction is one step from being able to stall or
				// (with more) capture consensus — a social/operational trigger, not an
				// on-chain enforcement (impossible per Kwon).
				if m.TopShare >= 1.0/3 {
					fmt.Printf("  ⚠ CONCENTRATION ALARM: one bond holds %.0f%% of bonded weight (≥ the ⅓ capture fraction) — real standing is concentrating; this is the honest-whale residual C2 measures but cannot close on-chain. Act out-of-band.\n", m.TopShare*100)
				}
				// Export the committed head as a copy-pasteable weak-subjectivity
				// checkpoint (F-1): a fresh/long-offline node pins one via -ws-checkpoint
				// to refuse a long-range reorg. Publish it / cross-check across nodes.
				fmt.Printf("  checkpoint: %d:%s\n", b.Height, b.Hash())
			}
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

	// A validator running below the safe thresholds self-commits the
	// registry (a lone/fresh node can rubber-stamp). Legitimate for a
	// one-box or fully-trusted swarm, dangerous on an open network — so it
	// is an explicit, LOUD choice, never a silent default.
	if *validator && (*quorum < 1 || *minRep <= 0) {
		fmt.Printf("⚠ trusted-deployment mode (quorum %d, min-rep %d): this validator self-commits the registry — safe only for a one-box or fully-trusted swarm, NOT an open network\n", *quorum, *minRep)
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
		token, err := loadOrCreateUIToken(*storeDir)
		if err != nil {
			return err
		}
		ui := &uiServer{
			loop: loop, nd: nd, reg: reg, capRep: capRep,
			selfPeer:  fmt.Sprintf("%s@%s", id, tr.Addr()),
			validator: *validator, started: time.Now(),
			peerCount:     func() int { return tr.PeerCount() },
			carePublished: *carePublished,
			token:         token,
		}
		bound, err := ui.serve(*uiAddr)
		if err != nil {
			return err
		}
		// The token rides the URL query so the operator's browser is
		// authorized in one click; state-changing calls need it, reads don't.
		fmt.Printf("ui: http://%s/?token=%s\n", bound, token)
	}

	// Discovery, in layers: explicit flag, DNS seed, and the persisted
	// address book from last run.
	peersPath := filepath.Join(*storeDir, "peers.json")
	var seeds []ports.NodeID
	seeded := make(map[ports.NodeID]bool)
	addSeeds := func(peers []tcpnet.Peer, source string) {
		for _, p := range peers {
			if p.ID == id {
				continue
			}
			// A peer can appear once per address form (direct + relay);
			// both feed the book, the ID seeds the bootstrap once.
			tr.AddPeer(p.ID, p.Addr)
			if !seeded[p.ID] {
				seeded[p.ID] = true
				seeds = append(seeds, p.ID)
			}
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
	// Persist the living address book so the next start needs no flags —
	// but only peers we've actually reached, not every address ever
	// observed. Otherwise a warm restart reloads a graveyard of dead
	// ephemeral publisher identities and drowns lookups in timeouts (#43).
	// The reachable set lives on the (lock-free) node loop, so snapshot it
	// there and do the disk write off-loop.
	go func() {
		for range time.Tick(30 * time.Second) {
			done := make(chan []tcpnet.Peer, 1)
			loop.Post(func() {
				reachable := nd.ReachablePeers()
				all := tr.Peers()
				live := all[:0]
				for _, p := range all {
					if reachable[p.ID] {
						live = append(live, p)
					}
				}
				done <- live
			})
			discovery.SaveFile(peersPath, <-done)
		}
	}()
	// leanOnRelay registers with a relay (configured via -relay-via or
	// discovered through gossip), switches our advertised address to the
	// relay form, and — the important part — RE-bootstraps: the first
	// bootstrap of a NATed node may have come up empty (peers had no way
	// to answer someone with no dialable address), so the join is
	// retried now that every envelope carries an address the swarm can
	// actually reach us at.
	leanOnRelay := func(viaID ports.NodeID, viaAddr string) {
		rc, err := relay.NewClient(ident, viaID, viaAddr, tr.RelayInbound, obs)
		if err != nil {
			fmt.Fprintln(os.Stderr, "relay-via:", err)
			return
		}
		// #27: let the transport upgrade a relay path to a direct one. The
		// relay coordinates (RequestPunch); when it signals us, punch the peer
		// from our registration port (HolePunch).
		rc.SetOnPunch(func(peer ports.NodeID, peerAddr string, localPort int) {
			tr.HolePunch(peer, peerAddr, localPort)
		})
		tr.SetRequestPunch(rc.RequestPunch)
		go rc.Run(func(err error) {
			if err != nil {
				fmt.Fprintln(os.Stderr, "relay-via: registration failed:", err)
				return
			}
			loop.Post(func() {
				tr.SetAdvertise(rc.Addr())
				fmt.Printf("relay-via: registered — peers reach us at %s\n", rc.Addr())
				dlog("relay-via registered", "addr", rc.Addr())
				if seen := rc.Observed(); seen != "" { // STUN-style, for hole-punching (#27)
					nd.SetObservedAddr(seen)
					fmt.Printf("relay-via: this node's public endpoint looks like %s (observed by the relay)\n", seen)
					dlog("observed public endpoint", "addr", seen)
				}
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
						leanOnRelay(viaID, viaAddr)
					default:
						// No relay configured — but the swarm gossips
						// relay capability, so one may already be (or soon
						// become) known. Adopt the first that shows up.
						fmt.Println("reachability: no peer could dial back — this node looks NATed; watching the swarm for a gossiped relay (-relay-via RELAYID@HOST:PORT skips the wait)")
						dlog("natted, watching for gossiped relay")
						// First cut: adopt the lowest-ID gossiped relay and
						// commit to it (leanOnRelay then reconnects it with
						// backoff for the node's lifetime, exactly as an
						// explicit -relay-via would). Choosing among several
						// relays and failing over when the chosen one won't
						// register is a documented follow-up (see BACKLOG).
						go func() {
							for {
								if rs := tr.KnownRelays(); len(rs) > 0 {
									r := rs[0]
									fmt.Printf("relay: discovered %s@%s via gossip — leaning on it\n", r.ID, r.Addr)
									dlog("gossiped relay adopted", "relay", r.ID, "addr", r.Addr)
									leanOnRelay(r.ID, r.Addr)
									return
								}
								time.Sleep(5 * time.Second)
							}
						}()
					}
				})
			} else if *relayVia != "" {
				// Nobody to ask (a lone node bootstrapping into an empty
				// swarm): assume the conservative answer and take the relay.
				fmt.Println("reachability: no peers to check with — assuming NATed; leaning on the -relay-via relay")
				leanOnRelay(viaID, viaAddr)
			}
			// Start challenging peers' storage bonds (and refreshing our own
			// standing): consensus writes are gated on earned, held storage.
			// This comes BEFORE chain sync on purpose — a restarted node re-earns
			// its view of peer reputation here, and SyncChain needs that view to
			// judge which fork carries real standing (F1).
			if *validator {
				nd.StartBondAudit()
				// Fetch the other validators' token-issuer keys so we can verify
				// the publish tokens they blind-signed (chain token check).
				for _, aid := range attesterIDs {
					nd.FetchIssuerKey(aid, func(error) {})
				}
				// Catch up on any blocks committed while we were down — and keep
				// retrying, because peer standing (above) is re-earned live and
				// isn't ready the instant we boot. NOT gated on -attesters: a node
				// restarted with only -bootstrap still discovers validators via
				// gossip and rejoins (F1). Persist each catch-up.
				nd.StartChainSync(attesterIDs, func(added int) {
					fmt.Printf("chain: caught up %d block(s) from peers\n", added)
					if err := chainstore.Save(chainPath, nd.Chain().Blocks(0)); err != nil {
						fmt.Fprintln(os.Stderr, "chain save:", err)
					}
				})
				// -forge-block / -lowbond-propose: RED-TEAM / TEST HARNESS — send ONE crafted
				// proposal to a peer and report whether the honest validator refused it, proving
				// the two ValidateProposal defences (#184 forged-block→reject, low-bond→reject)
				// over the real wire. Retry only until the target is reachable. NEVER honest.
				badPropose := func(targetStr string, forge bool, label string) {
					if targetStr == "" {
						return
					}
					tid, terr := ports.ParseHash(strings.TrimSpace(targetStr))
					if terr != nil {
						fmt.Fprintf(os.Stderr, "-%s: %v\n", label, terr)
						return
					}
					fmt.Printf("⚠ ADVERSARY: -%s set — sending a bad proposal to prove honest rejection (red-team harness, never honest)\n", label)
					var try func()
					try = func() {
						nd.ProposeBadBlock(tid, forge, func(refused bool, err error) {
							if err != nil {
								clk.AfterFunc(1*ports.Second, try) // target not reachable yet; retry
								return
							}
							if refused {
								fmt.Printf("adversary: %s proposal correctly REJECTED by %s\n", label, tid)
							} else {
								fmt.Printf("adversary: %s proposal UNEXPECTEDLY ACCEPTED by %s (DEFECT)\n", label, tid)
							}
						})
					}
					clk.AfterFunc(2*ports.Second, try)
				}
				badPropose(*forgeBlock, true, "forge-block")
				badPropose(*lowbondPropose, false, "lowbond-propose")
				// -equivocate: RED-TEAM / TEST HARNESS — drive a deliberate double-sign so
				// honest replicas catch and slash it over the real wire (#184). Retry on the
				// loop-safe clock until this node has earned standing with both peers (early
				// attempts are refused). NEVER honest; a correct node refuses to equivocate.
				if *equivocate != "" {
					parts := strings.Split(*equivocate, ",")
					idX, e1 := ports.ParseHash(strings.TrimSpace(parts[0]))
					var idYZ ports.NodeID
					var e2 error
					if len(parts) == 2 {
						idYZ, e2 = ports.ParseHash(strings.TrimSpace(parts[1]))
					} else {
						e2 = fmt.Errorf("need exactly two peer IDs \"idX,idYZ\"")
					}
					if e1 != nil || e2 != nil {
						fmt.Fprintln(os.Stderr, "-equivocate: bad peer ids:", e1, e2)
					} else {
						fmt.Println("⚠ ADVERSARY: -equivocate set — this validator will DELIBERATELY double-sign (red-team harness, never honest)")
						var tryEquiv func()
						tryEquiv = func() {
							nd.Equivocate(idX, idYZ, func(err error) {
								if err != nil {
									clk.AfterFunc(1*ports.Second, tryEquiv) // not qualified with peers yet; retry
									return
								}
								fmt.Println("adversary: equivocation complete (double-signed height 1)")
							})
						}
						clk.AfterFunc(2*ports.Second, tryEquiv)
					}
				}
				// -revoke: propose an on-chain takedown of the named root once it is
				// committed and we have earned standing to gather a quorum (F5). Poll
				// on the loop-safe clock; existence + quorum are enforced by the chain.
				if *revokeRoot != "" {
					root, rerr := ports.ParseHash(strings.TrimSpace(*revokeRoot))
					if rerr != nil {
						fmt.Fprintln(os.Stderr, "-revoke:", rerr)
					} else {
						var tryRevoke func()
						tryRevoke = func() {
							if _, ok := nd.Chain().LookupRoot(root); !ok {
								clk.AfterFunc(2*ports.Second, tryRevoke) // root not committed yet
								return
							}
							nd.ProposeRevocation([]ports.Hash{root}, attesterIDs, attesterIDs, *quorum, func(err error) {
								if err != nil {
									clk.AfterFunc(2*ports.Second, tryRevoke) // no quorum yet; retry
									return
								}
								fmt.Printf("takedown: proposed on-chain revocation of %s\n", root)
								if serr := chainstore.Save(chainPath, nd.Chain().Blocks(0)); serr != nil {
									fmt.Fprintln(os.Stderr, "chain save:", serr)
								}
							})
						}
						clk.AfterFunc(2*ports.Second, tryRevoke)
					}
				}
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

// resolveLogLevel turns the -log/-debug flags into (level, on): -log
// wins when set, -debug is shorthand for the debug firehose. A VALIDATOR
// with neither flag defaults to info — the M0 trust plane (standing accrual,
// commits, catch-up, caretaker sweeps) is exactly what an operator must be
// able to see, so it narrates the normal path by default rather than staying
// silent until someone knows to ask (acceptance F7). A non-validator with
// neither flag keeps logging off (LogError is a harmless placeholder the
// caller ignores when on is false).
func resolveLogLevel(name string, debug, validatorDefault bool) (ports.LogLevel, bool, error) {
	if name != "" {
		lvl, err := ports.ParseLevel(name)
		if err != nil {
			return 0, false, err
		}
		return lvl, true, nil
	}
	if debug {
		return ports.LogDebug, true, nil
	}
	if validatorDefault {
		return ports.LogInfo, true, nil
	}
	return ports.LogError, false, nil
}

// openLog wires the file sink: everything the node and transport narrate
// at or above level lands in a grep-able debug.log next to the store, so
// a failure in the field leaves an artifact. The caller closes the sink.
func openLog(storeDir string, level ports.LogLevel, tr *tcpnet.Transport, nd *node.Node) (*logfile.Sink, error) {
	logPath := filepath.Join(storeDir, "debug.log")
	lg, err := logfile.Open(logPath, level)
	if err != nil {
		return nil, err
	}
	tr.SetLogger(lg)
	nd.SetLogger(lg)
	fmt.Printf("log: %s and above → %s\n", level, logPath)
	return lg, nil
}

// dialableRelayAddr turns the relay listener's bound address into one
// worth gossiping. A concrete bind speaks for itself; a wildcard bind
// ("0.0.0.0:4002") borrows the host the daemon already advertises for
// swarm traffic (-advertise), keeping the relay's own port. Neither →
// "" (nothing gossiped).
func dialableRelayAddr(bound, advertise string) string {
	host, port, err := net.SplitHostPort(bound)
	if err != nil {
		return ""
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsUnspecified() {
		return bound
	}
	if advertise == "" {
		return ""
	}
	advHost, _, err := net.SplitHostPort(advertise)
	if err != nil {
		return ""
	}
	return net.JoinHostPort(advHost, port)
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
	// Same DefaultConfig base as the daemon (#71). This is the actual swarm
	// add/get fetcher, so it inherits the #65 retry; it stages and leaves,
	// so the repair/demand fields are harmless (it never caretakes).
	cfg := node.DefaultConfig()
	cfg.RequestTimeout = ports.Duration(2 * time.Second)
	cfg.RequireSignedProviders = true // reject forged/unsigned provider records on fetch (H5)
	cfg.ProviderRecordTTL = ports.Duration(30 * time.Minute)
	cfg.DHTDomainCap = 2 // resolve providers from a domain-spread set — eclipse resistance (H5-B)
	nd := node.New(ident.NodeID(), cfg, walltime.New(loop), tr, memstore.New())
	nd.SetSigner(ident.Signer()) // sign self-certifying provider records (H5)
	nd.SetEphemeral(true)        // a publish/fetch client that keeps nothing — peers must not route to it (#43)

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

// DerivedBondFloor is the anti-release floor an untrusted (objective) validator
// gets when the operator sets none. A plot must be too big to re-plot inside one
// challenge window, else it can be released and recomputed just-in-time: at the
// measured ~270 MB/s plot throughput (bond.BenchmarkSeal) and this daemon's ~2s
// window that threshold is ~540 MiB, so this default carries ~2x margin.
const DerivedBondFloor = int64(1) << 30 // 1 GiB

// effectiveBondFloor decides the anti-release floor (M0 retest G4-residual).
// Shipping the floor mechanism but defaulting it OFF left a doc-following open
// validator admitting sub-floor, releasable bonds — "fixed but off by default"
// is not fixed. So the floor is SAFE-BY-DEFAULT on the objective/open path, the
// same treatment -objective already has, while an operator can still opt out
// EXPLICITLY (-min-bond-floor 0) for a trusted/demo swarm. defaulted reports
// whether the value was derived rather than operator-set.
func effectiveBondFloor(floorSet bool, explicit int64, objectivePath bool) (floor int64, defaulted bool) {
	if floorSet {
		return explicit, false // an explicit choice always wins, including 0 (opt out)
	}
	if objectivePath {
		return DerivedBondFloor, true
	}
	return explicit, false
}

// DerivedBondTTL is the objective re-challenge cadence an untrusted (objective)
// validator gets when the operator sets none: standing lapses this many committed
// blocks after a validator's latest on-chain bond registration unless it renews
// with a fresh space-time proof (M0 hardening H2 / red-team RT-2). Shipping the
// TTL mechanism but defaulting it OFF left release-and-coast live — a validator
// registers a bond once, releases the plot, and votes forever off one proof — so
// like -objective and -min-bond-floor it is now SAFE-BY-DEFAULT on the objective
// path. The value trades liveness margin against how long a released bond can
// coast: the paired non-proposer renewal path (node.SubmitBondRenewal) fires
// every chain-sync sweep, so an honest validator gets many inclusion chances per
// window and never lapses, while a coaster is pruned within this many blocks. A
// tuning knob (Evolving), not a fixed law; a real deployment can tighten it.
const DerivedBondTTL = uint64(32)

// effectiveBondTTL decides the objective re-challenge TTL, mirroring
// effectiveBondFloor: an explicit -bond-ttl always wins (including 0, the
// trusted/demo opt-out), otherwise the untrusted objective path gets
// DerivedBondTTL and every other posture stays at the explicit (0) value.
func effectiveBondTTL(ttlSet bool, explicit uint64, objectivePath bool) (ttl uint64, defaulted bool) {
	if ttlSet {
		return explicit, false
	}
	if objectivePath {
		return DerivedBondTTL, true
	}
	return explicit, false
}

// effectiveByzantineQuorum decides Byzantine quorum sizing (H4), mirroring the
// floor/TTL derivations: an explicit -byzantine-quorum always wins (including
// =false, the trusted opt-out), otherwise the untrusted objective path turns it on
// and every other posture leaves it off. It only ever RAISES the quorum, so
// defaulting it on for an untrusted validator can never weaken an existing config.
func effectiveByzantineQuorum(byzSet, explicit, objectivePath bool) (on, defaulted bool) {
	if byzSet {
		return explicit, false
	}
	if objectivePath {
		return true, true
	}
	return false, false
}

// DerivedOperatorMargin is the C2 split-defense margin M an untrusted (objective)
// validator gets when the operator sets none. M discounts the bond-distinct Nakamoto
// coefficient to ⌊k̂/M⌋, so a single operator splitting real stake across ~M NodeIDs
// cannot fake the decentralization that sheds the launch anchors — a splitter must
// clear mature-validators×M distinct bonds. Shipping the mechanism but defaulting
// M=1 (no margin) was the Invariant-B footgun the blind red-team flagged. The value is
// a conservative heuristic (Evolving) — M stays fundamentally unverifiable on-chain
// (#182), so this is margin, not proof; a real deployment can raise it.
const DerivedOperatorMargin = 2

// effectiveOperatorMargin decides the C2 operator margin, mirroring the floor/TTL/
// Byzantine derivations: an explicit -operator-margin always wins (including 1, the
// trusted/single-operator opt-out), otherwise the untrusted objective path gets
// DerivedOperatorMargin and every other posture keeps the explicit value. It only ever
// raises the bar to shed the wheels, so defaulting it up for an untrusted validator can
// never weaken an existing config.
func effectiveOperatorMargin(marginSet bool, explicit int, objectivePath bool) (margin int, defaulted bool) {
	if marginSet {
		return explicit, false
	}
	if objectivePath && explicit < DerivedOperatorMargin {
		return DerivedOperatorMargin, true
	}
	return explicit, false
}
