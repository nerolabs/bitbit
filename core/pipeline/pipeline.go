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

	"github.com/nerolabs/silt/core/chunk"
	"github.com/nerolabs/silt/core/crypto"
	"github.com/nerolabs/silt/core/erasure"
	"github.com/nerolabs/silt/core/link"
	"github.com/nerolabs/silt/core/manifest"
	"github.com/nerolabs/silt/ports"
)

// DefaultChunkSize is 64 KiB for the sim; 64 MiB is the minimum production
// chunk size (small chunks keep tests and demos fast). The manifest layer
// bounds the maximum at manifest.MaxChunkSize.
const DefaultChunkSize = 64 << 10

type Options struct {
	ChunkSize int
	Mode      crypto.Mode
	// Erasure is the (k, n) code; zero value means DefaultParams.
	Erasure erasure.Params
	// Rand supplies key material for private mode. Injected, never
	// defaulted inside core: determinism under test is non-negotiable.
	Rand io.Reader
	// Publisher is recorded in the registry entry; required when the
	// registry is credit-gated, ignored otherwise.
	Publisher ports.NodeID
	// Token, when set, is a quorum-issued publish credential that authorizes
	// the entry WITHOUT a durable Publisher identity (T3, #14/F1). Acquire it
	// (node.AcquireToken) before calling Add and pass it here; the entry then
	// carries the token instead of a publisher.
	Token *ports.PublishToken
}

// Add ingests r and returns the file's silt link: the Merkle root
// (its public name) plus the link key (the private capability). The link
// key is the hash of the plaintext manifest, so convergent content
// yields the same link every time — dedup extends all the way up to the
// handle you share.
func Add(ctx context.Context, store ports.ChunkStore, reg ports.Registry, r io.Reader, opts Options) (link.Handle, error) {
	if opts.ChunkSize == 0 {
		opts.ChunkSize = DefaultChunkSize
	}
	if opts.Erasure == (erasure.Params{}) {
		opts.Erasure = erasure.DefaultParams
	}
	if err := opts.Erasure.Validate(); err != nil {
		return link.Handle{}, fmt.Errorf("add: %w", err)
	}
	frames, err := chunk.Split(r, opts.ChunkSize)
	if err != nil {
		return link.Handle{}, fmt.Errorf("add: %w", err)
	}

	m := &manifest.Manifest{
		Version:   manifest.Version,
		Mode:      string(opts.Mode),
		ChunkSize: int64(opts.ChunkSize),
		FileSize:  fileSizeOf(frames),
		K:         opts.Erasure.K,
		N:         opts.Erasure.N,
	}

	var fileKey [crypto.KeySize]byte
	if opts.Mode == crypto.Private {
		if opts.Rand == nil {
			return link.Handle{}, fmt.Errorf("add: private mode requires an injected randomness source")
		}
		fileKey, err = crypto.NewFileKey(opts.Rand)
		if err != nil {
			return link.Handle{}, err
		}
		m.FileKey = fileKey[:]
	}

	ctChunks := make([][]byte, 0, len(frames))
	for i, frame := range frames {
		var ct []byte
		switch opts.Mode {
		case crypto.Convergent:
			var secret [crypto.SecretSize]byte
			ct, secret, err = crypto.ConvergentEncrypt(frame)
			if err != nil {
				return link.Handle{}, fmt.Errorf("add: chunk %d: %w", i, err)
			}
			m.ChunkSecrets = append(m.ChunkSecrets, secret[:])
		case crypto.Private:
			ct, err = crypto.PrivateEncrypt(fileKey, uint64(i), frame)
			if err != nil {
				return link.Handle{}, fmt.Errorf("add: chunk %d: %w", i, err)
			}
		default:
			return link.Handle{}, fmt.Errorf("add: unknown mode %q", opts.Mode)
		}
		c := ports.NewChunk(ct)
		if err := store.Put(ctx, c); err != nil {
			return link.Handle{}, fmt.Errorf("add: storing chunk %d: %w", i, err)
		}
		m.Chunks = append(m.Chunks, c.ID[:])
		ctChunks = append(ctChunks, ct)
	}

	// Erasure-code the ciphertext stream: each stripe of k chunks gains
	// n-k parity shards, stored like any other chunk.
	p := opts.Erasure
	for j := 0; j < p.Stripes(len(ctChunks)); j++ {
		lo := j * p.K
		hi := min(lo+p.K, len(ctChunks))
		parity, err := erasure.EncodeStripe(p, ctChunks[lo:hi])
		if err != nil {
			return link.Handle{}, fmt.Errorf("add: encoding stripe %d: %w", j, err)
		}
		for q, shard := range parity {
			c := ports.NewChunk(shard)
			if err := store.Put(ctx, c); err != nil {
				return link.Handle{}, fmt.Errorf("add: storing parity %d of stripe %d: %w", q, j, err)
			}
			m.Parity = append(m.Parity, c.ID[:])
		}
	}

	root := m.Root()

	// The manifest is sealed before it touches storage: the link key is
	// the hash of the plaintext manifest (deterministic, content-bound),
	// and the stored blob is ciphertext twice over — layout under the
	// layout key, decryption material boxed under the content key.
	// Infrastructure hosts noise describing noise.
	mbytes, err := m.Marshal()
	if err != nil {
		return link.Handle{}, fmt.Errorf("add: %w", err)
	}
	h := link.Handle{Root: root, Key: ports.HashBytes(mbytes)}
	blob, err := manifest.Seal(m, h.LayoutKey(), h.ContentKey())
	if err != nil {
		return link.Handle{}, fmt.Errorf("add: sealing manifest: %w", err)
	}
	mframes, err := chunk.Split(bytes.NewReader(blob), opts.ChunkSize)
	if err != nil {
		return link.Handle{}, fmt.Errorf("add: chunking manifest: %w", err)
	}
	var manifestIDs []ports.ChunkID
	for i, f := range mframes {
		c := ports.NewChunk(f)
		if err := store.Put(ctx, c); err != nil {
			return link.Handle{}, fmt.Errorf("add: storing manifest chunk %d: %w", i, err)
		}
		manifestIDs = append(manifestIDs, c.ID)
	}

	err = reg.Publish(ctx, ports.Entry{
		Root:           root,
		ManifestChunks: manifestIDs,
		FileSize:       m.FileSize,
		Publisher:      opts.Publisher,
		Token:          opts.Token,
	})
	if err != nil {
		return link.Handle{}, fmt.Errorf("add: publish: %w", err)
	}
	return h, nil
}

// Get retrieves the file named by root and writes it to w. Every chunk
// is hash-verified on receipt, and the manifest's chunk list is verified
// against the root before any data is fetched — the registry entry
// itself is treated as untrusted routing metadata.
func Get(ctx context.Context, store ports.ChunkStore, reg ports.Registry, h link.Handle, w io.Writer) error {
	entry, ok, err := reg.Lookup(ctx, h.Root)
	if err != nil {
		return fmt.Errorf("get: %w", err)
	}
	if !ok {
		return fmt.Errorf("get %s: %w", h.Root, ports.ErrNoSuchEntry)
	}

	m, err := LoadFull(ctx, store, entry, h)
	if err != nil {
		return fmt.Errorf("get: %w", err)
	}
	// The critical check: the manifest we fetched must actually be the
	// one the root names. A registry (or store) pointing us at a
	// different manifest is caught right here.
	if m.Root() != h.Root {
		return fmt.Errorf("get: manifest root %s does not match requested root %s", m.Root(), h.Root)
	}

	mode, err := crypto.ParseMode(m.Mode)
	if err != nil {
		return fmt.Errorf("get: %w", err)
	}
	var fileKey [crypto.KeySize]byte
	if mode == crypto.Private {
		copy(fileKey[:], m.FileKey)
	}

	ctChunks, err := fetchDataChunks(ctx, store, m)
	if err != nil {
		return fmt.Errorf("get: %w", err)
	}

	frames := make([][]byte, 0, len(ctChunks))
	for i, ct := range ctChunks {
		var pt []byte
		switch mode {
		case crypto.Convergent:
			var secret [crypto.SecretSize]byte
			copy(secret[:], m.ChunkSecrets[i])
			pt, err = crypto.ConvergentDecrypt(ct, secret)
		case crypto.Private:
			pt, err = crypto.PrivateDecrypt(fileKey, uint64(i), ct)
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
	if counter.n != m.FileSize {
		return fmt.Errorf("get: reassembled %d bytes, manifest says %d", counter.n, m.FileSize)
	}
	return nil
}

// LoadBlob fetches, verifies, and joins the sealed manifest blob
// referenced by a registry entry — still ciphertext.
func LoadBlob(ctx context.Context, store ports.ChunkStore, entry ports.Entry) ([]byte, error) {
	var mbuf bytes.Buffer
	mframes := make([][]byte, 0, len(entry.ManifestChunks))
	for _, id := range entry.ManifestChunks {
		c, err := store.Get(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("manifest chunk: %w", err)
		}
		if !c.Verify() {
			return nil, fmt.Errorf("manifest chunk %s: %w", id, ports.ErrCorrupt)
		}
		mframes = append(mframes, c.Data)
	}
	if err := chunk.Join(&mbuf, mframes); err != nil {
		return nil, fmt.Errorf("reassembling manifest: %w", err)
	}
	return mbuf.Bytes(), nil
}

// LoadFull opens the manifest with the full link: layout + secrets.
func LoadFull(ctx context.Context, store ports.ChunkStore, entry ports.Entry, h link.Handle) (*manifest.Manifest, error) {
	blob, err := LoadBlob(ctx, store, entry)
	if err != nil {
		return nil, err
	}
	return manifest.OpenFull(blob, h.LayoutKey(), h.ContentKey())
}

// LoadLayout opens only the outer layer with a care link: structure
// without secrets — the caretaker's whole world.
func LoadLayout(ctx context.Context, store ports.ChunkStore, entry ports.Entry, ch link.CareHandle) (*manifest.Layout, error) {
	blob, err := LoadBlob(ctx, store, entry)
	if err != nil {
		return nil, err
	}
	l, err := manifest.OpenLayout(blob, ch.LayoutKey)
	if err != nil {
		return nil, err
	}
	if l.Root() != ch.Root {
		return nil, fmt.Errorf("layout root %s does not match care link root %s", l.Root(), ch.Root)
	}
	return l, nil
}

// fetchDataChunks returns every ciphertext data chunk of the file, in
// order, reconstructing missing or corrupt ones from parity when the
// manifest is erasure-coded. A shard that fails its hash check is
// treated exactly like a lost shard — to the decoder, corruption IS
// loss. Every reconstructed chunk is re-verified against the manifest's
// (root-committed) hash before use.
func fetchDataChunks(ctx context.Context, store ports.ChunkStore, m *manifest.Manifest) ([][]byte, error) {
	dataIDs := m.ChunkIDs()
	if m.K == 0 { // uncoded manifest: every chunk must be present
		out := make([][]byte, len(dataIDs))
		for i, id := range dataIDs {
			c, err := store.Get(ctx, id)
			if err != nil {
				return nil, fmt.Errorf("chunk %d: %w (no erasure coding to recover with)", i, err)
			}
			out[i] = c.Data
		}
		return out, nil
	}

	p := erasure.Params{K: m.K, N: m.N}
	parityIDs := m.ParityIDs()
	out := make([][]byte, len(dataIDs))
	for j := 0; j < p.Stripes(len(dataIDs)); j++ {
		lo := j * p.K
		hi := min(lo+p.K, len(dataIDs))
		realData := hi - lo

		shards := make([][]byte, p.N)
		missing := 0
		for i := 0; i < realData; i++ {
			c, err := store.Get(ctx, dataIDs[lo+i])
			if err != nil {
				missing++ // lost or corrupt — parity's problem now
				continue
			}
			shards[i] = c.Data
		}
		if missing > 0 {
			for q := 0; q < p.ParityShards(); q++ {
				c, err := store.Get(ctx, parityIDs[j*p.ParityShards()+q])
				if err != nil {
					continue
				}
				shards[p.K+q] = c.Data
			}
			if err := erasure.ReconstructStripe(p, shards, realData); err != nil {
				return nil, fmt.Errorf("stripe %d: %d data shard(s) lost and %w", j, missing, err)
			}
			// Reconstruction is only trusted if the recovered bytes hash
			// to the IDs the Merkle root committed to.
			for i := 0; i < realData; i++ {
				if ports.HashBytes(shards[i]) != dataIDs[lo+i] {
					return nil, fmt.Errorf("stripe %d: reconstructed chunk %d does not match its manifest hash", j, lo+i)
				}
			}
		}
		copy(out[lo:hi], shards[:realData])
	}
	return out, nil
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
