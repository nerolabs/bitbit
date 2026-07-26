// Package logfile is the file-sink Logger adapter: one grep-able line
// per event, written to a debug.log (or any io.Writer). This is what
// turns a one-off failure in the field into an artifact that can be
// pulled into a bug report and diagnosed — the whole point of the
// -debug flag.
//
// Line shape:
//
//	2026-07-26T17:03:09.412Z warn dial failed to=ab12… addr=1.2.3.4:4001 err="connection refused"
//
// Values render with %v; anything containing a space or '"' is quoted
// so a line always splits cleanly on spaces outside quotes.
package logfile

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/nerolabs/silt/ports"
)

type Sink struct {
	min ports.LogLevel
	now func() time.Time

	mu sync.Mutex
	w  io.Writer
	c  io.Closer
}

var _ ports.Logger = (*Sink)(nil)

// New logs everything at or above (i.e. numerically <=) min to w.
func New(w io.Writer, min ports.LogLevel) *Sink {
	return &Sink{w: w, min: min, now: time.Now}
}

// Open appends to the file at path, creating it if needed.
func Open(path string, min ports.LogLevel) (*Sink, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("logfile: %w", err)
	}
	s := New(f, min)
	s.c = f
	return s, nil
}

func (s *Sink) Enabled(l ports.LogLevel) bool { return l <= s.min }

func (s *Sink) Log(lvl ports.LogLevel, event string, kv ...any) {
	if !s.Enabled(lvl) {
		return
	}
	var b strings.Builder
	b.WriteString(s.now().UTC().Format("2006-01-02T15:04:05.000Z"))
	b.WriteByte(' ')
	b.WriteString(lvl.String())
	b.WriteByte(' ')
	b.WriteString(event)
	for i := 0; i+1 < len(kv); i += 2 {
		b.WriteByte(' ')
		fmt.Fprintf(&b, "%v", kv[i])
		b.WriteByte('=')
		v := fmt.Sprintf("%v", kv[i+1])
		if strings.ContainsAny(v, " \"") {
			v = fmt.Sprintf("%q", v)
		}
		b.WriteString(v)
	}
	b.WriteByte('\n')
	s.mu.Lock()
	s.w.Write([]byte(b.String()))
	s.mu.Unlock()
}

// Close flushes nothing (writes are unbuffered) but releases the file.
func (s *Sink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.c != nil {
		return s.c.Close()
	}
	return nil
}
