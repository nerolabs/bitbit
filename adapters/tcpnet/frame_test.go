// White-box tests for the frame-size cap (#104): they reference the
// unexported maxFrame, so they live in package tcpnet rather than the
// external test package the rest of the suite uses.
package tcpnet

import (
	"bytes"
	"testing"
	"time"

	"github.com/nerolabs/silt/adapters/eventloop"
	"github.com/nerolabs/silt/adapters/identity"
	"github.com/nerolabs/silt/ports"
)

func newTransport(t *testing.T, seed int64) (*Transport, *eventloop.Loop) {
	t.Helper()
	loop := eventloop.New()
	go loop.Run()
	tr, err := New(loop, identity.FromSeed(seed), "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return tr, loop
}

// A frame carrying a minimum-production-sized chunk (64 MiB) must round-trip
// over the real TLS transport. The old 32 MiB cap dropped every such frame,
// so the swarm could only move sim-sized (64 KiB) chunks (#104).
func TestMinProductionChunkFrameRoundTrips(t *testing.T) {
	trA, loopA := newTransport(t, 300)
	defer func() { trA.Close(); loopA.Stop() }()
	trB, loopB := newTransport(t, 301)
	defer func() { trB.Close(); loopB.Stop() }()

	payload := make([]byte, 64<<20) // 64 MiB: the minimum production chunk
	for i := range payload {
		payload[i] = byte(i)
	}
	got := make(chan ports.Message, 1)
	trB.SetHandler(func(_ ports.NodeID, m ports.Message) { got <- m })

	trA.AddPeer(identity.FromSeed(301).NodeID(), trB.Addr())
	if err := trA.Send(identity.FromSeed(301).NodeID(),
		ports.Message{Kind: ports.MsgStoreChunk, Data: payload}); err != nil {
		t.Fatalf("send 64 MiB chunk: %v", err)
	}

	select {
	case m := <-got:
		if !bytes.Equal(m.Data, payload) {
			t.Fatalf("64 MiB chunk corrupted in transit (got %d bytes)", len(m.Data))
		}
	case <-time.After(30 * time.Second):
		t.Fatal("64 MiB chunk frame never arrived — receiver dropped it")
	}
}

// A frame larger than the cap must fail Send with an explicit error rather
// than being emitted and silently dropped by the peer's read loop (S1/S3).
func TestOversizeFrameFailsSendLoudly(t *testing.T) {
	trA, loopA := newTransport(t, 302)
	defer func() { trA.Close(); loopA.Stop() }()
	trB, loopB := newTransport(t, 303)
	defer func() { trB.Close(); loopB.Stop() }()

	// A payload of maxFrame bytes guarantees the marshaled frame (payload +
	// envelope) exceeds maxFrame.
	oversize := make([]byte, maxFrame)
	trA.AddPeer(identity.FromSeed(303).NodeID(), trB.Addr())
	err := trA.Send(identity.FromSeed(303).NodeID(),
		ports.Message{Kind: ports.MsgStoreChunk, Data: oversize})
	if err == nil {
		t.Fatal("Send accepted an over-cap frame instead of failing loudly")
	}
	if !bytes.Contains([]byte(err.Error()), []byte("exceeds max")) {
		t.Fatalf("unexpected error (want a size rejection): %v", err)
	}

	// The node is still up: a normal send still works.
	got := make(chan struct{}, 1)
	trB.SetHandler(func(ports.NodeID, ports.Message) { got <- struct{}{} })
	if err := trA.Send(identity.FromSeed(303).NodeID(),
		ports.Message{Kind: ports.MsgFindNode}); err != nil {
		t.Fatalf("normal send after an oversize rejection: %v", err)
	}
	select {
	case <-got:
	case <-time.After(5 * time.Second):
		t.Fatal("node did not survive an oversize-frame rejection")
	}
}
