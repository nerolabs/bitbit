package node

import (
	"errors"
	"testing"

	"github.com/nerolabs/silt/adapters/memstore"
	"github.com/nerolabs/silt/adapters/simclock"
	"github.com/nerolabs/silt/ports"
)

// linkNet is a minimal two-endpoint transport for unit-testing the fetch
// path. It delivers each Send to the target node's handler via the shared
// scheduler — so replies flow through the real serving code, not a mock —
// and can fail a fetcher's FetchChunk send transiently, standing in for the
// "relay at capacity" refusal a saturated relay returns under load (#65).
type linkNet struct {
	sched     *simclock.Scheduler
	ends      map[ports.NodeID]*linkEnd
	dropFetch func(from, to ports.NodeID) bool
}

type linkEnd struct {
	net *linkNet
	id  ports.NodeID
	h   func(from ports.NodeID, msg ports.Message)
}

func (e *linkEnd) SetHandler(h func(ports.NodeID, ports.Message)) { e.h = h }

func (e *linkEnd) Send(to ports.NodeID, msg ports.Message) error {
	// Only an outbound fetch is subject to the injected relay refusal; the
	// reply (MsgFetchChunkReply) travels a different kind and is never dropped.
	if msg.Kind == ports.MsgFetchChunk && e.net.dropFetch != nil && e.net.dropFetch(e.id, to) {
		return errors.New("relay at capacity")
	}
	dst, ok := e.net.ends[to]
	if !ok {
		return errors.New("no route to peer")
	}
	e.net.sched.AfterFunc(ports.Millisecond, func() { dst.h(e.id, msg) })
	return nil
}

// twoNode wires a fetcher and a provider over a linkNet. The provider holds
// `held` (skipped when its Data is nil) and serves it with the real handler;
// the fetcher's store starts empty. Returns the fetcher, the provider's id,
// and the scheduler to Run.
func twoNode(t *testing.T, cfg Config, held ports.Chunk, dropFetch func(from, to ports.NodeID) bool) (*Node, ports.NodeID, *simclock.Scheduler) {
	t.Helper()
	sched := simclock.New()
	ln := &linkNet{sched: sched, ends: map[ports.NodeID]*linkEnd{}, dropFetch: dropFetch}
	var fID, pID ports.NodeID
	fID[0], pID[0] = 1, 2
	fEnd := &linkEnd{net: ln, id: fID}
	pEnd := &linkEnd{net: ln, id: pID}
	ln.ends[fID], ln.ends[pID] = fEnd, pEnd

	fetcher := New(fID, cfg, sched, fEnd, memstore.New())
	pStore := memstore.New()
	if held.Data != nil {
		pStore.Put(bg(), held)
	}
	New(pID, cfg, sched, pEnd, pStore) // provider serves via its own handler
	return fetcher, pID, sched
}

// TestFetchRetryRecoversTransientRelayCongestion is the #65 fetch-side
// regression: a provider reached through a saturated relay refuses the first
// fetch ("relay at capacity"), then the slot frees. With FetchAttempts>1 the
// fetch re-sweeps after a backoff and succeeds; before the retry it reported
// the chunk unreachable — the Mac B tail-of-sweep failures in the field.
func TestFetchRetryRecoversTransientRelayCongestion(t *testing.T) {
	chunk := ports.NewChunk([]byte("hello relay congestion"))
	sends := 0
	drop := func(from, to ports.NodeID) bool { sends++; return sends == 1 } // only the first fetch send
	fetcher, provID, sched := twoNode(t, DefaultConfig(), chunk, drop)

	var got, done bool
	fetcher.fetchFrom(chunk.ID, []ports.NodeID{provID}, func(ok bool) { got, done = ok, true })
	sched.Run()

	if !done || !got {
		t.Fatalf("fetch should recover after a transient relay refusal (done=%v got=%v)", done, got)
	}
	if ok, _ := fetcher.store.Has(bg(), chunk.ID); !ok {
		t.Fatalf("the recovered chunk should be in the fetcher's store")
	}
}

// TestFetchNoRetryStillFailsLoud shows the retry is what recovers it: with
// FetchAttempts=1 the same single transient refusal is fatal.
func TestFetchNoRetryStillFailsLoud(t *testing.T) {
	chunk := ports.NewChunk([]byte("hello relay congestion"))
	cfg := DefaultConfig()
	cfg.FetchAttempts = 1
	sends := 0
	drop := func(from, to ports.NodeID) bool { sends++; return sends == 1 }
	fetcher, provID, sched := twoNode(t, cfg, chunk, drop)

	var got, done bool
	fetcher.fetchFrom(chunk.ID, []ports.NodeID{provID}, func(ok bool) { got, done = ok, true })
	sched.Run()

	if !done || got {
		t.Fatalf("with retry disabled the transient refusal should fail (done=%v got=%v)", done, got)
	}
}

// TestFetchNoRetryOnCleanMiss: when the provider simply doesn't have the
// chunk (a clean Found:false, not a transport error), fetchFrom must NOT
// spend its FetchAttempts on pointless re-sweeps — one pass, then false.
func TestFetchNoRetryOnCleanMiss(t *testing.T) {
	want := ports.NewChunk([]byte("nobody has this"))
	sends := 0
	count := func(from, to ports.NodeID) bool { sends++; return false } // never a transport error
	fetcher, provID, sched := twoNode(t, DefaultConfig(), ports.Chunk{}, count)

	var got, done bool
	fetcher.fetchFrom(want.ID, []ports.NodeID{provID}, func(ok bool) { got, done = ok, true })
	sched.Run()

	if !done || got {
		t.Fatalf("a clean miss should report false (done=%v got=%v)", done, got)
	}
	if sends != 1 {
		t.Fatalf("a clean miss must not re-sweep (FetchAttempts=%d): got %d fetch sends, want 1",
			DefaultConfig().FetchAttempts, sends)
	}
}
