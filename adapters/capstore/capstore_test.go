package capstore_test

import (
	"context"
	"errors"
	"testing"

	"github.com/nerolabs/bitbit/adapters/capstore"
	"github.com/nerolabs/bitbit/adapters/memstore"
	"github.com/nerolabs/bitbit/adapters/storetest"
	"github.com/nerolabs/bitbit/ports"
)

func TestConformance(t *testing.T) {
	storetest.Run(t, func(t *testing.T) ports.ChunkStore {
		s, err := capstore.Open(memstore.New(), 1<<20)
		if err != nil {
			t.Fatal(err)
		}
		return s
	})
}

func TestBudgetEnforcedAndReclaimed(t *testing.T) {
	ctx := context.Background()
	s, err := capstore.Open(memstore.New(), 100)
	if err != nil {
		t.Fatal(err)
	}
	c1 := ports.NewChunk(make([]byte, 60))
	c2raw := make([]byte, 60)
	c2raw[0] = 1
	c2 := ports.NewChunk(c2raw)

	if err := s.Put(ctx, c1); err != nil {
		t.Fatal(err)
	}
	if used, total := s.Capacity(); used != 60 || total != 100 {
		t.Fatalf("capacity %d/%d, want 60/100", used, total)
	}
	if err := s.Put(ctx, c2); !errors.Is(err, ports.ErrStoreFull) {
		t.Fatalf("over-budget put: want ErrStoreFull, got %v", err)
	}
	if err := s.Put(ctx, c1); err != nil { // re-put of held chunk: free
		t.Fatalf("idempotent re-put charged the budget: %v", err)
	}
	if err := s.Delete(ctx, c1.ID); err != nil {
		t.Fatal(err)
	}
	if used, _ := s.Capacity(); used != 0 {
		t.Fatalf("used %d after delete, want 0", used)
	}
	if err := s.Put(ctx, c2); err != nil {
		t.Fatalf("put after reclaim: %v", err)
	}
}

func TestReopenMeasuresExisting(t *testing.T) {
	ctx := context.Background()
	inner := memstore.New()
	c := ports.NewChunk(make([]byte, 40))
	if err := inner.Put(ctx, c); err != nil {
		t.Fatal(err)
	}
	s, err := capstore.Open(inner, 100)
	if err != nil {
		t.Fatal(err)
	}
	if used, _ := s.Capacity(); used != 40 {
		t.Fatalf("reopen measured %d used, want 40", used)
	}
}
