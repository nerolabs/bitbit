//go:build !linux && !darwin

package tcpnet

import "syscall"

// Platforms without SO_REUSEPORT can't share a local port, so hole-punching
// (#27) isn't available — those nodes simply stay on the relay. reuseControl
// is a no-op so the code compiles and dials still work (just without port
// reuse); callers gate the punch on reusePortSupported.
const reusePortSupported = false

func reuseControl(_, _ string, _ syscall.RawConn) error { return nil }
