// Package eventloop serializes work onto a single goroutine — the real
// world's stand-in for the sim scheduler's single-threadedness. Core
// node code is written lock-free on the assumption that all its entry
// points (transport deliveries, timer callbacks, API calls) run one at
// a time; in the sim the scheduler guarantees that, and over real
// sockets this loop does.
package eventloop

import "sync"

type Loop struct {
	mu      sync.Mutex
	cond    *sync.Cond
	queue   []func()
	stopped bool
	// OnPanic, if set, is called when a posted task panics. The loop
	// recovers and keeps running, so one bad task — e.g. handling a
	// malformed frame that reaches a node-side decoder — fails that task
	// rather than killing the node's single thread (Gate 1 / anti-persona
	// #14). Set it before Run. If nil the panic is still contained but
	// silent; wire it to a logger in production so the drop is observable
	// (tenets S3/V4). Set-once before Run, so no lock is needed.
	OnPanic func(r any)
}

func New() *Loop {
	l := &Loop{}
	l.cond = sync.NewCond(&l.mu)
	return l
}

// Post enqueues fn from any goroutine. Never blocks.
func (l *Loop) Post(fn func()) {
	l.mu.Lock()
	if !l.stopped {
		l.queue = append(l.queue, fn)
		l.cond.Signal()
	}
	l.mu.Unlock()
}

// Run executes posted functions in order until Stop. Call from exactly
// one goroutine; that goroutine becomes "the node's thread".
func (l *Loop) Run() {
	for {
		l.mu.Lock()
		for len(l.queue) == 0 && !l.stopped {
			l.cond.Wait()
		}
		if len(l.queue) == 0 && l.stopped {
			l.mu.Unlock()
			return
		}
		fn := l.queue[0]
		l.queue = l.queue[1:]
		l.mu.Unlock()
		l.run(fn)
	}
}

// run executes one task under panic recovery so a panicking task cannot
// unwind through Run and stop the node's thread.
func (l *Loop) run(fn func()) {
	defer func() {
		if r := recover(); r != nil && l.OnPanic != nil {
			l.OnPanic(r)
		}
	}()
	fn()
}

// Stop drains the queue and then lets Run return.
func (l *Loop) Stop() {
	l.mu.Lock()
	l.stopped = true
	l.cond.Broadcast()
	l.mu.Unlock()
}
