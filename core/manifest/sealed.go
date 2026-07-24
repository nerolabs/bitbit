// Sealed manifests (M11): what gets stored in the swarm is ciphertext
// twice over.
//
//	blob = Seal_layoutKey( layout ‖ Seal_contentKey(secrets) )
//
// The outer layer hides the stripe structure from infrastructure; the
// inner box hides the decryption material from caretakers. Opening with
// only the layout key yields a Layout — everything repair and audit
// need, nothing a reader wants. Opening with both keys yields the full
// Manifest. Keys are supplied by the caller (core/link derives them
// from the bitbit link); this package just does the boxing.
package manifest

import (
	"fmt"

	"github.com/nerolabs/bitbit/core/crypto"
	"github.com/nerolabs/bitbit/ports"
)

const (
	layoutDomain  = "bitbit/manifest/v1/layout"
	secretsDomain = "bitbit/manifest/v1/secrets"
)

// Layout is the repair-grade view of a file: which chunks, which
// stripes, what code — and no way to read any of it.
type Layout struct {
	Version   int      `cbor:"1,keyasint"`
	ChunkSize int64    `cbor:"2,keyasint"`
	K         int      `cbor:"3,keyasint,omitempty"`
	N         int      `cbor:"4,keyasint,omitempty"`
	Chunks    [][]byte `cbor:"5,keyasint"`
	Parity    [][]byte `cbor:"6,keyasint,omitempty"`
	// Box is the sealed secrets (opaque without the content key).
	Box []byte `cbor:"7,keyasint"`
}

type secretsPart struct {
	Mode         string   `cbor:"1,keyasint"`
	FileSize     int64    `cbor:"2,keyasint"`
	ChunkSecrets [][]byte `cbor:"3,keyasint,omitempty"`
	FileKey      []byte   `cbor:"4,keyasint,omitempty"`
}

func (l *Layout) ChunkIDs() []ports.ChunkID  { return toHashes(l.Chunks) }
func (l *Layout) ParityIDs() []ports.ChunkID { return toHashes(l.Parity) }
func (l *Layout) Leaves() []ports.Hash       { return append(l.ChunkIDs(), l.ParityIDs()...) }
func (l *Layout) Root() ports.Hash           { return MerkleRoot(l.Leaves()) }

// Seal encrypts m into the storable blob under the two derived keys.
func Seal(m *Manifest, layoutKey, contentKey [32]byte) ([]byte, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	sb, err := encMode.Marshal(secretsPart{
		Mode: m.Mode, FileSize: m.FileSize,
		ChunkSecrets: m.ChunkSecrets, FileKey: m.FileKey,
	})
	if err != nil {
		return nil, err
	}
	box, err := crypto.SealBox(contentKey, secretsDomain, sb)
	if err != nil {
		return nil, err
	}
	eb, err := encMode.Marshal(Layout{
		Version: m.Version, ChunkSize: m.ChunkSize,
		K: m.K, N: m.N, Chunks: m.Chunks, Parity: m.Parity,
		Box: box,
	})
	if err != nil {
		return nil, err
	}
	return crypto.SealBox(layoutKey, layoutDomain, eb)
}

// OpenLayout decrypts only the outer layer: the caretaker's view.
func OpenLayout(blob []byte, layoutKey [32]byte) (*Layout, error) {
	eb, err := crypto.OpenBox(layoutKey, layoutDomain, blob)
	if err != nil {
		return nil, fmt.Errorf("manifest: layout: %w", err)
	}
	var l Layout
	if err := cborUnmarshal(eb, &l); err != nil {
		return nil, fmt.Errorf("manifest: layout: %w", err)
	}
	if l.Version != Version {
		return nil, fmt.Errorf("manifest: unsupported version %d", l.Version)
	}
	return &l, nil
}

// OpenFull decrypts both layers: the reader's view.
func OpenFull(blob []byte, layoutKey, contentKey [32]byte) (*Manifest, error) {
	l, err := OpenLayout(blob, layoutKey)
	if err != nil {
		return nil, err
	}
	sb, err := crypto.OpenBox(contentKey, secretsDomain, l.Box)
	if err != nil {
		return nil, fmt.Errorf("manifest: secrets: %w", err)
	}
	var s secretsPart
	if err := cborUnmarshal(sb, &s); err != nil {
		return nil, fmt.Errorf("manifest: secrets: %w", err)
	}
	m := &Manifest{
		Version: l.Version, ChunkSize: l.ChunkSize, K: l.K, N: l.N,
		Chunks: l.Chunks, Parity: l.Parity,
		Mode: s.Mode, FileSize: s.FileSize,
		ChunkSecrets: s.ChunkSecrets, FileKey: s.FileKey,
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return m, nil
}
