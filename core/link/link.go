// Package link defines the silt link — the share handle for a file —
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
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/nerolabs/silt/core/crypto"
	"github.com/nerolabs/silt/ports"
)

const (
	fullPrefix = "silt:v1:"
	carePrefix = "siltcare:v1:"

	layoutDomain  = "silt/link/v1/layout"
	contentDomain = "silt/link/v1/content"
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
	return fullPrefix + encodePart(h.Root[:]) + ":" + encodePart(h.Key[:])
}

func (c CareHandle) String() string {
	return carePrefix + encodePart(c.Root[:]) + ":" + encodePart(c.LayoutKey[:])
}

// Links encode their two 32-byte values as unpadded base64url — 43 chars
// each, versus 64 for hex — so a share link is ~30% shorter and easier to
// copy by hand. base64url has no ':' so it never collides with the
// separators, and no '/' or '+' so it's URL- and shell-safe.
func encodePart(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// decodePart accepts the compact base64url form and, for links minted by
// earlier builds, the 64-char hex form too.
func decodePart(s string) ([32]byte, error) {
	var out [32]byte
	if len(s) == 64 {
		if h, err := ports.ParseHash(s); err == nil {
			return h, nil
		}
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || len(raw) != 32 {
		return out, fmt.Errorf("expected a 32-byte base64url value, got %q", s)
	}
	copy(out[:], raw)
	return out, nil
}

// Parse reads a full link.
func Parse(s string) (Handle, error) {
	s = strings.TrimSpace(s)
	body, ok := strings.CutPrefix(s, fullPrefix)
	if !ok {
		// A care link is a valid link, just the wrong capability for fetching —
		// say so, rather than "not a link" (which reads like a typo).
		if strings.HasPrefix(s, carePrefix) {
			return Handle{}, fmt.Errorf("link: this is a %s care link — repair/audit only, it cannot decrypt content; fetching the bytes needs the full %s link", carePrefix, fullPrefix)
		}
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
	var root ports.Hash
	var key [32]byte
	parts := strings.Split(body, ":")
	if len(parts) != 2 {
		return root, key, fmt.Errorf("link: want ROOT:KEY")
	}
	r, err := decodePart(parts[0])
	if err != nil {
		return root, key, fmt.Errorf("link: root: %w", err)
	}
	k, err := decodePart(parts[1])
	if err != nil {
		return root, key, fmt.Errorf("link: key: %w", err)
	}
	copy(root[:], r[:])
	copy(key[:], k[:])
	return root, key, nil
}
