// Package denylist is Silt's takedown mechanism. It answers a hard
// question honestly: on an append-only chain, a published identifier can
// never be un-recorded — so how does illegal content get removed?
//
// The answer is that removal happens at the AVAILABILITY layer, not the
// ledger layer. The chain keeps its immutable record of "root X was
// published" (an opaque 32-byte hash — a fingerprint, not content), but
// compliant nodes NO-OP on a denied root: they refuse to store, serve,
// announce, or repair its chunks, and a chain-backed registry refuses to
// resolve it. The file's chunks stop being hosted and repaired, so its
// redundancy decays and it rots off the network — even though its name
// persists in history. Deletion of the RECORD is neither possible nor
// necessary; what matters is that the CONTENT becomes unreachable.
//
// Crucially this never touches encryption. A denied root is matched by
// its hash; nothing is decrypted, and infrastructure stays content-blind.
//
// A Set is populated two ways (see docs/safety-denylist.md): from
// on-chain revocation records (append-only tombstones committed by the
// same reputation quorum that commits publications, so takedown
// replicates and is tamper-evident like everything else), and from an
// operator's local file (a jurisdiction's list, a trusted blocklist).
// It is deliberately not a single global authority — operators choose
// which lists to honor, the way DNS and mail operators choose blocklists.
package denylist

import (
	"bufio"
	"encoding/hex"
	"io"
	"strings"
	"sync"

	"github.com/nerolabs/silt/ports"
)

// Set is a thread-safe collection of denied root hashes.
type Set struct {
	mu sync.RWMutex
	m  map[ports.Hash]bool
}

func New() *Set { return &Set{m: make(map[ports.Hash]bool)} }

func (s *Set) Add(root ports.Hash) {
	s.mu.Lock()
	s.m[root] = true
	s.mu.Unlock()
}

// Denied reports whether root is on the list. Nil-safe: a nil Set denies
// nothing, so callers can hold an optional *Set without guarding.
func (s *Set) Denied(root ports.Hash) bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.m[root]
}

func (s *Set) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.m)
}

// Roots returns the denied roots (unordered).
func (s *Set) Roots() []ports.Hash {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]ports.Hash, 0, len(s.m))
	for r := range s.m {
		out = append(out, r)
	}
	return out
}

// LoadInto reads hex root hashes (one per line; blank lines and lines
// beginning with '#' are ignored) and adds them to s. Unparseable lines
// are skipped so one bad line can't disarm the whole list.
func LoadInto(s *Set, r io.Reader) error {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		b, err := hex.DecodeString(line)
		if err != nil || len(b) != len(ports.Hash{}) {
			continue
		}
		var h ports.Hash
		copy(h[:], b)
		s.Add(h)
	}
	return sc.Err()
}
