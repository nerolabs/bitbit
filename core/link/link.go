// Package link defines the bitbit link — the share handle for a file —
// and its one-way key hierarchy:
//
//	link key  ──HKDF──►  layout key  (decrypts stripe structure)
//	    │
//	    └─────HKDF──►  content key  (decrypts the file's key material)
//
// Holding the FULL link (root + link key) means you can derive both:
// read the layout, decrypt the content. Holding only a CARE link
// (root + layout key) means you can see which shards form which
// stripes — enough to repair and audit a file forever — but the
// content keys, and therefore the bytes, stay sealed. HKDF is one-way:
// no amount of layout access climbs back up to the content.
//
// Infrastructure holds neither. To a daemon, a chunk is noise and a
// manifest is noise-about-noise; a link is something users exchange
// out of band (or via a resolver layer like Aslan).
package link

import (
	"fmt"
	"strings"

	"github.com/nerolabs/bitbit/core/crypto"
	"github.com/nerolabs/bitbit/ports"
)

const (
	fullPrefix = "bitbit:v1:"
	carePrefix = "bitbitcare:v1:"

	layoutDomain  = "bitbit/link/v1/layout"
	contentDomain = "bitbit/link/v1/content"
)

// Handle is the full capability: retrieve and decrypt.
type Handle struct {
	Root ports.Hash
	Key  [32]byte // the link key; layout and content keys derive from it
}

// CareHandle is the maintenance capability: repair and audit, no
// decryption.
type CareHandle struct {
	Root      ports.Hash
	LayoutKey [32]byte
}

func (h Handle) LayoutKey() [32]byte  { return crypto.DeriveKey(h.Key, layoutDomain) }
func (h Handle) ContentKey() [32]byte { return crypto.DeriveKey(h.Key, contentDomain) }

// Care degrades a full handle to a care handle — the direction that
// works. There is no way back.
func (h Handle) Care() CareHandle {
	return CareHandle{Root: h.Root, LayoutKey: h.LayoutKey()}
}

func (h Handle) String() string {
	return fmt.Sprintf("%s%s:%x", fullPrefix, h.Root, h.Key)
}

func (c CareHandle) String() string {
	return fmt.Sprintf("%s%s:%x", carePrefix, c.Root, c.LayoutKey)
}

// Parse reads a full link.
func Parse(s string) (Handle, error) {
	body, ok := strings.CutPrefix(strings.TrimSpace(s), fullPrefix)
	if !ok {
		return Handle{}, fmt.Errorf("link: not a %s link", fullPrefix)
	}
	root, key, err := splitBody(body)
	if err != nil {
		return Handle{}, err
	}
	return Handle{Root: root, Key: key}, nil
}

// ParseCare reads a care link.
func ParseCare(s string) (CareHandle, error) {
	body, ok := strings.CutPrefix(strings.TrimSpace(s), carePrefix)
	if !ok {
		return CareHandle{}, fmt.Errorf("link: not a %s link", carePrefix)
	}
	root, key, err := splitBody(body)
	if err != nil {
		return CareHandle{}, err
	}
	return CareHandle{Root: root, LayoutKey: key}, nil
}

// ParseAnyCare accepts either link form and returns the care
// capability (a full link degrades; a care link is itself).
func ParseAnyCare(s string) (CareHandle, error) {
	if h, err := Parse(s); err == nil {
		return h.Care(), nil
	}
	return ParseCare(s)
}

func splitBody(body string) (ports.Hash, [32]byte, error) {
	var key [32]byte
	parts := strings.Split(body, ":")
	if len(parts) != 2 {
		return ports.Hash{}, key, fmt.Errorf("link: want ROOT:KEY")
	}
	root, err := ports.ParseHash(parts[0])
	if err != nil {
		return ports.Hash{}, key, fmt.Errorf("link: root: %w", err)
	}
	k, err := ports.ParseHash(parts[1]) // same 32-byte hex shape
	if err != nil {
		return ports.Hash{}, key, fmt.Errorf("link: key: %w", err)
	}
	copy(key[:], k[:])
	return root, key, nil
}
