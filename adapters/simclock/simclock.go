// Package simclock is the sim's beating heart: a single-threaded event
// scheduler that IS the clock. Events fire in (time, insertion-order)
// order, callbacks run synchronously inside Run, and nothing else in the
// sim ever sleeps or spawns a goroutine — which is why a run with the
// same seed is byte-identical, every time. Fast-forwarding is free:
// "waiting" ten simulated minutes is popping a heap entry.
package simclock

import (
	"container/heap"

	"shardnet/ports"
)

type event struct {
	at       ports.Time
	seq      uint64 // insertion order breaks time ties deterministically
	fn       func()
	canceled bool
}

type eventHeap []*event

func (h eventHeap) Len() int { return len(h) }
func (h eventHeap) Less(i, j int) bool {
	if h[i].at != h[j].at {
		return h[i].at < h[j].at
	}
	return h[i].seq < h[j].seq
}
func (h eventHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *eventHeap) Push(x any)        { *h = append(*h, x.(*event)) }
func (h *eventHeap) Pop() any {
	old := *h
	n := len(old)
	e := old[n-1]
	old[n-1] = nil
	*h = old[:n-1]
	return e
}

// Scheduler implements ports.Clock and owns simulated time.
type Scheduler struct {
	now    ports.Time
	seq    uint64
	events eventHeap
}

var _ ports.Clock = (*Scheduler)(nil)

func New() *Scheduler { return &Scheduler{} }

func (s *Scheduler) Now() ports.Time { return s.now }

// AfterFunc schedules fn at now+d. Negative d fires "immediately" (at
// the current time, after already-queued events for that instant).
func (s *Scheduler) AfterFunc(d ports.Duration, fn func()) (cancel func()) {
	if d < 0 {
		d = 0
	}
	s.seq++
	e := &event{at: s.now.Add(d), seq: s.seq, fn: fn}
	heap.Push(&s.events, e)
	return func() { e.canceled = true }
}

// Step fires the next event, advancing time to it. Returns false when
// the queue is empty.
func (s *Scheduler) Step() bool {
	for len(s.events) > 0 {
		e := heap.Pop(&s.events).(*event)
		if e.canceled {
			continue
		}
		s.now = e.at
		e.fn()
		return true
	}
	return false
}

// Run fires events until the queue drains (quiescence).
func (s *Scheduler) Run() {
	for s.Step() {
	}
}

// RunUntil fires events with time ≤ t, then advances the clock to t.
func (s *Scheduler) RunUntil(t ports.Time) {
	for len(s.events) > 0 {
		head := s.events[0]
		if head.canceled {
			heap.Pop(&s.events)
			continue
		}
		if head.at > t {
			break
		}
		s.Step()
	}
	if s.now < t {
		s.now = t
	}
}

// Pending reports how many live events are queued.
func (s *Scheduler) Pending() int {
	n := 0
	for _, e := range s.events {
		if !e.canceled {
			n++
		}
	}
	return n
}
