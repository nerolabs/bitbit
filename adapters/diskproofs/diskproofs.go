// Package diskproofs persists a ports.StorageProof per hosted chunk as a
// small CBOR sidecar, mirroring the diskstore's object layout:
//
//	<root>/<first two hex chars>/<full hex chunk id>
//
// It is the durable half of the #69 fix: after a restart the node reloads
// these proofs so it can re-announce each coded shard under the right column
// key (and still answer storage-audit challenges). Writes are atomic
// (temp file + rename), like the object store.
package diskproofs

import (
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/fxamacker/cbor/v2"
	"github.com/nerolabs/silt/ports"
)

type Store struct{ root string }

var _ ports.ProofStore = (*Store)(nil)

// Open roots the proof tree at dir (created if absent).
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("diskproofs: %w", err)
	}
	return &Store{root: dir}, nil
}

// diskProof is the on-disk shape: hashes as byte strings (compact), fields
// keyed by int so the encoding is stable and small.
type diskProof struct {
	Root    []byte   `cbor:"1,keyasint"`
	Index   int      `cbor:"2,keyasint"`
	Total   int      `cbor:"3,keyasint"`
	Path    [][]byte `cbor:"4,keyasint"`
	Column  int      `cbor:"5,keyasint"`
	PorTags [][]byte `cbor:"6,keyasint,omitempty"`
}

func (s *Store) path(id ports.ChunkID) string {
	h := hex.EncodeToString(id[:])
	return filepath.Join(s.root, h[:2], h)
}

func (s *Store) Put(id ports.ChunkID, p ports.StorageProof) error {
	dp := diskProof{
		Root:   append([]byte(nil), p.Root[:]...),
		Index:  p.Index,
		Total:  p.Total,
		Column: p.Column,
	}
	for _, h := range p.Path {
		dp.Path = append(dp.Path, append([]byte(nil), h[:]...))
	}
	// PoR authenticators must survive a restart: without them a re-announced
	// shard can't answer an audit and its honest host is wrongly slashed (#69
	// / Gate 4a). One 32-byte tag per por-block.
	for _, tag := range p.PorTags {
		dp.PorTags = append(dp.PorTags, append([]byte(nil), tag...))
	}
	b, err := cbor.Marshal(dp)
	if err != nil {
		return fmt.Errorf("diskproofs marshal %s: %w", id, err)
	}
	dst := s.path(id)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), dst)
}

func (s *Store) Delete(id ports.ChunkID) error {
	err := os.Remove(s.path(id))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// Load reads every sidecar back into a map. A single unreadable/corrupt proof
// is skipped, not fatal — the chunk just re-announces under its bare id until
// re-hosted, which is strictly better than refusing to start.
func (s *Store) Load() (map[ports.ChunkID]ports.StorageProof, error) {
	out := make(map[ports.ChunkID]ports.StorageProof)
	err := filepath.WalkDir(s.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		name := d.Name()
		if len(name) != 64 { // skip stray temp files / non-proof entries
			return nil
		}
		raw, herr := hex.DecodeString(name)
		if herr != nil || len(raw) != 32 {
			return nil
		}
		var id ports.ChunkID
		copy(id[:], raw)
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		var dp diskProof
		if cbor.Unmarshal(b, &dp) != nil || len(dp.Root) != 32 {
			return nil
		}
		p := ports.StorageProof{Index: dp.Index, Total: dp.Total, Column: dp.Column}
		copy(p.Root[:], dp.Root)
		for _, hb := range dp.Path {
			if len(hb) != 32 {
				continue
			}
			var h ports.Hash
			copy(h[:], hb)
			p.Path = append(p.Path, h)
		}
		for _, tag := range dp.PorTags {
			p.PorTags = append(p.PorTags, append([]byte(nil), tag...))
		}
		out[id] = p
		return nil
	})
	if err != nil {
		return out, fmt.Errorf("diskproofs load: %w", err)
	}
	return out, nil
}
