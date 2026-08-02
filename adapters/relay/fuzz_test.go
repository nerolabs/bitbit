package relay

import (
	"bytes"
	"testing"

	"github.com/fxamacker/cbor/v2"
)

// FuzzCtrlDecode drives arbitrary bytes through the relay control-frame
// decoder. A relay accepts frames from unauthenticated dialers, so a
// malformed control frame must fail with an error and never panic (Gate 1
// / anti-persona #14). This mirrors the cbor.Unmarshal in readCtrl,
// isolated from the socket read so the fuzzer feeds bytes directly.
func FuzzCtrlDecode(f *testing.F) {
	if b, err := encMode.Marshal(ctrl{Op: "connect", Target: bytes.Repeat([]byte{0x02}, 32), Addr: "1.2.3.4:5"}); err == nil {
		f.Add(b)
	}
	f.Add([]byte{})
	f.Add([]byte{0xff})
	f.Add(bytes.Repeat([]byte{0xbf}, 64))

	f.Fuzz(func(t *testing.T, b []byte) {
		var c ctrl
		_ = cbor.Unmarshal(b, &c)
	})
}
