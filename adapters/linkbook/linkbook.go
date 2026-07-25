// Package linkbook persists the silt links a user holds — the files
// they actually have keys for. This is the honest core of the client's
// "library": the chain lists opaque roots the network hosts, but only a
// link (root + key) opens one, and keys arrive out of band (or via a
// resolver like Aslan). So a user's library IS their link book, not the
// registry. Stored as JSON, deduped by root, newest first.
package linkbook

import (
	"encoding/json"
	"os"
	"sync"
	"time"

	"github.com/nerolabs/silt/core/link"
)

// Item is one saved link with optional user-supplied label and the time
// it was added. Label is the ONLY place human meaning touches the
// client — supplied by the user, never learned from the network.
type Item struct {
	Link  string `json:"link"`
	Label string `json:"label,omitempty"`
	Added int64  `json:"added"`
}

type Book struct {
	mu    sync.Mutex
	path  string
	items []Item
}

func Open(path string) (*Book, error) {
	b := &Book{path: path}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return b, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &b.items); err != nil {
		return nil, err
	}
	return b, nil
}

// Add validates the link, dedupes by root (updating the label), and
// persists. Returns the file's root hex for the UI.
func (b *Book) Add(rawLink, label string) (string, error) {
	h, err := link.Parse(rawLink)
	if err != nil {
		return "", err
	}
	root := h.Root.String()
	b.mu.Lock()
	defer b.mu.Unlock()
	for i := range b.items {
		if lh, err := link.Parse(b.items[i].Link); err == nil && lh.Root == h.Root {
			b.items[i].Label = label
			b.items[i].Link = rawLink
			return root, b.save()
		}
	}
	b.items = append([]Item{{Link: rawLink, Label: label, Added: time.Now().Unix()}}, b.items...)
	return root, b.save()
}

// Remove drops the link whose root matches (hex).
func (b *Book) Remove(rootHex string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	kept := b.items[:0]
	for _, it := range b.items {
		if lh, err := link.Parse(it.Link); err == nil && lh.Root.String() == rootHex {
			continue
		}
		kept = append(kept, it)
	}
	b.items = kept
	return b.save()
}

func (b *Book) Items() []Item {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]Item, len(b.items))
	copy(out, b.items)
	return out
}

// save assumes the lock is held. Atomic replace.
func (b *Book) save() error {
	raw, err := json.MarshalIndent(b.items, "", "  ")
	if err != nil {
		return err
	}
	tmp := b.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, b.path)
}
