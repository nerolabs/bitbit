package diskstore_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/nerolabs/bitbit/adapters/diskstore"
	"github.com/nerolabs/bitbit/adapters/storetest"
	"github.com/nerolabs/bitbit/ports"
)

func TestConformance(t *testing.T) {
	storetest.Run(t, func(t *testing.T) ports.ChunkStore {
		s, err := diskstore.Open(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		return s
	})
}

func TestOnDiskCorruptionIsCaught(t *testing.T) {
	dir := t.TempDir()
	s, err := diskstore.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	c := ports.NewChunk([]byte("bytes that will rot on disk"))
	if err := s.Put(ctx, c); err != nil {
		t.Fatal(err)
	}
	// Flip a byte in the underlying file, as a failing disk would.
	hex := c.ID.String()
	path := filepath.Join(dir, "objects", hex[:2], hex)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data[0] ^= 0xFF
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, c.ID); !errors.Is(err, ports.ErrCorrupt) {
		t.Fatalf("want ErrCorrupt, got %v", err)
	}
}
