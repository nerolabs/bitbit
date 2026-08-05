package dht

import (
	"sort"

	"github.com/nerolabs/silt/ports"
)

// Providers is the provider-record store: "these nodes claim to have chunk H".
// Records live on the nodes whose IDs are closest to H — that's what makes them
// findable by the same lookup that finds nodes.
//
// A record is a ports.ProviderRecord, which MAY be self-certifying: a signed one
// binds the claim to the provider's identity and an expiry, so a fetcher the
// record is re-served to can verify it rather than trust a (possibly eclipsing)
// responder (M0 H5). Unsigned records carry only the NodeID (legacy/trusted).
// The store itself keeps whatever it is handed — verification is the caller's job
// (see node.RequireSignedProviders), because the same store serves strict and
// legacy deployments; a fetcher still hash-verifies chunk bytes on receipt, so a
// lying provider wastes time, never corrupts data.
type Providers struct {
	m map[ports.Hash][]ports.ProviderRecord // per key, deduped by provider ID, insertion-ordered
}

func NewProviders() *Providers {
	return &Providers{m: make(map[ports.Hash][]ports.ProviderRecord)}
}

// Add records rec.ID as a provider for rec.Key. A repeat announcement from the
// same provider REPLACES the prior record (so a refreshed signature/expiry wins)
// rather than duplicating it.
func (p *Providers) Add(rec ports.ProviderRecord) {
	list := p.m[rec.Key]
	for i, existing := range list {
		if existing.ID == rec.ID {
			list[i] = rec
			return
		}
	}
	p.m[rec.Key] = append(list, rec)
}

// Get returns the records for key, closest-provider-first (deterministic order —
// map iteration must never leak into behavior).
func (p *Providers) Get(key ports.Hash) []ports.ProviderRecord {
	src := p.m[key]
	out := make([]ports.ProviderRecord, len(src))
	copy(out, src)
	SortRecordsByDistance(key, out)
	return out
}

// IDs returns just the provider NodeIDs for key, closest-first — the legacy
// accessor for internal callers that don't need the signatures.
func (p *Providers) IDs(key ports.Hash) []ports.NodeID {
	recs := p.Get(key)
	out := make([]ports.NodeID, len(recs))
	for i, r := range recs {
		out[i] = r.ID
	}
	return out
}

func (p *Providers) Remove(provider ports.NodeID) {
	for key, list := range p.m {
		kept := list[:0]
		for _, r := range list {
			if r.ID != provider {
				kept = append(kept, r)
			}
		}
		p.m[key] = kept
	}
}

// Len returns the number of keys with at least one provider.
func (p *Providers) Len() int {
	n := 0
	for _, list := range p.m {
		if len(list) > 0 {
			n++
		}
	}
	return n
}

// SortRecordsByDistance orders records by ascending XOR distance of their
// provider ID to target, in place.
func SortRecordsByDistance(target ports.Hash, recs []ports.ProviderRecord) {
	sort.Slice(recs, func(i, j int) bool { return Closer(target, recs[i].ID, recs[j].ID) })
}
