package dht

import "shardnet/ports"

// Providers is the provider-record store: "these nodes claim to have
// chunk H". Records live on the nodes whose IDs are closest to H —
// that's what makes them findable by the same lookup that finds nodes.
// Claims are unverified by design; fetchers verify the chunk hash itself
// on receipt, so a lying provider wastes time, never corrupts data.
type Providers struct {
	m map[ports.Hash][]ports.NodeID // insertion-ordered, deduped
}

func NewProviders() *Providers {
	return &Providers{m: make(map[ports.Hash][]ports.NodeID)}
}

func (p *Providers) Add(key ports.Hash, provider ports.NodeID) {
	for _, existing := range p.m[key] {
		if existing == provider {
			return
		}
	}
	p.m[key] = append(p.m[key], provider)
}

// Get returns providers for key, closest-to-key first (deterministic
// order — map iteration must never leak into behavior).
func (p *Providers) Get(key ports.Hash) []ports.NodeID {
	out := append([]ports.NodeID(nil), p.m[key]...)
	SortByDistance(key, out)
	return out
}

func (p *Providers) Remove(provider ports.NodeID) {
	for key, list := range p.m {
		kept := list[:0]
		for _, id := range list {
			if id != provider {
				kept = append(kept, id)
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
