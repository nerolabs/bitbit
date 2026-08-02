package tcpnet

import (
	"bytes"
	"testing"

	"github.com/fxamacker/cbor/v2"

	"github.com/nerolabs/silt/ports"
)

// FuzzEnvelopeDecode drives arbitrary bytes through the exact decode path
// readLoop runs on every inbound frame: CBOR-decode the envelope, then
// project the wire message onto the port type via fromWire. This is the
// most exposed decoder in the system — raw bytes from any peer that
// completes a TLS handshake — so it must always fail with an error and
// never panic (Gate 1 / anti-persona #14). readLoop also has a recover
// net under it, but the point of the fuzzer is that the net is never
// needed here.
func FuzzEnvelopeDecode(f *testing.F) {
	// Seed with a real envelope carrying a fully-populated message so the
	// fuzzer starts from valid wire structure and mutates outward.
	seed := envelope{
		From: bytes.Repeat([]byte{0x01}, 32),
		Addr: "127.0.0.1:9000",
		Msg: toWire(ports.Message{
			Kind:      ports.MsgKind(3),
			RID:       42,
			Nodes:     []ports.NodeID{{1}, {2}},
			Providers: []ports.NodeID{{3}},
			Data:      []byte("chunk"),
			Proof:     &ports.StorageProof{Total: 4, Index: 1, Path: []ports.Hash{{9}}},
		}),
	}
	if b, err := encMode.Marshal(seed); err == nil {
		f.Add(b)
	}
	f.Add([]byte{})
	f.Add([]byte{0xff})
	f.Add(bytes.Repeat([]byte{0xbf}, 64)) // indefinite-length CBOR maps

	f.Fuzz(func(t *testing.T, b []byte) {
		var env envelope
		if err := cbor.Unmarshal(b, &env); err != nil {
			return
		}
		// fromWire runs on every accepted frame; it copies attacker-sized
		// byte slices into fixed-width arrays, so it must tolerate any
		// length without panicking.
		_ = fromWire(env.Msg)
	})
}
