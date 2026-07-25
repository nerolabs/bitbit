package linkbook_test

import (
	"path/filepath"
	"testing"

	"github.com/nerolabs/silt/adapters/linkbook"
	"github.com/nerolabs/silt/core/link"
	"github.com/nerolabs/silt/ports"
)

func aLink(seed byte) string {
	h := link.Handle{Root: ports.HashBytes([]byte{seed}), Key: ports.HashBytes([]byte{seed, seed})}
	return h.String()
}

func TestAddDedupeRemovePersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "links.json")
	b, err := linkbook.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Add(aLink(1), "movie"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Add(aLink(2), "doc"); err != nil {
		t.Fatal(err)
	}
	// Re-adding the same root updates the label, doesn't duplicate.
	if _, err := b.Add(aLink(1), "renamed"); err != nil {
		t.Fatal(err)
	}
	items := b.Items()
	if len(items) != 2 {
		t.Fatalf("want 2 items, got %d", len(items))
	}

	// Reopen: persistence survives.
	b2, err := linkbook.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	got := b2.Items()
	if len(got) != 2 {
		t.Fatalf("after reopen want 2, got %d", len(got))
	}
	found := false
	for _, it := range got {
		if it.Label == "renamed" {
			found = true
		}
	}
	if !found {
		t.Fatal("label update did not persist")
	}

	// Remove by root.
	root1 := ports.HashBytes([]byte{1}).String()
	if err := b2.Remove(root1); err != nil {
		t.Fatal(err)
	}
	if len(b2.Items()) != 1 {
		t.Fatal("remove failed")
	}
}

func TestAddRejectsJunk(t *testing.T) {
	b, _ := linkbook.Open(filepath.Join(t.TempDir(), "l.json"))
	if _, err := b.Add("not-a-link", ""); err == nil {
		t.Fatal("junk link must be rejected")
	}
	if _, err := b.Add(aLink(1)[:20], ""); err == nil {
		t.Fatal("truncated link must be rejected")
	}
}
