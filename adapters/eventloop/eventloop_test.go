package eventloop

import (
	"sync"
	"testing"
)

// A task that panics must not stop the loop: the panic is reported and the
// next task still runs. This is the "node still up" outcome — one bad task
// (e.g. handling a malformed frame that reaches a decoder) fails that
// task, not the node's single thread.
func TestPanicInTaskDoesNotStopLoop(t *testing.T) {
	l := New()
	var mu sync.Mutex
	var panics []any
	l.OnPanic = func(r any) {
		mu.Lock()
		panics = append(panics, r)
		mu.Unlock()
	}

	go l.Run()
	defer l.Stop()

	done := make(chan struct{})
	l.Post(func() { panic("bad frame") })
	l.Post(func() { close(done) }) // must still run after the panic
	<-done

	mu.Lock()
	defer mu.Unlock()
	if len(panics) != 1 || panics[0] != "bad frame" {
		t.Fatalf("panic not reported through OnPanic, got %v", panics)
	}
}

// With no OnPanic hook set, a panicking task is still contained (the loop
// keeps running) — it is just silent. Proves the nil-hook path is safe.
func TestPanicContainedWithoutHook(t *testing.T) {
	l := New()
	go l.Run()
	defer l.Stop()

	done := make(chan struct{})
	l.Post(func() { panic("boom") })
	l.Post(func() { close(done) })
	<-done
}
