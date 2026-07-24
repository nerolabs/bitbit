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
	"shardnet/core/erasure"
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
	// Erasure-coding parameters: stripes of K consecutive chunks carry
	// N-K parity shards each. K=0 means no coding (an M1 manifest).
	K int `cbor:"8,keyasint,omitempty"`
	N int `cbor:"9,keyasint,omitempty"`
	// ManifestKey is reserved for v2 manifest encryption.
	ManifestKey []byte `cbor:"10,keyasint,omitempty"`
	// Parity lists parity shard IDs in stripe order: stripe j owns
	// Parity[j*(N-K) : (j+1)*(N-K)]. A short final stripe is padded with
	// implicit zero shards during encoding (see core/erasure), so every
	// stripe has exactly N-K parity entries.
	Parity [][]byte `cbor:"11,keyasint,omitempty"`
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
	if m.K == 0 {
		if m.N != 0 || len(m.Parity) != 0 {
			return fmt.Errorf("manifest: parity present but k=0 (uncoded)")
		}
		return nil
	}
	p := erasure.Params{K: m.K, N: m.N}
	if err := p.Validate(); err != nil {
		return fmt.Errorf("manifest: %w", err)
	}
	wantParity := 0
	if len(m.Chunks) > 0 {
		wantParity = p.Stripes(len(m.Chunks)) * p.ParityShards()
	}
	if len(m.Parity) != wantParity {
		return fmt.Errorf("manifest: %d parity shards for %d chunks under (k=%d,n=%d), want %d",
			len(m.Parity), len(m.Chunks), m.K, m.N, wantParity)
	}
	for i, id := range m.Parity {
		if len(id) != len(ports.Hash{}) {
			return fmt.Errorf("manifest: parity %d has ID length %d", i, len(id))
		}
	}
	return nil
}

// ChunkIDs returns the data chunk list as typed hashes.
func (m *Manifest) ChunkIDs() []ports.ChunkID {
	return toHashes(m.Chunks)
}

// ParityIDs returns the parity shard list as typed hashes.
func (m *Manifest) ParityIDs() []ports.ChunkID {
	return toHashes(m.Parity)
}

// Leaves is the Merkle leaf order: all data chunk IDs in file order,
// then all parity IDs in stripe order. The root therefore commits to
// every shard of the file, so inclusion proofs work for parity too —
// which the future proof-of-retrieval seam needs.
func (m *Manifest) Leaves() []ports.Hash {
	return append(m.ChunkIDs(), m.ParityIDs()...)
}

// Root computes the file's Merkle root over all shard hashes.
func (m *Manifest) Root() ports.Hash {
	return MerkleRoot(m.Leaves())
}

func toHashes(raw [][]byte) []ports.Hash {
	ids := make([]ports.Hash, len(raw))
	for i, b := range raw {
		copy(ids[i][:], b)
	}
	return ids
}
