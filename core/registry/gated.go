package registry

import (
	"context"

	"github.com/nerolabs/bitbit/ports"
)

// Gated wraps the append-only log with credit gating: publishing costs
// the ledger's fee, paid by the entry's Publisher. This is where the
// economics touch the registry — and the only place; lookups stay free
// because reading must never require wealth.
type Gated struct {
	log    *Log
	ledger ports.CreditLedger
}

var _ ports.Registry = (*Gated)(nil)

func NewGated(ledger ports.CreditLedger) *Gated {
	return &Gated{log: New(), ledger: ledger}
}

func (g *Gated) Publish(ctx context.Context, e ports.Entry) error {
	if e.Publisher == (ports.NodeID{}) {
		return ports.ErrPublisherRequired
	}
	// Idempotent republish of an identical entry stays free — the log
	// won't grow, so nothing was bought.
	if existing, ok, _ := g.log.Lookup(ctx, e.Root); ok {
		if err := g.log.Publish(ctx, e); err != nil {
			return err
		}
		_ = existing
		return nil
	}
	if !g.ledger.CanPublish(e.Publisher) {
		return ports.ErrInsufficientCredit
	}
	if err := g.log.Publish(ctx, e); err != nil {
		return err
	}
	return g.ledger.ChargePublish(e.Publisher)
}

func (g *Gated) Lookup(ctx context.Context, root ports.Hash) (ports.Entry, bool, error) {
	return g.log.Lookup(ctx, root)
}

func (g *Gated) All(ctx context.Context) ([]ports.Entry, error) {
	return g.log.All(ctx)
}
