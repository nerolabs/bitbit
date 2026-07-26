package relay

import (
	"crypto/tls"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/nerolabs/silt/adapters/identity"
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
	cfg := &tls.Config{
		Certificates:          []tls.Certificate{cert},
		InsecureSkipVerify:    true, // replaced by pinning, not skipped
		VerifyPeerCertificate: identity.VerifyExpected(relayID),
		MinVersion:            tls.VersionTLS13,
	}
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 5 * time.Second}, "tcp", relayAddr, cfg)
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

	mu     sync.Mutex
	conn   net.Conn // current control conn, nil between attempts
	closed bool
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
	if c.lg != nil && c.lg.Enabled(lvl) {
		c.lg.Log(lvl, event, kv...)
	}
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
func (c *Client) session(registered func(error)) error {
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
	c.mu.Unlock()

	conn.SetDeadline(time.Now().Add(opTimeout))
	wmu := &sync.Mutex{}
	write := func(fr ctrl) error {
		wmu.Lock()
		defer wmu.Unlock()
		return writeCtrl(conn, fr)
	}
	if err := write(ctrl{Op: "register"}); err != nil {
		conn.Close()
		return err
	}
	fr, err := readCtrl(conn)
	if err != nil || fr.Op != "ok" {
		conn.Close()
		return fmt.Errorf("relay register refused (%v %s)", err, fr.Err)
	}
	c.logf(ports.LogInfo, "relay registered", "relay", c.relayAddr)
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
		if fr.Op == "incoming" {
			go c.acceptStream(fr.Stream)
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
