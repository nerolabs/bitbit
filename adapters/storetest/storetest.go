// Package storetest is a conformance suite every ChunkStore adapter must
// pass — one behavioral contract, however many implementations.
package storetest

import (
	"context"
	"errors"
	"testing"

	"shardnet/ports"
)

func Run(t *testing.T, newStore func(t *testing.T) ports.ChunkStore) {
	ctx := context.Background()

	t.Run("PutGetRoundtrip", func(t *testing.T) {
		s := newStore(t)
		c := ports.NewChunk([]byte("hello, shardnet"))
		if err := s.Put(ctx, c); err != nil {
			t.Fatal(err)
		}
		got, err := s.Get(ctx, c.ID)
		if err != nil {
			t.Fatal(err)
		}
		if string(got.Data) != string(c.Data) {
			t.Fatal("Get returned different bytes")
		}
	})

	t.Run("PutRejectsCorrupt", func(t *testing.T) {
		s := newStore(t)
		c := ports.NewChunk([]byte("valid"))
		c.Data = []byte("tampered before Put")
		if err := s.Put(ctx, c); err == nil {
			t.Fatal("Put must reject a chunk whose data does not hash to its ID")
		}
	})

	t.Run("GetMissing", func(t *testing.T) {
		s := newStore(t)
		_, err := s.Get(ctx, ports.HashBytes([]byte("never stored")))
		if !errors.Is(err, ports.ErrNotFound) {
			t.Fatalf("want ErrNotFound, got %v", err)
		}
	})

	t.Run("HasListDelete", func(t *testing.T) {
		s := newStore(t)
		c1 := ports.NewChunk([]byte("one"))
		c2 := ports.NewChunk([]byte("two"))
		for _, c := range []ports.Chunk{c1, c2} {
			if err := s.Put(ctx, c); err != nil {
				t.Fatal(err)
			}
		}
		if ok, _ := s.Has(ctx, c1.ID); !ok {
			t.Fatal("Has(stored) = false")
		}
		ids, err := s.List(ctx)
		if err != nil || len(ids) != 2 {
			t.Fatalf("List: %v, %d ids (want 2)", err, len(ids))
		}
		if err := s.Delete(ctx, c1.ID); err != nil {
			t.Fatal(err)
		}
		if ok, _ := s.Has(ctx, c1.ID); ok {
			t.Fatal("Has(deleted) = true")
		}
		if _, err := s.Get(ctx, c1.ID); !errors.Is(err, ports.ErrNotFound) {
			t.Fatalf("Get(deleted): want ErrNotFound, got %v", err)
		}
	})

	t.Run("PutIsIdempotent", func(t *testing.T) {
		s := newStore(t)
		c := ports.NewChunk([]byte("same chunk twice"))
		if err := s.Put(ctx, c); err != nil {
			t.Fatal(err)
		}
		if err := s.Put(ctx, c); err != nil {
			t.Fatalf("second Put of identical chunk: %v", err)
		}
	})
}
