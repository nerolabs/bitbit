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
	e, ok := r.C.LookupRoot(root)
	return e, ok, nil
}

func (r ReplicaRegistry) All(context.Context) ([]ports.Entry, error) {
	return r.C.AllEntries(), nil
}
