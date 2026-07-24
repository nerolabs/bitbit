// Package walltime is the real-clock adapter: ports.Clock backed by
// time.Now and time.AfterFunc, with every callback marshaled onto the
// node's event loop so core code keeps its single-threaded worldview.
package walltime

import (
	"sync/atomic"
	"time"

	"shardnet/adapters/eventloop"
	"shardnet/ports"
)

type Clock struct {
	loop *eventloop.Loop
}

var _ ports.Clock = (*Clock)(nil)

func New(loop *eventloop.Loop) *Clock { return &Clock{loop: loop} }

func (c *Clock) Now() ports.Time { return ports.Time(time.Now().UnixNano()) }

// AfterFunc fires fn on the loop after d. The cancel function honors the
// port contract exactly: a callback that has been posted to the loop but
// not yet run is still suppressed — core's request/timeout logic depends
// on "canceled means it will not fire", full stop.
func (c *Clock) AfterFunc(d ports.Duration, fn func()) (cancel func()) {
	var canceled atomic.Bool
	t := time.AfterFunc(time.Duration(d), func() {
		c.loop.Post(func() {
			if !canceled.Load() {
				fn()
			}
		})
	})
	return func() {
		canceled.Store(true)
		t.Stop()
	}
}
