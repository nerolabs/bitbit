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
type ReplicaRegistry struct {
	C *Chain
}

var _ ports.Registry = ReplicaRegistry{}

func (r ReplicaRegistry) Publish(context.Context, ports.Entry) error {
	return ErrUseConsensus
}

func (r ReplicaRegistry) Lookup(_ context.Context, root ports.Hash) (ports.Entry, bool, error) {
	if r.C.Revoked(root) {
		return ports.Entry{}, false, nil // taken down: unresolvable
	}
	e, ok := r.C.LookupRoot(root)
	return e, ok, nil
}

func (r ReplicaRegistry) All(context.Context) ([]ports.Entry, error) {
	all := r.C.AllEntries()
	kept := all[:0]
	for _, e := range all {
		if !r.C.Revoked(e.Root) {
			kept = append(kept, e)
		}
	}
	return kept, nil
}
