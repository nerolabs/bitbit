// Package diskplot persists a node's identity-bound storage-bond plot
// (core/bond) to disk, so a restarting daemon RELOADS its multi-block plot
// and re-verifies it against the committed root instead of re-plotting from
// scratch (#93). Plotting is deliberately expensive — that expense is the
// Sybil cost — so paying it again on every restart would be both wasteful and,
// for a large pledge, a long stall before the validator can prove standing.
//
// One file per identity. The blocks are stored raw (they are already fixed-
// width, high-entropy bytes; a codec would only add overhead) behind a small
// header carrying the block geometry and the committed root, and written
// atomically (temp file + rename) so a crash mid-write never yields a
// half-plot that reads back as valid.
package diskplot

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"github.com/nerolabs/silt/ports"
)

// magic/version guard the on-disk format; a mismatch is treated as "no plot"
// so an older or foreign file triggers a clean re-plot rather than a misread.
const (
	magic      = 0x53504c54 // "SPLT"
	version    = 1
	headerSize = 4 + 4 + 4 + 4 + 32 // magic, version, blockSize, nBlocks, root
)

type Store struct{ root string }

var _ ports.PlotStore = (*Store)(nil)

// Open roots a plot store at dir, creating it if needed.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("diskplot: %w", err)
	}
	return &Store{root: dir}, nil
}

func (s *Store) path(id ports.NodeID) string {
	return filepath.Join(s.root, hex.EncodeToString(id[:])+".plot")
}

// Save writes the plot atomically: a temp file fully flushed, then renamed
// over the destination, so a reader never observes a partial plot.
func (s *Store) Save(id ports.NodeID, root ports.Hash, blocks [][]byte) error {
	if len(blocks) == 0 {
		return fmt.Errorf("diskplot: refusing to save an empty plot")
	}
	bs := len(blocks[0])
	dst := s.path(id)
	tmp, err := os.CreateTemp(s.root, ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())

	var hdr [headerSize]byte
	binary.BigEndian.PutUint32(hdr[0:], magic)
	binary.BigEndian.PutUint32(hdr[4:], version)
	binary.BigEndian.PutUint32(hdr[8:], uint32(bs))
	binary.BigEndian.PutUint32(hdr[12:], uint32(len(blocks)))
	copy(hdr[16:], root[:])
	if _, err := tmp.Write(hdr[:]); err != nil {
		tmp.Close()
		return err
	}
	for i, b := range blocks {
		if len(b) != bs {
			tmp.Close()
			return fmt.Errorf("diskplot: block %d is %d bytes, want a uniform %d", i, len(b), bs)
		}
		if _, err := tmp.Write(b); err != nil {
			tmp.Close()
			return err
		}
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), dst)
}

// Load reads a persisted plot. ok is false (with nil error) when no plot
// exists or the file is unreadable/foreign — the caller then re-plots. A file
// whose length disagrees with its header is corrupt and reported as such.
func (s *Store) Load(id ports.NodeID) (root ports.Hash, blocks [][]byte, ok bool, err error) {
	raw, rerr := os.ReadFile(s.path(id))
	if rerr != nil {
		return root, nil, false, nil // absent (or unreadable): re-plot, not fatal
	}
	if len(raw) < headerSize {
		return root, nil, false, fmt.Errorf("diskplot: plot file shorter than its header")
	}
	if binary.BigEndian.Uint32(raw[0:]) != magic || binary.BigEndian.Uint32(raw[4:]) != version {
		return root, nil, false, nil // foreign/older format: re-plot cleanly
	}
	bs := int(binary.BigEndian.Uint32(raw[8:]))
	n := int(binary.BigEndian.Uint32(raw[12:]))
	copy(root[:], raw[16:headerSize])
	if bs <= 0 || n <= 0 {
		return ports.Hash{}, nil, false, fmt.Errorf("diskplot: bad geometry (blockSize=%d nBlocks=%d)", bs, n)
	}
	body := raw[headerSize:]
	if len(body) != bs*n {
		return ports.Hash{}, nil, false, fmt.Errorf("diskplot: body is %d bytes, header implies %d", len(body), bs*n)
	}
	blocks = make([][]byte, n)
	for i := 0; i < n; i++ {
		blocks[i] = append([]byte(nil), body[i*bs:(i+1)*bs]...)
	}
	return root, blocks, true, nil
}
