//go:build linux || darwin

// Package reuseport centralizes the SO_REUSEPORT dial control used for TCP
// hole-punching (#27). Both the transport (which fires the punch) and the
// relay client (whose registration conn's local port the punch REUSES) must
// set it, and the relay adapter can't import the transport, so the hook
// lives here where both can reach it.
package reuseport

import (
	"syscall"

	"golang.org/x/sys/unix"
)

// Supported reports whether this platform can share a local port across
// sockets — the prerequisite for the coordinated TCP simultaneous-open, which
// dials a peer from the SAME local port the relay-registration connection used
// so the NAT reuses (and the relay already observed) the mapping. Linux and
// Darwin have SO_REUSEPORT; elsewhere a node simply stays on the relay.
const Supported = true

// Control is a net.Dialer/ListenConfig Control hook: set SO_REUSEADDR +
// SO_REUSEPORT before the socket binds, so multiple connections can share one
// local port. The relay control conn is dialed with this so the port it
// registers under can later be re-bound by the hole-punch dial.
func Control(_, _ string, c syscall.RawConn) error {
	var serr error
	if err := c.Control(func(fd uintptr) {
		if err := unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEADDR, 1); err != nil {
			serr = err
			return
		}
		serr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEPORT, 1)
	}); err != nil {
		return err
	}
	return serr
}
