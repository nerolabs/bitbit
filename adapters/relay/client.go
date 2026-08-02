package relay

import (
	"crypto/tls"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/nerolabs/silt/adapters/identity"
	"github.com/nerolabs/silt/internal/reuseport"
	"github.com/nerolabs/silt/internal/safe"
	"github.com/nerolabs/silt/ports"
)

// DialThrough reaches target through the relay at relayAddr: outer
// pinned-TLS dial to the relay, a connect request, and — once the
// target has accepted and the splice is live — the conn is returned as
// a raw pipe to the target. The caller runs its own end-to-end TLS
// handshake over it; the relay never holds bytes it can read.
func DialThrough(cert tls.Certificate, relayID ports.NodeID, relayAddr string, target ports.NodeID) (net.Conn, error) {
	conn, err := dialRelay(cert, relayID, relayAddr)
	if err != nil {
		return nil, err
	}
	conn.SetDeadline(time.Now().Add(opTimeout))
	if err := writeCtrl(conn, ctrl{Op: "connect", Target: target[:]}); err != nil {
		conn.Close()
		return nil, fmt.Errorf("relay connect: %w", err)
	}
	fr, err := readCtrl(conn)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("relay connect: %w", err)
	}
	if fr.Op != "ok" {
		conn.Close()
		return nil, fmt.Errorf("relay refused: %s", fr.Err)
	}
	conn.SetDeadline(time.Time{})
	return conn, nil
}

func dialRelay(cert tls.Certificate, relayID ports.NodeID, relayAddr string) (*tls.Conn, error) {
	cfg := identity.ClientConfig(cert, relayID)
	// Dial with SO_REUSEPORT so the local port this registration binds can
	// later be RE-bound by the hole-punch dial (which must fire from the same
	// port the relay observed, #27). Without it the punch dial fails to bind
	// (the port is already held by this conn) and every upgrade stays on the
	// relay — the exact bug behind #111.
	conn, err := tls.DialWithDialer(
		&net.Dialer{Timeout: 5 * time.Second, Control: reuseport.Control},
		"tcp", relayAddr, cfg)
	if err != nil {
		return nil, fmt.Errorf("relay dial %s: %w", relayAddr, err)
	}
	return conn, nil
}

// Client is the NATed side of the relay: it keeps one registered
// control conn to the relay alive (reconnecting with backoff, pinging
// through NAT idle timeouts) and, for every incoming notice, dials back
// and hands the spliced raw conn to OnConn. OnConn is expected to run
// the end-to-end TLS *server* handshake — tcpnet does exactly that.
type Client struct {
	relayID   ports.NodeID
	relayAddr string
	cert      tls.Certificate
	onConn    func(net.Conn)
	lg        ports.Logger

	mu        sync.Mutex
	conn      net.Conn // current control conn, nil between attempts
	closed    bool
	observed  string // our public host:port as the relay last reported it (#27)
	localPort int    // local port of the control conn — reused for the punch dial (#27)
	writeMu   sync.Mutex
	onPunch   func(peer ports.NodeID, peerAddr string, localPort int)
}

// Observed returns this node's public host:port as the relay saw it at
// registration ("" until the first successful register). A NATed node uses it
// as the endpoint a peer should aim a hole-punch at (#27).
func (c *Client) Observed() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.observed
}

// LocalPort is the control conn's local port. A hole-punch dials the peer from
// this same port so the NAT reuses the mapping the relay observed (#27).
func (c *Client) LocalPort() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.localPort
}

// SetOnPunch registers the callback fired when the relay tells us to punch a
// peer: (peer id, peer's observed endpoint to dial, our reusable local port).
func (c *Client) SetOnPunch(fn func(peer ports.NodeID, peerAddr string, localPort int)) {
	c.onPunch = fn
}

// send writes a control frame to the current registration conn, serialized
// against the pinger and other senders.
func (c *Client) send(fr ctrl) error {
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn == nil {
		return fmt.Errorf("relay: no control connection")
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return writeCtrl(conn, fr)
}

// RequestPunch asks the relay to coordinate a hole-punch with target: the relay
// tells each side the other's observed endpoint (Addr), and both dial it from
// their registration port at once — DCUtR upgrading a relay to a direct link
// (#27). Best-effort; if the punch fails the relay path stays.
func (c *Client) RequestPunch(target ports.NodeID) {
	_ = c.send(ctrl{Op: "punch", Target: target[:]})
}

// NewClient prepares a registration with the relay; Run starts it.
func NewClient(ident *identity.Identity, relayID ports.NodeID, relayAddr string, onConn func(net.Conn), lg ports.Logger) (*Client, error) {
	cert, err := ident.Certificate()
	if err != nil {
		return nil, fmt.Errorf("relay: %w", err)
	}
	return &Client{
		relayID:   relayID,
		relayAddr: relayAddr,
		cert:      cert,
		onConn:    onConn,
		lg:        lg,
	}, nil
}

// Addr is the address other peers should use to reach us: the
// address-book form tcpnet's dialer recognizes.
func (c *Client) Addr() string { return Addr(c.relayID, c.relayAddr) }

func (c *Client) logf(lvl ports.LogLevel, event string, kv ...any) {
	ports.LogIf(c.lg, lvl, event, kv...)
}

// Run registers and serves until Close, reconnecting with backoff. The
// first outcome — whether the initial registration landed — is reported
// through ready so a daemon can decide what to advertise. ready fires
// once, as soon as the registration is acknowledged (a session then
// stays up for its whole lifetime).
func (c *Client) Run(ready func(error)) {
	notify := func(err error) {
		if ready != nil {
			ready(err)
			ready = nil
		}
	}
	backoff := time.Second
	for {
		err := c.session(notify)
		c.mu.Lock()
		closed := c.closed
		c.mu.Unlock()
		notify(err)
		if closed {
			return
		}
		c.logf(ports.LogWarn, "relay registration lost", "relay", c.relayAddr, "err", err)
		time.Sleep(backoff)
		if backoff *= 2; backoff > time.Minute {
			backoff = time.Minute
		}
	}
}

// Close stops Run and drops the registration.
func (c *Client) Close() {
	c.mu.Lock()
	c.closed = true
	if c.conn != nil {
		c.conn.Close()
	}
	c.mu.Unlock()
}

// session is one registration: dial, register, then answer incoming
// notices until the conn dies. registered fires once the relay has
// acknowledged us; the return value says why the session ended.
func (c *Client) session(registered func(error)) (err error) {
	// A malformed frame from the relay must end this session, not crash
	// the client (Gate 1 / anti-persona #14); Run's backoff loop then
	// reconnects. The panic surfaces as the session's error.
	defer safe.Recover("relay: client session", &err)
	conn, err := dialRelay(c.cert, c.relayID, c.relayAddr)
	if err != nil {
		return err
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		conn.Close()
		return nil
	}
	c.conn = conn
	if la, ok := conn.LocalAddr().(*net.TCPAddr); ok {
		c.localPort = la.Port // the port a hole-punch will reuse (#27)
	}
	c.mu.Unlock()

	conn.SetDeadline(time.Now().Add(opTimeout))
	write := c.send
	if err := write(ctrl{Op: "register"}); err != nil {
		conn.Close()
		return err
	}
	fr, err := readCtrl(conn)
	if err != nil || fr.Op != "ok" {
		conn.Close()
		return fmt.Errorf("relay register refused (%v %s)", err, fr.Err)
	}
	if fr.Addr != "" { // the relay's STUN-style view of our public endpoint (#27)
		c.mu.Lock()
		c.observed = fr.Addr
		c.mu.Unlock()
	}
	c.logf(ports.LogInfo, "relay registered", "relay", c.relayAddr, "observed", fr.Addr)
	registered(nil)

	// Pings keep the NAT mapping (and the relay's idle reaper) at bay.
	stopPing := make(chan struct{})
	defer close(stopPing)
	go func() {
		t := time.NewTicker(pingEvery)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				if write(ctrl{Op: "ping"}) != nil {
					conn.Close() // unblocks the read loop below
					return
				}
			case <-stopPing:
				return
			}
		}
	}()

	for {
		conn.SetDeadline(time.Now().Add(ctrlIdle))
		fr, err := readCtrl(conn)
		if err != nil {
			conn.Close()
			return err
		}
		switch {
		case fr.Op == "incoming":
			go c.acceptStream(fr.Stream)
		case fr.Op == "punch" && c.onPunch != nil && len(fr.Target) == 32 && fr.Addr != "":
			// The relay is coordinating a hole-punch: dial fr.Addr from our
			// registration port, right now, while the peer does the same (#27).
			var peer ports.NodeID
			copy(peer[:], fr.Target)
			c.mu.Lock()
			lp := c.localPort
			c.mu.Unlock()
			go c.onPunch(peer, fr.Addr, lp)
		}
	}
}

// acceptStream dials the second conn back to the relay and claims the
// stream; on "ok" the conn is a raw pipe to whoever asked for us.
func (c *Client) acceptStream(id uint64) {
	conn, err := dialRelay(c.cert, c.relayID, c.relayAddr)
	if err != nil {
		c.logf(ports.LogWarn, "relay accept dial failed", "err", err)
		return
	}
	conn.SetDeadline(time.Now().Add(opTimeout))
	if err := writeCtrl(conn, ctrl{Op: "accept", Stream: id}); err != nil {
		conn.Close()
		return
	}
	fr, err := readCtrl(conn)
	if err != nil || fr.Op != "ok" {
		conn.Close()
		return
	}
	conn.SetDeadline(time.Time{})
	c.onConn(conn)
}
