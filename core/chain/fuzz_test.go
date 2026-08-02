package chain

import (
	"bytes"
	"testing"
)

// FuzzDecode drives arbitrary bytes through the block decoder. Blocks
// arrive over the wire in a GET/response payload (see core/node
// chainrole), so a malformed encoding must fail with an error and never
// panic (Gate 1 / anti-persona #14).
func FuzzDecode(f *testing.F) {
	f.Add(Encode(&Block{}))
	f.Add([]byte{})
	f.Add([]byte{0xff, 0x00})
	f.Add(bytes.Repeat([]byte{0x9f}, 64)) // indefinite-length CBOR arrays

	f.Fuzz(func(t *testing.T, b []byte) {
		blk, err := Decode(b)
		if err != nil {
			return
		}
		// A block that decoded cleanly must re-encode without panicking —
		// exercises the accepted branch through the encoder too.
		_ = Encode(blk)
	})
}

// FuzzDecodeBlocks fuzzes the batch decoder used when a replica pulls a
// range of blocks from a peer.
func FuzzDecodeBlocks(f *testing.F) {
	f.Add(EncodeBlocks([]Block{{}, {}}))
	f.Add([]byte{})
	f.Add(bytes.Repeat([]byte{0x9f}, 128))

	f.Fuzz(func(t *testing.T, b []byte) {
		bs, err := DecodeBlocks(b)
		if err != nil {
			return
		}
		_ = EncodeBlocks(bs)
	})
}
