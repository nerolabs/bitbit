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
		fn()
	}
}

// Stop drains the queue and then lets Run return.
func (l *Loop) Stop() {
	l.mu.Lock()
	l.stopped = true
	l.cond.Broadcast()
	l.mu.Unlock()
}
