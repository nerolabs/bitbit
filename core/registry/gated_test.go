package registry_test

import (
	"context"
	"errors"
	"testing"

	"github.com/nerolabs/bitbit/core/credit"
	"github.com/nerolabs/bitbit/core/registry"
	"github.com/nerolabs/bitbit/ports"
)

func gatedEntry(rootByte byte, publisher ports.NodeID) ports.Entry {
	e := entry(rootByte, 100)
	e.Publisher = publisher
	return e
}

func TestGatedPublish(t *testing.T) {
	ctx := context.Background()
	ledger := credit.New(100, 0)
	g := registry.NewGated(ledger)
	rich, poor := ports.HashBytes([]byte("rich")), ports.HashBytes([]byte("poor"))
	ledger.Register(rich)
	ledger.Register(poor)
	ledger.RecordServe(rich, poor, ports.HashBytes([]byte("c")), 250)

	if err := g.Publish(ctx, entry(1, 100)); !errors.Is(err, ports.ErrPublisherRequired) {
		t.Fatalf("anonymous publish: want ErrPublisherRequired, got %v", err)
	}
	if err := g.Publish(ctx, gatedEntry(1, poor)); !errors.Is(err, ports.ErrInsufficientCredit) {
		t.Fatalf("broke publish: want ErrInsufficientCredit, got %v", err)
	}
	if err := g.Publish(ctx, gatedEntry(1, rich)); err != nil {
		t.Fatal(err)
	}
	if got := ledger.Balance(rich); got != 150 {
		t.Fatalf("fee not charged: balance %d, want 150", got)
	}
	// Identical republish: free, no double charge.
	if err := g.Publish(ctx, gatedEntry(1, rich)); err != nil {
		t.Fatal(err)
	}
	if got := ledger.Balance(rich); got != 150 {
		t.Fatalf("idempotent republish charged again: balance %d", got)
	}
	// Second unique publish: charged; third: broke.
	if err := g.Publish(ctx, gatedEntry(2, rich)); err != nil {
		t.Fatal(err)
	}
	if err := g.Publish(ctx, gatedEntry(3, rich)); !errors.Is(err, ports.ErrInsufficientCredit) {
		t.Fatalf("want ErrInsufficientCredit after wealth spent, got %v", err)
	}
	all, _ := g.All(ctx)
	if len(all) != 2 {
		t.Fatalf("registry has %d entries, want 2", len(all))
	}
}
