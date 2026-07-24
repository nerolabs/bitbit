// Package manifest defines the file manifest: everything needed to
// rebuild a file from chunks, plus the Merkle tree that gives the file
// its global identity.
//
// Canonical bytes matter here. The manifest is serialized, chunked, and
// stored like any other data, and its Merkle root must be reproducible
// by anyone from the same logical content — so we serialize with CBOR in
// deterministic (canonical) encoding mode: fixed field order, shortest
// integer forms, no floating point. Marshal(Unmarshal(b)) == b, always.
package manifest

import (
	"fmt"

	"github.com/fxamacker/cbor/v2"

	"shardnet/core/crypto"
	"shardnet/ports"
)

const Version = 1

// Manifest describes one stored file.
//
// Erasure-coding fields (K, N, stripe layout) land in M2 — K=N=0 means
// "no coding, chunks are stored directly". ManifestKey is reserved for
// v2 encrypted manifests and must stay empty for now.
type Manifest struct {
	Version   int    `cbor:"1,keyasint"`
	Mode      string `cbor:"2,keyasint"` // crypto.Mode
	ChunkSize int64  `cbor:"3,keyasint"` // frame size used at split time
	FileSize  int64  `cbor:"4,keyasint"`
	// Chunks lists ciphertext chunk IDs in file order. The Merkle root
	// over this list is the file's identity.
	Chunks [][]byte `cbor:"5,keyasint"`
	// ChunkSecrets holds the per-chunk convergent secrets (convergent
	// mode only), aligned with Chunks. Whoever has the manifest can
	// decrypt; whoever only has chunks cannot.
	ChunkSecrets [][]byte `cbor:"6,keyasint,omitempty"`
	// FileKey is the per-file key (private mode only). Plaintext in v1 —
	// acceptable in the sim, revisit with encrypted manifests.
	FileKey []byte `cbor:"7,keyasint,omitempty"`
	// Erasure-coding parameters, reserved until M2.
	K int `cbor:"8,keyasint,omitempty"`
	N int `cbor:"9,keyasint,omitempty"`
	// ManifestKey is reserved for v2 manifest encryption.
	ManifestKey []byte `cbor:"10,keyasint,omitempty"`
}

var encMode cbor.EncMode

func init() {
	var err error
	encMode, err = cbor.CanonicalEncOptions().EncMode()
	if err != nil {
		panic(err)
	}
}

// Marshal serializes to canonical CBOR bytes.
func (m *Manifest) Marshal() ([]byte, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return encMode.Marshal(m)
}

// Unmarshal parses and validates manifest bytes.
func Unmarshal(b []byte) (*Manifest, error) {
	var m Manifest
	if err := cbor.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("manifest: decode: %w", err)
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

// Validate enforces internal consistency; every load and store path runs
// through it so a malformed manifest fails loudly, early.
func (m *Manifest) Validate() error {
	if m.Version != Version {
		return fmt.Errorf("manifest: unsupported version %d", m.Version)
	}
	mode, err := crypto.ParseMode(m.Mode)
	if err != nil {
		return fmt.Errorf("manifest: %w", err)
	}
	if m.ChunkSize <= 0 {
		return fmt.Errorf("manifest: non-positive chunk size %d", m.ChunkSize)
	}
	if m.FileSize < 0 {
		return fmt.Errorf("manifest: negative file size")
	}
	for i, c := range m.Chunks {
		if len(c) != len(ports.Hash{}) {
			return fmt.Errorf("manifest: chunk %d has ID length %d", i, len(c))
		}
	}
	switch mode {
	case crypto.Convergent:
		if len(m.ChunkSecrets) != len(m.Chunks) {
			return fmt.Errorf("manifest: %d chunks but %d convergent secrets", len(m.Chunks), len(m.ChunkSecrets))
		}
		for i, s := range m.ChunkSecrets {
			if len(s) != crypto.SecretSize {
				return fmt.Errorf("manifest: secret %d has length %d", i, len(s))
			}
		}
		if len(m.FileKey) != 0 {
			return fmt.Errorf("manifest: convergent manifest must not carry a file key")
		}
	case crypto.Private:
		if len(m.FileKey) != crypto.KeySize {
			return fmt.Errorf("manifest: private manifest needs a %d-byte file key", crypto.KeySize)
		}
		if len(m.ChunkSecrets) != 0 {
			return fmt.Errorf("manifest: private manifest must not carry convergent secrets")
		}
	}
	if len(m.ManifestKey) != 0 {
		return fmt.Errorf("manifest: encrypted manifests are not supported yet")
	}
	return nil
}

// ChunkIDs returns the chunk list as typed hashes.
func (m *Manifest) ChunkIDs() []ports.ChunkID {
	ids := make([]ports.ChunkID, len(m.Chunks))
	for i, c := range m.Chunks {
		copy(ids[i][:], c)
	}
	return ids
}

// Root computes the file's Merkle root from the manifest's chunk list.
func (m *Manifest) Root() ports.Hash {
	return MerkleRoot(m.ChunkIDs())
}
