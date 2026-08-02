package chunk

import (
	"bytes"
	"io"
	"testing"
)

// FuzzJoin drives arbitrary frame bytes through Join, which reads each
// frame's 8-byte length header. A frame is attacker-controlled data off
// the wire, so a malformed or short frame — or a header claiming more
// payload than the frame holds — must be a clean error, never a panic or
// a slice out of range (Gate 1 / anti-persona #14). Two byte slices
// exercise the multi-frame path, including the non-final-frame check.
func FuzzJoin(f *testing.F) {
	f.Add([]byte{}, []byte{})
	f.Add(make([]byte, MinChunkSize), make([]byte, MinChunkSize))
	// A header that over-claims its payload: length = 0xffff... with a
	// tiny frame. Must error, not read past the frame.
	over := append([]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}, 0x01)
	f.Add(over, []byte{})

	f.Fuzz(func(t *testing.T, a, b []byte) {
		frames := [][]byte{}
		if len(a) > 0 {
			frames = append(frames, a)
		}
		if len(b) > 0 {
			frames = append(frames, b)
		}
		// Discard the output; we only care that Join never panics and only
		// ever fails with an error.
		_ = Join(io.Discard, frames)
	})
}

// FuzzRoundTrip proves Join undoes Split for any input at any legal chunk
// size — the exactness contract the package promises — while also
// fuzzing the split path.
func FuzzRoundTrip(f *testing.F) {
	f.Add([]byte("hello world"), 16)
	f.Add([]byte(""), MinChunkSize)
	f.Fuzz(func(t *testing.T, data []byte, size int) {
		if size < MinChunkSize || size > 1<<20 {
			return
		}
		frames, err := Split(bytes.NewReader(data), size)
		if err != nil {
			t.Fatalf("Split of %d bytes at size %d: %v", len(data), size, err)
		}
		var out bytes.Buffer
		if err := Join(&out, frames); err != nil {
			t.Fatalf("Join after Split: %v", err)
		}
		if !bytes.Equal(out.Bytes(), data) {
			t.Fatalf("round-trip mismatch: got %d bytes, want %d", out.Len(), len(data))
		}
	})
}
