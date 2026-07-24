package simclock

import (
	"testing"

	"shardnet/ports"
)

func TestFiresInTimeOrderWithStableTies(t *testing.T) {
	s := New()
	var got []int
	s.AfterFunc(20*ports.Millisecond, func() { got = append(got, 3) })
	s.AfterFunc(10*ports.Millisecond, func() { got = append(got, 1) })
	s.AfterFunc(10*ports.Millisecond, func() { got = append(got, 2) }) // same instant: insertion order
	s.Run()
	want := []int{1, 2, 3}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order %v, want %v", got, want)
		}
	}
	if s.Now() != ports.Time(20*ports.Millisecond) {
		t.Fatalf("clock at %d", s.Now())
	}
}

func TestCancel(t *testing.T) {
	s := New()
	fired := false
	cancel := s.AfterFunc(ports.Millisecond, func() { fired = true })
	cancel()
	cancel() // idempotent
	s.Run()
	if fired {
		t.Fatal("canceled event fired")
	}
}

func TestNestedScheduling(t *testing.T) {
	s := New()
	var order []string
	s.AfterFunc(10*ports.Millisecond, func() {
		order = append(order, "a")
		s.AfterFunc(5*ports.Millisecond, func() { order = append(order, "c") })
	})
	s.AfterFunc(12*ports.Millisecond, func() { order = append(order, "b") })
	s.Run()
	if len(order) != 3 || order[0] != "a" || order[1] != "b" || order[2] != "c" {
		t.Fatalf("order %v", order)
	}
}

func TestRunUntilStopsAtDeadline(t *testing.T) {
	s := New()
	var fired []int
	s.AfterFunc(10*ports.Millisecond, func() { fired = append(fired, 1) })
	c := s.AfterFunc(15*ports.Millisecond, func() { fired = append(fired, 99) })
	c() // canceled event sits at the heap front region past deadline handling
	s.AfterFunc(30*ports.Millisecond, func() { fired = append(fired, 2) })
	s.RunUntil(ports.Time(20 * ports.Millisecond))
	if len(fired) != 1 || fired[0] != 1 {
		t.Fatalf("fired %v, want [1]", fired)
	}
	if s.Now() != ports.Time(20*ports.Millisecond) {
		t.Fatalf("clock at %d, want deadline", s.Now())
	}
	s.Run()
	if len(fired) != 2 || fired[1] != 2 {
		t.Fatalf("fired %v, want [1 2]", fired)
	}
}
