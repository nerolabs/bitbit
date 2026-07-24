// Package chunk splits a byte stream into fixed-size frames and joins
// them back, exactly.
//
// Every frame is the same size (ChunkSize), which matters later: erasure
// coding needs equal-length shards. The final piece of a file is almost
// never a full chunk, so each frame carries an explicit length header and
// the tail is zero-padded:
//
//	[8-byte big-endian payload length][payload][zero padding]
//
// Join reads the header and takes exactly that many bytes, so
// Join(Split(x)) == x for any x — including empty — without needing any
// out-of-band size information.
package chunk

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// HeaderSize is the per-frame framing overhead in bytes.
const HeaderSize = 8

// MinChunkSize is the smallest usable chunk size (header + 1 payload byte).
const MinChunkSize = HeaderSize + 1

var ErrBadFrame = errors.New("chunk: malformed frame")

// Split reads r to EOF and returns frames of exactly chunkSize bytes.
// An empty input yields zero frames.
func Split(r io.Reader, chunkSize int) ([][]byte, error) {
	if chunkSize < MinChunkSize {
		return nil, fmt.Errorf("chunk: chunkSize %d < minimum %d", chunkSize, MinChunkSize)
	}
	payloadSize := chunkSize - HeaderSize
	var frames [][]byte
	for {
		frame := make([]byte, chunkSize)
		n, err := io.ReadFull(r, frame[HeaderSize:])
		if n == 0 {
			if err == io.EOF || err == io.ErrUnexpectedEOF || err == nil {
				return frames, nil
			}
			return nil, err
		}
		if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
			return nil, err
		}
		binary.BigEndian.PutUint64(frame[:HeaderSize], uint64(n))
		frames = append(frames, frame)
		if n < payloadSize {
			return frames, nil
		}
	}
}

// Join writes the original stream reassembled from frames to w.
// Every frame except the last must be full; any inconsistency is an error
// rather than a silent truncation.
func Join(w io.Writer, frames [][]byte) error {
	for i, frame := range frames {
		if len(frame) < MinChunkSize {
			return fmt.Errorf("%w: frame %d is %d bytes", ErrBadFrame, i, len(frame))
		}
		n := binary.BigEndian.Uint64(frame[:HeaderSize])
		payloadSize := uint64(len(frame) - HeaderSize)
		if n > payloadSize {
			return fmt.Errorf("%w: frame %d claims %d payload bytes, has room for %d", ErrBadFrame, i, n, payloadSize)
		}
		if i < len(frames)-1 && n != payloadSize {
			return fmt.Errorf("%w: non-final frame %d is short (%d of %d bytes)", ErrBadFrame, i, n, payloadSize)
		}
		if _, err := w.Write(frame[HeaderSize : HeaderSize+n]); err != nil {
			return err
		}
	}
	return nil
}
