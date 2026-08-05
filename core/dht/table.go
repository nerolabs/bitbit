package dht

import "github.com/nerolabs/silt/ports"

// Table is the k-bucket routing table. Bucket i holds peers whose
// highest differing bit from self is i — i.e. peers at distance
// [2^i, 2^(i+1)). Close buckets are tiny neighborhoods the node knows
// exhaustively; far buckets each cover half the network in k samples.
// That asymmetry is the whole O(log N) trick.
type Table struct {
	self    ports.NodeID
	k       int
	buckets [256][]ports.NodeID // each ordered oldest → newest
	// Failure-domain diversity (M0 H5-B / Memo 08): when domainOf is set and
	// perDomainCap > 0, a bucket admits at most perDomainCap peers sharing one
	// domain, so an adversary minting many NodeIDs in ONE domain (a cheap key-
	// surround from a single /24) can't fill the buckets near a key and crowd out
	// the honest, domain-diverse peers a lookup needs. Domain 0 (unknown) is never
	// capped — an unknown peer is assumed independent (like preferFreshDomain).
	domainOf     func(ports.NodeID) uint64
	perDomainCap int
}

func NewTable(self ports.NodeID, k int) *Table {
	return &Table{self: self, k: k}
}

func (t *Table) Self() ports.NodeID { return t.self }

// SetDiversity turns on the per-bucket domain cap (H5-B). domainOf resolves a
// peer's failure domain (0 = unknown); perDomainCap ≤ 0 disables the cap.
func (t *Table) SetDiversity(domainOf func(ports.NodeID) uint64, perDomainCap int) {
	t.domainOf = domainOf
	t.perDomainCap = perDomainCap
}

// domainSaturated reports whether bucket b already holds perDomainCap peers in
// id's (known) domain — in which case a NEW peer from that domain is not admitted.
func (t *Table) domainSaturated(b []ports.NodeID, id ports.NodeID) bool {
	if t.domainOf == nil || t.perDomainCap <= 0 {
		return false
	}
	d := t.domainOf(id)
	if d == 0 {
		return false // unknown domain: assumed independent, never capped
	}
	cnt := 0
	for _, e := range b {
		if t.domainOf(e) == d {
			cnt++
		}
	}
	return cnt >= t.perDomainCap
}

// Observe records that a peer was seen alive. Known peers move to the
// back (newest). New peers join if the bucket has room; if it's full the
// newcomer is dropped — Kademlia's insight is that the oldest live peer
// is statistically the most reliable, so incumbents win. (The textbook
// refinement — ping the oldest and evict only if it's dead — can slot in
// here when repair lands in M4.)
func (t *Table) Observe(id ports.NodeID) {
	i := BucketIndex(t.self, id)
	if i < 0 {
		return // never table yourself
	}
	b := t.buckets[i]
	for j, existing := range b {
		if existing == id {
			t.buckets[i] = append(append(b[:j:j], b[j+1:]...), id)
			return
		}
	}
	if len(b) < t.k && !t.domainSaturated(b, id) {
		t.buckets[i] = append(b, id)
	}
}

// Remove drops a peer (observed dead, e.g. a request timed out).
func (t *Table) Remove(id ports.NodeID) {
	i := BucketIndex(t.self, id)
	if i < 0 {
		return
	}
	b := t.buckets[i]
	for j, existing := range b {
		if existing == id {
			t.buckets[i] = append(b[:j:j], b[j+1:]...)
			return
		}
	}
}

// Closest returns up to n known peers nearest to target.
func (t *Table) Closest(target ports.Hash, n int) []ports.NodeID {
	var all []ports.NodeID
	for _, b := range t.buckets {
		all = append(all, b...)
	}
	SortByDistance(target, all)
	if len(all) > n {
		all = all[:n]
	}
	return all
}

// Size returns the number of known peers.
func (t *Table) Size() int {
	n := 0
	for _, b := range t.buckets {
		n += len(b)
	}
	return n
}
