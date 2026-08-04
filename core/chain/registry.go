package chain

import (
	"context"

	"github.com/nerolabs/silt/ports"
)

// ReplicaRegistry exposes a chain replica through the ports.Registry
// seam — the promise from M1, kept: readers (pipeline.Get, NetGet)
// never learn the registry became a blockchain. Publishing through a
// replica is refused; entries enter the chain only via consensus
// (node.ProposeEntry).
//
// HonorRevocations makes this reader subscribe to the chain's takedown
// records. It defaults to false — a bare registry resolves EVERYTHING the
// chain committed, so following the chain never silently imposes someone
// else's takedowns (red-team F5). An operator that chooses to honor on-chain
// revocations sets it true; the effect is then "proportional to who trusts
// you" (TENETS §9), the same voluntary, per-operator stance as the local
// denylist — never a global switch that every chain-follower inherits.
type ReplicaRegistry struct {
	C                *Chain
	HonorRevocations bool
}

var _ ports.Registry = ReplicaRegistry{}

func (r ReplicaRegistry) Publish(context.Context, ports.Entry) error {
	return ErrUseConsensus
}

func (r ReplicaRegistry) Lookup(_ context.Context, root ports.Hash) (ports.Entry, bool, error) {
	if r.HonorRevocations && r.C.Revoked(root) {
		return ports.Entry{}, false, nil // taken down for THIS subscribing reader
	}
	e, ok := r.C.LookupRoot(root)
	return e, ok, nil
}

func (r ReplicaRegistry) All(context.Context) ([]ports.Entry, error) {
	all := r.C.AllEntries()
	if !r.HonorRevocations {
		return all, nil
	}
	kept := all[:0]
	for _, e := range all {
		if !r.C.Revoked(e.Root) {
			kept = append(kept, e)
		}
	}
	return kept, nil
}
