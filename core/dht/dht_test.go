package dht

import (
	"math/rand"
	"testing"
	"testing/quick"

	"github.com/nerolabs/silt/ports"
)

func randID(rng *rand.Rand) ports.NodeID {
	var id ports.NodeID
	rng.Read(id[:])
	return id
}

func TestDistanceIsAMetric(t *testing.T) {
	f := func(a, b, c [32]byte) bool {
		da := Distance(a, a)
		for _, x := range da {
			if x != 0 { // d(a,a) = 0
				return false
			}
		}
		if Distance(a, b) != Distance(b, a) { // symmetry
			return false
		}
		// XOR's special gift: d(a,b) XOR d(b,c) == d(a,c) exactly —
		// stronger than the triangle inequality.
		ab, bc, ac := Distance(a, b), Distance(b, c), Distance(a, c)
		for i := range ab {
			if ab[i]^bc[i] != ac[i] {
				return false
			}
		}
		return true
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 200}); err != nil {
		t.Fatal(err)
	}
}

func TestBucketIndex(t *testing.T) {
	var self ports.NodeID // zero ID keeps the arithmetic readable
	if BucketIndex(self, self) != -1 {
		t.Fatal("self must have no bucket")
	}
	var id ports.NodeID
	id[0] = 0x80 // differs in the very first bit
	if got := BucketIndex(self, id); got != 255 {
		t.Fatalf("first-bit difference: bucket %d, want 255", got)
	}
	id = ports.NodeID{}
	id[31] = 0x01 // differs only in the last bit
	if got := BucketIndex(self, id); got != 0 {
		t.Fatalf("last-bit difference: bucket %d, want 0", got)
	}
	id = ports.NodeID{}
	id[31] = 0x0A // highest set bit is bit 3
	if got := BucketIndex(self, id); got != 3 {
		t.Fatalf("bucket %d, want 3", got)
	}
}

func TestTableObserveAndClosest(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	self := randID(rng)
	tab := NewTable(self, 4)
	var all []ports.NodeID
	for i := 0; i < 200; i++ {
		id := randID(rng)
		tab.Observe(id)
		all = append(all, id)
	}
	tab.Observe(self) // must be ignored
	if tab.Size() > 200 {
		t.Fatal("table grew past what it observed")
	}

	target := randID(rng)
	got := tab.Closest(target, 8)
	if len(got) != 8 {
		t.Fatalf("got %d, want 8", len(got))
	}
	for i := 1; i < len(got); i++ {
		if Closer(target, got[i], got[i-1]) {
			t.Fatal("Closest results not sorted by distance")
		}
	}
	// Everything returned must actually be in the table (no self).
	for _, id := range got {
		if id == self {
			t.Fatal("table returned self")
		}
	}
}

func TestTableBucketFullDropsNewcomer(t *testing.T) {
	var self ports.NodeID
	tab := NewTable(self, 2)
	// Three IDs in the same bucket (255): first bit set.
	mk := func(b byte) ports.NodeID {
		var id ports.NodeID
		id[0] = 0x80
		id[31] = b
		return id
	}
	tab.Observe(mk(1))
	tab.Observe(mk(2))
	tab.Observe(mk(3)) // bucket full: dropped
	if tab.Size() != 2 {
		t.Fatalf("size %d, want 2", tab.Size())
	}
	tab.Remove(mk(1))
	tab.Observe(mk(3)) // room now
	if tab.Size() != 2 {
		t.Fatalf("size %d after remove+observe, want 2", tab.Size())
	}
}

// The load-bearing test: run the lookup state machine over a synthetic
// network where every node knows its own k closest peers, and check it
// finds the TRUE k closest nodes to the target — in O(log N) rounds.
func TestLookupConvergesInLogNRounds(t *testing.T) {
	const (
		nNodes = 500
		k      = 8
		alpha  = 3
	)
	rng := rand.New(rand.NewSource(42))
	ids := make([]ports.NodeID, nNodes)
	for i := range ids {
		ids[i] = randID(rng)
	}
	// Each node gets a real, fully-populated k-bucket table. (An earlier
	// draft gave nodes only their own neighborhood — and lookups got
	// stuck, which is a nice accidental demonstration of WHY Kademlia
	// keeps contacts at every distance scale: your nearest neighbors
	// share your prefix and know nothing about the other subtrees.)
	tables := make(map[ports.NodeID]*Table, nNodes)
	for _, n := range ids {
		tab := NewTable(n, k)
		for _, other := range ids {
			tab.Observe(other)
		}
		tables[n] = tab
	}
	knows := func(n ports.NodeID, target ports.Hash) []ports.NodeID {
		return tables[n].Closest(target, k)
	}

	for trial := 0; trial < 25; trial++ {
		target := randID(rng)
		truth := append([]ports.NodeID(nil), ids...)
		SortByDistance(target, truth)
		truth = truth[:k]

		start := ids[rng.Intn(nNodes)]
		l := NewLookup(target, k, alpha, knows(start, target))
		rounds, queries := 0, 0
		for !l.Done() {
			batch := l.NextQueries()
			if len(batch) == 0 {
				t.Fatal("lookup stuck: not done but nothing to query")
			}
			rounds++
			for _, peer := range batch {
				queries++
				l.OnReply(peer, knows(peer, target))
			}
		}
		got := l.Result()
		for i, want := range truth {
			if got[i] != want {
				t.Fatalf("trial %d: result[%d] wrong (found %d of true top-%d)", trial, i, i, k)
			}
		}
		// log2(500) ≈ 9; allow generous slack, catch O(N) regressions.
		if rounds > 30 {
			t.Fatalf("trial %d: %d rounds for %d nodes — not O(log N)", trial, rounds, nNodes)
		}
		if queries > 6*k {
			t.Fatalf("trial %d: %d queries — too chatty", trial, queries)
		}
	}
}

func TestLookupSurvivesFailures(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	ids := make([]ports.NodeID, 50)
	for i := range ids {
		ids[i] = randID(rng)
	}
	target := randID(rng)
	l := NewLookup(target, 4, 2, ids[:8])
	for !l.Done() {
		for _, peer := range l.NextQueries() {
			if rng.Float64() < 0.3 {
				l.OnFailure(peer) // 30% of peers are dead
			} else {
				more := []ports.NodeID{ids[rng.Intn(len(ids))], ids[rng.Intn(len(ids))]}
				l.OnReply(peer, more)
			}
		}
	}
	if len(l.Result()) == 0 {
		t.Fatal("lookup with failures returned nothing")
	}
	for _, id := range l.Result() {
		if l.state[id] != stateReplied {
			t.Fatal("Result contains a peer that never replied")
		}
	}
}

func TestProvidersDeterministicOrder(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	p := NewProviders()
	key := randID(rng)
	var added []ports.NodeID
	for i := 0; i < 10; i++ {
		id := randID(rng)
		p.Add(ports.ProviderRecord{Key: key, ID: id})
		p.Add(ports.ProviderRecord{Key: key, ID: id}) // dedup
		added = append(added, id)
	}
	got := p.IDs(key)
	if len(got) != 10 {
		t.Fatalf("got %d providers, want 10", len(got))
	}
	for i := 1; i < len(got); i++ {
		if Closer(key, got[i], got[i-1]) {
			t.Fatal("providers not sorted by distance to key")
		}
	}
	p.Remove(added[0])
	for _, id := range p.IDs(key) {
		if id == added[0] {
			t.Fatal("removed provider still present")
		}
	}
}
