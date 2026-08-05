package node

import (
	"sort"
	"strconv"
	"testing"

	"github.com/nerolabs/silt/adapters/identity"
	"github.com/nerolabs/silt/adapters/memstore"
	"github.com/nerolabs/silt/adapters/simclock"
	"github.com/nerolabs/silt/adapters/simnet"
	"github.com/nerolabs/silt/core/dht"
	"github.com/nerolabs/silt/ports"
)

// M0 hardening H5-B (Memo 08): a key must stay DISCOVERABLE even when an adversary
// owns the NodeIDs closest to it but sits in a SINGLE failure domain — the cheap
// (~$4) /24 key-surround. With failure-domain diversity on, provider records are
// announced to, and resolved from, a set spread across domains, so an honest node
// in a domain the adversary doesn't control both holds the record and answers for
// it; the per-bucket domain cap keeps those honest peers routable. This is the
// suppression half of S5 (the forgery half is H5-A / redteam_h5_test.go).
func TestRedteamH5B_KeyDiscoverableUnderSingleDomainKeySurround(t *testing.T) {
	// discoverable runs the whole scenario at a given domain cap (0 = diversity off)
	// and reports whether the fetcher can still find the honest provider of a key the
	// adversary has surrounded.
	discoverable := func(t *testing.T, cap int) bool {
		t.Helper()
		sched := simclock.New()
		net := simnet.New(sched, 1, simnet.DefaultConfig())
		key := ports.HashBytes([]byte("surrounded-key"))

		// Grind identities and sort by XOR distance to the key: the closest are the
		// key-surround adversary (that's exactly what an attacker grinds for).
		cands := make([]*identity.Identity, 3000)
		for i := range cands {
			cands[i] = identity.FromSeed(int64(i) + 1)
		}
		sort.Slice(cands, func(i, j int) bool {
			return dht.Closer(key, cands[i].NodeID(), cands[j].NodeID())
		})

		mk := func(ident *identity.Identity, domain string) *Node {
			cfg := DefaultConfig()
			cfg.Domain = domain
			cfg.DHTDomainCap = cap
			cfg.RequireSignedProviders = true // strict signed records (H5-A) + diversity (H5-B)
			nd := New(ident.NodeID(), cfg, sched, net.Endpoint(ident.NodeID()), memstore.New())
			nd.SetSigner(ident.Signer())
			return nd
		}

		var all []*Node
		// The 10 NodeIDs closest to the key: one adversary domain, all suppressing.
		for i := 0; i < 10; i++ {
			a := mk(cands[i], "adversary")
			a.SetEclipser(true)
			all = append(all, a)
		}
		// Honest nodes drawn from far away, each its OWN distinct domain.
		var honest []*Node
		for i := 0; i < 8; i++ {
			h := mk(cands[800+i], "honest-"+strconv.Itoa(i))
			honest = append(honest, h)
			all = append(all, h)
		}
		provider := honest[0] // holds and announces the record
		fetcher := mk(cands[1600], "fetcher")
		all = append(all, fetcher)

		// Wire up the network deterministically: every node learns every other's
		// domain (as gossip would carry it) and then observes it — so the per-bucket
		// domain cap actually applies. This isolates the diversity mechanism from
		// lookup-convergence luck.
		for _, a := range all {
			for _, b := range all {
				if a == b {
					continue
				}
				a.peerDomains[b.ID()] = b.domainID
				a.table.Observe(b.ID())
			}
		}

		provider.provs.Add(provider.providerRecord(key)) // provider registers itself
		announced := false
		provider.announceAll([]ports.ChunkID{key}, func() { announced = true })
		sched.Run()
		if !announced {
			t.Fatal("announce never completed")
		}

		var provs []ports.NodeID
		resolved := false
		fetcher.resolveProviders(key, func(p []ports.NodeID) { provs = p; resolved = true })
		sched.Run()
		if !resolved {
			t.Fatal("resolve never completed")
		}
		for _, p := range provs {
			if p == provider.ID() {
				return true
			}
		}
		return false
	}

	// Control: with diversity OFF the record is announced only to the closest
	// NodeIDs — all the suppressing adversary — and the resolve walk reaches only
	// them, so the surrounded key is NOT discoverable.
	if discoverable(t, 0) {
		t.Fatal("control: without domain diversity a single-domain key-surround should suppress discovery")
	}
	// With diversity ON the key stays discoverable via honest cross-domain nodes.
	if !discoverable(t, 2) {
		t.Fatal("H5-B: with domain diversity the surrounded key MUST stay discoverable through honest nodes in other domains")
	}
}

// TestH5B_DiverseNearSpreadsAcrossDomains: diverseNear caps per domain, so a
// single-domain cluster nearest a key can't monopolise the near set.
func TestH5B_DiverseNearSpreadsAcrossDomains(t *testing.T) {
	sched := simclock.New()
	net := simnet.New(sched, 1, simnet.DefaultConfig())
	self := identity.FromSeed(1)
	cfg := DefaultConfig()
	cfg.DHTDomainCap = 2
	n := New(self.NodeID(), cfg, sched, net.Endpoint(self.NodeID()), memstore.New())

	key := ports.HashBytes([]byte("k"))
	// 20 peers in one dominant domain + 3 in distinct others.
	for i := 0; i < 20; i++ {
		id := identity.FromSeed(int64(1000 + i)).NodeID()
		n.peerDomains[id] = 0xD0 // one domain
		n.table.Observe(id)
	}
	for i := 0; i < 3; i++ {
		id := identity.FromSeed(int64(2000 + i)).NodeID()
		n.peerDomains[id] = uint64(0xA0 + i) // distinct domains
		n.table.Observe(id)
	}
	got := n.diverseNear(key, 8)
	domains := map[uint64]int{}
	for _, id := range got {
		domains[n.domainOf(id)]++
	}
	if domains[0xD0] > 2 {
		t.Fatalf("diverseNear admitted %d peers from the dominant domain, cap is 2", domains[0xD0])
	}
	if len(domains) < 2 {
		t.Fatal("diverseNear must span multiple domains, not just the dominant one")
	}
}

// TestH5B_RoutingBucketCapsPerDomain: the routing table itself refuses to keep more
// than the cap of one domain in a bucket, so a Sybil cluster can't fill it.
func TestH5B_RoutingBucketCapsPerDomain(t *testing.T) {
	self := identity.FromSeed(1).NodeID()
	tab := dht.NewTable(self, 20)
	domainOf := map[ports.NodeID]uint64{}
	tab.SetDiversity(func(id ports.NodeID) uint64 { return domainOf[id] }, 2)

	// Many peers that all share one domain and fall in the SAME bucket (share the
	// top bit with a fixed pattern relative to self) — only `cap` may be admitted.
	var sameBucket []ports.NodeID
	for i := 0; len(sameBucket) < 6 && i < 5000; i++ {
		id := identity.FromSeed(int64(10_000 + i)).NodeID()
		if dht.BucketIndex(self, id) == dht.BucketIndex(self, identity.FromSeed(10_000).NodeID()) {
			sameBucket = append(sameBucket, id)
		}
	}
	for _, id := range sameBucket {
		domainOf[id] = 0xBAD
		tab.Observe(id)
	}
	kept := 0
	for _, id := range tab.Closest(self, 100) {
		if domainOf[id] == 0xBAD {
			kept++
		}
	}
	if kept > 2 {
		t.Fatalf("routing table kept %d peers from one domain in a bucket, cap is 2 — a single-domain Sybil cluster can fill the table", kept)
	}
}
