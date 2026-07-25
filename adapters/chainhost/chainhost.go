// Package chainhost fronts a validator's chain replica as a
// ports.Registry for HTTP clients — the piece that lets `bitbit swarm
// add/get` keep working unchanged after the registry becomes a chain.
// Publish triggers a consensus round on the daemon's event loop and
// blocks the (goroutine-per-request) HTTP handler until commit or
// timeout; reads serve straight from the local replica.
package chainhost

import (
	"context"
	"fmt"
	"time"

	"github.com/nerolabs/bitbit/adapters/eventloop"
	"github.com/nerolabs/bitbit/core/chain"
	"github.com/nerolabs/bitbit/core/node"
	"github.com/nerolabs/bitbit/ports"
)

type Host struct {
	Loop      *eventloop.Loop
	Node      *node.Node
	Attesters []ports.NodeID
	Broadcast []ports.NodeID
	Quorum    int
	Timeout   time.Duration
}

var _ ports.Registry = (*Host)(nil)

func (h *Host) onLoop(fn func(done func())) error {
	ch := make(chan struct{})
	h.Loop.Post(func() { fn(func() { close(ch) }) })
	timeout := h.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	select {
	case <-ch:
		return nil
	case <-time.After(timeout):
		return fmt.Errorf("chainhost: consensus timed out")
	}
}

func (h *Host) Publish(_ context.Context, e ports.Entry) error {
	var result error
	err := h.onLoop(func(done func()) {
		// Idempotent republish of an identical entry is a free no-op.
		if existing, ok := h.Node.Chain().LookupRoot(e.Root); ok {
			if existing.Root == e.Root && existing.FileSize == e.FileSize {
				done()
				return
			}
			result = ports.ErrDupPublish
			done()
			return
		}
		h.Node.ProposeEntry(e, h.Attesters, h.Broadcast, h.Quorum, func(err error) {
			result = err
			done()
		})
	})
	if err != nil {
		return err
	}
	return result
}

func (h *Host) Lookup(_ context.Context, root ports.Hash) (ports.Entry, bool, error) {
	var e ports.Entry
	var ok bool
	err := h.onLoop(func(done func()) {
		e, ok = h.Node.Chain().LookupRoot(root)
		done()
	})
	return e, ok, err
}

func (h *Host) All(context.Context) ([]ports.Entry, error) {
	var out []ports.Entry
	err := h.onLoop(func(done func()) {
		out = h.Node.Chain().AllEntries()
		done()
	})
	return out, err
}

// Blocks snapshots the replica for persistence.
func (h *Host) Blocks() []chain.Block {
	var out []chain.Block
	h.onLoop(func(done func()) {
		out = h.Node.Chain().Blocks(0)
		done()
	})
	return out
}
