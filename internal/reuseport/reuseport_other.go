//go:build !linux && !darwin

package reuseport

import "syscall"

// Platforms without SO_REUSEPORT can't share a local port, so hole-punching
// (#27) isn't available — those nodes simply stay on the relay. Control is a
// no-op so the code compiles and dials still work (just without port reuse);
// callers gate the punch on Supported.
const Supported = false

func Control(_, _ string, _ syscall.RawConn) error { return nil }
