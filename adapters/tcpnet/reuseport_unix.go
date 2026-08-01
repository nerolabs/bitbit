//go:build linux || darwin

package tcpnet

import (
	"syscall"

	"golang.org/x/sys/unix"
)

// reusePortSupported reports whether this platform can share a local port
// across sockets — the prerequisite for TCP hole-punching, which must dial a
// peer from the SAME local port the relay-registration connection used so the
// NAT reuses (and the relay already observed) the mapping. Linux and Darwin
// have SO_REUSEPORT; elsewhere we simply stay on the relay.
const reusePortSupported = true

// reuseControl is a net.Dialer/ListenConfig Control hook: set SO_REUSEADDR +
// SO_REUSEPORT on the socket before it binds, so multiple connections can
// share one local port. Used for the hole-punch dial (#27).
func reuseControl(_, _ string, c syscall.RawConn) error {
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
