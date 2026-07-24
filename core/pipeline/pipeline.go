// Package pipeline is the M1 roundtrip: the full path from a byte stream
// to a published Merkle root and back.
//
//	Add: split → encrypt per chunk → hash → store → manifest → store
//	     manifest as chunks → publish root
//	Get: lookup root → fetch+verify manifest chunks → parse → verify the
//	     chunk list against the root → fetch+verify data chunks → decrypt
//	     → join → size check
//
// Everything speaks through ports; the pipeline neither knows nor cares
// whether the store is a map, a directory, or (later) a swarm of peers.
package pipeline

import (
	"bytes"
	"context"
	"fmt"
	"io"

	"shardnet/core/chunk"
	"shardnet/core/crypto"
	"shardnet/core/manifest"
	"shardnet/ports"
)

// DefaultChunkSize is 64 KiB for the sim (64 MiB is the production
// number; small chunks keep tests and demos fast).
const DefaultChunkSize = 64 << 10

type Options struct {
	ChunkSize int
	Mode      crypto.Mode
	// Rand supplies key material for private mode. Injected, never
	// defaulted inside core: determinism under test is non-negotiable.
	Rand io.Reader
}

// Add ingests r and returns the file's Merkle root.
func Add(ctx context.Context, store ports.ChunkStore, reg ports.Registry, r io.Reader, opts Options) (ports.Hash, error) {
	if opts.ChunkSize == 0 {
		opts.ChunkSize = DefaultChunkSize
	}
	frames, err := chunk.Split(r, opts.ChunkSize)
	if err != nil {
		return ports.Hash{}, fmt.Errorf("add: %w", err)
	}

	m := &manifest.Manifest{
		Version:   manifest.Version,
		Mode:      string(opts.Mode),
		ChunkSize: int64(opts.ChunkSize),
		FileSize:  fileSizeOf(frames),
	}

	var fileKey [crypto.KeySize]byte
	if opts.Mode == crypto.Private {
		if opts.Rand == nil {
			return ports.Hash{}, fmt.Errorf("add: private mode requires an injected randomness source")
		}
		fileKey, err = crypto.NewFileKey(opts.Rand)
		if err != nil {
			return ports.Hash{}, err
		}
		m.FileKey = fileKey[:]
	}

	for i, frame := range frames {
		var ct []byte
		switch opts.Mode {
		case crypto.Convergent:
			var secret [crypto.SecretSize]byte
			ct, secret, err = crypto.ConvergentEncrypt(frame)
			if err != nil {
				return ports.Hash{}, fmt.Errorf("add: chunk %d: %w", i, err)
			}
			m.ChunkSecrets = append(m.ChunkSecrets, secret[:])
		case crypto.Private:
			ct, err = crypto.PrivateEncrypt(fileKey, uint64(i), frame)
			if err != nil {
				return ports.Hash{}, fmt.Errorf("add: chunk %d: %w", i, err)
			}
		default:
			return ports.Hash{}, fmt.Errorf("add: unknown mode %q", opts.Mode)
		}
		c := ports.NewChunk(ct)
		if err := store.Put(ctx, c); err != nil {
			return ports.Hash{}, fmt.Errorf("add: storing chunk %d: %w", i, err)
		}
		m.Chunks = append(m.Chunks, c.ID[:])
	}

	root := m.Root()

	// The manifest travels the same road as data: serialized, chunked,
	// stored. Its chunks are plain (no encryption) in v1.
	mbytes, err := m.Marshal()
	if err != nil {
		return ports.Hash{}, fmt.Errorf("add: %w", err)
	}
	mframes, err := chunk.Split(bytes.NewReader(mbytes), opts.ChunkSize)
	if err != nil {
		return ports.Hash{}, fmt.Errorf("add: chunking manifest: %w", err)
	}
	var manifestIDs []ports.ChunkID
	for i, f := range mframes {
		c := ports.NewChunk(f)
		if err := store.Put(ctx, c); err != nil {
			return ports.Hash{}, fmt.Errorf("add: storing manifest chunk %d: %w", i, err)
		}
		manifestIDs = append(manifestIDs, c.ID)
	}

	err = reg.Publish(ctx, ports.Entry{
		Root:           root,
		ManifestChunks: manifestIDs,
		FileSize:       m.FileSize,
	})
	if err != nil {
		return ports.Hash{}, fmt.Errorf("add: publish: %w", err)
	}
	return root, nil
}

// Get retrieves the file named by root and writes it to w. Every chunk
// is hash-verified on receipt, and the manifest's chunk list is verified
// against the root before any data is fetched — the registry entry
// itself is treated as untrusted routing metadata.
func Get(ctx context.Context, store ports.ChunkStore, reg ports.Registry, root ports.Hash, w io.Writer) error {
	entry, ok, err := reg.Lookup(ctx, root)
	if err != nil {
		return fmt.Errorf("get: %w", err)
	}
	if !ok {
		return fmt.Errorf("get %s: %w", root, ports.ErrNoSuchEntry)
	}

	var mbuf bytes.Buffer
	mframes := make([][]byte, 0, len(entry.ManifestChunks))
	for _, id := range entry.ManifestChunks {
		c, err := store.Get(ctx, id)
		if err != nil {
			return fmt.Errorf("get: manifest chunk: %w", err)
		}
		if !c.Verify() {
			return fmt.Errorf("get: manifest chunk %s: %w", id, ports.ErrCorrupt)
		}
		mframes = append(mframes, c.Data)
	}
	if err := chunk.Join(&mbuf, mframes); err != nil {
		return fmt.Errorf("get: reassembling manifest: %w", err)
	}
	m, err := manifest.Unmarshal(mbuf.Bytes())
	if err != nil {
		return fmt.Errorf("get: %w", err)
	}
	// The critical check: the manifest we fetched must actually be the
	// one the root names. A registry (or store) pointing us at a
	// different manifest is caught right here.
	if m.Root() != root {
		return fmt.Errorf("get: manifest root %s does not match requested root %s", m.Root(), root)
	}

	mode, err := crypto.ParseMode(m.Mode)
	if err != nil {
		return fmt.Errorf("get: %w", err)
	}
	var fileKey [crypto.KeySize]byte
	if mode == crypto.Private {
		copy(fileKey[:], m.FileKey)
	}

	var written int64
	frames := make([][]byte, 0, len(m.Chunks))
	for i, id := range m.ChunkIDs() {
		c, err := store.Get(ctx, id)
		if err != nil {
			return fmt.Errorf("get: chunk %d: %w", i, err)
		}
		if !c.Verify() {
			return fmt.Errorf("get: chunk %d (%s): %w", i, id, ports.ErrCorrupt)
		}
		var pt []byte
		switch mode {
		case crypto.Convergent:
			var secret [crypto.SecretSize]byte
			copy(secret[:], m.ChunkSecrets[i])
			pt, err = crypto.ConvergentDecrypt(c.Data, secret)
		case crypto.Private:
			pt, err = crypto.PrivateDecrypt(fileKey, uint64(i), c.Data)
		}
		if err != nil {
			return fmt.Errorf("get: chunk %d: %w", i, err)
		}
		frames = append(frames, pt)
	}
	counter := &countingWriter{w: w}
	if err := chunk.Join(counter, frames); err != nil {
		return fmt.Errorf("get: %w", err)
	}
	written = counter.n
	if written != m.FileSize {
		return fmt.Errorf("get: reassembled %d bytes, manifest says %d", written, m.FileSize)
	}
	return nil
}

func fileSizeOf(frames [][]byte) int64 {
	var total int64
	for _, f := range frames {
		total += int64(frameLen(f))
	}
	return total
}

func frameLen(f []byte) int {
	n := 0
	for i := 0; i < chunk.HeaderSize; i++ {
		n = n<<8 | int(f[i])
	}
	return n
}

type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}
