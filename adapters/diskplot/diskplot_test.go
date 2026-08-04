package diskplot

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/nerolabs/silt/ports"
)

func nodeID(b byte) ports.NodeID { return ports.HashBytes([]byte{b}) }

func samplePlot() (ports.Hash, [][]byte) {
	blocks := [][]byte{
		bytes.Repeat([]byte{1}, 4096),
		bytes.Repeat([]byte{2}, 4096),
		bytes.Repeat([]byte{3}, 4096),
	}
	return ports.HashBytes([]byte("root")), blocks
}

// The restart path: a plot saved by one handle is read back identically by a
// fresh handle on the same directory.
func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	id := nodeID(1)
	root, blocks := samplePlot()
	if err := s.Save(id, root, blocks); err != nil {
		t.Fatalf("save: %v", err)
	}

	s2, err := Open(dir) // simulate a restart
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	gotRoot, gotBlocks, ok, err := s2.Load(id)
	if err != nil || !ok {
		t.Fatalf("load: ok=%v err=%v", ok, err)
	}
	if gotRoot != root {
		t.Fatalf("root mismatch: %x vs %x", gotRoot, root)
	}
	if len(gotBlocks) != len(blocks) {
		t.Fatalf("got %d blocks, want %d", len(gotBlocks), len(blocks))
	}
	for i := range blocks {
		if !bytes.Equal(gotBlocks[i], blocks[i]) {
			t.Fatalf("block %d differs after reload", i)
		}
	}
}

// An identity with no persisted plot loads cleanly as "absent" (ok=false, no
// error) so the caller plots fresh — not a fatal condition.
func TestLoadAbsent(t *testing.T) {
	s, _ := Open(t.TempDir())
	if _, _, ok, err := s.Load(nodeID(9)); ok || err != nil {
		t.Fatalf("absent plot: ok=%v err=%v, want ok=false err=nil", ok, err)
	}
}

// A truncated file (crash mid-write, bit-rot) is reported as corrupt rather
// than misread as a valid short plot — the caller then re-plots.
func TestLoadTruncatedIsCorrupt(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	id := nodeID(2)
	root, blocks := samplePlot()
	if err := s.Save(id, root, blocks); err != nil {
		t.Fatalf("save: %v", err)
	}
	// Chop the last block off the file.
	p := s.path(id)
	raw, _ := os.ReadFile(p)
	if err := os.WriteFile(p, raw[:len(raw)-4096], 0o644); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	if _, _, ok, err := s.Load(id); ok || err == nil {
		t.Fatal("a truncated plot should load as corrupt (ok=false, err!=nil)")
	}
}

// A foreign/older file (bad magic) is treated as "no plot", not an error, so
// a format bump triggers a clean re-plot instead of a crash.
func TestLoadForeignFormat(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	id := nodeID(3)
	if err := os.WriteFile(filepath.Join(dir, s.path(id)[len(dir)+1:]), bytes.Repeat([]byte{0xab}, 64), 0o644); err != nil {
		t.Fatalf("write foreign: %v", err)
	}
	if _, _, ok, err := s.Load(id); ok || err != nil {
		t.Fatalf("foreign file: ok=%v err=%v, want ok=false err=nil", ok, err)
	}
}

// M0 Sybil G2 migration: a WELL-FORMED plot from the previous format version
// (correct magic, version 2) must be rejected as "no plot" so a restart re-plots
// under the v3 identity-and-size-bound labeling instead of reloading the insecure
// v2 labeling the red-team broke. This is the version guard doing its load-bearing
// job — without it, a v2 plot's root re-derives fine (Merkle of block hashes) and
// would be silently reloaded (docs/design/m0-sybil-rebind.md §5, §8.4).
func TestLoadRejectsPreviousFormatVersion(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	id := nodeID(4)

	// Hand-write a valid header claiming version 2 with one block.
	block := bytes.Repeat([]byte{0x5a}, 4096)
	var hdr [headerSize]byte
	binary.BigEndian.PutUint32(hdr[0:], magic)
	binary.BigEndian.PutUint32(hdr[4:], 2) // the OLD format version
	binary.BigEndian.PutUint32(hdr[8:], uint32(len(block)))
	binary.BigEndian.PutUint32(hdr[12:], 1)
	root := ports.HashBytes([]byte("v2 root"))
	copy(hdr[16:], root[:])
	if err := os.WriteFile(filepath.Join(dir, s.path(id)[len(dir)+1:]), append(hdr[:], block...), 0o644); err != nil {
		t.Fatalf("write v2 plot: %v", err)
	}

	if _, _, ok, err := s.Load(id); ok || err != nil {
		t.Fatalf("G2 migration: a v2 plot must load as absent (ok=false, err=nil) so the node re-plots to v3; got ok=%v err=%v", ok, err)
	}
}
