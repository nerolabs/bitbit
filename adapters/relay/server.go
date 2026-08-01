package relay

import (
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/nerolabs/silt/adapters/identity"
	"github.com/nerolabs/silt/ports"
)

// Config caps what a relay will carry. Every limit exists because an
// open relay is otherwise a free bandwidth faucet; these are the
// "rate-capped, opt-in" guardrails from the cross-network design, not a
// real abuse story (that's flagged for launch).
type Config struct {
	MaxSessions     int   // concurrent splices across all peers (default 128)
	PerPeerSessions int   // concurrent splices per registered target (default 16)
	MaxSessionBytes int64 // per-direction byte cap per splice (default 1 GiB)
}

func (c Config) withDefaults() Config {
	if c.MaxSessions == 0 {
		// Splices are short-lived (a request frame and its reply), so the cap
		// bounds *concurrent* fan-out, not total traffic. The original 64/8
		// throttled a single NATed target to 8 concurrent exchanges, which
		// saturated under a conc-10 publish/fetch sweep (#65); 128/16 gives a
		// rendezvous node realistic headroom while staying a bounded,
		// operator-tunable cost (each splice is still byte-capped below).
		c.MaxSessions = 128
	}
	if c.PerPeerSessions == 0 {
		c.PerPeerSessions = 16
	}
	if c.MaxSessionBytes == 0 {
		c.MaxSessionBytes = 1 << 30
	}
	return c
}

// Server is the relay service a reachable daemon offers with -relay.
// It authenticates every party by the same pinned-TLS rule as the swarm
// (the identity IS the key), forwards opaque bytes between them, and
// keeps no state beyond live connections.
type Server struct {
	cfg   Config
	ident *identity.Identity
	ln    net.Listener
	lg    ports.Logger

	mu       sync.Mutex
	regs     map[ports.NodeID]*control
	pend     map[uint64]*pendingSplice
	seq      uint64
	sessions int
	perPeer  map[ports.NodeID]int
}

// control is a registered NATed peer's standing conn. Writes come from
// multiple goroutines (pongs from its reader, incoming notices from
// connectors), hence the write lock.
type control struct {
	conn     net.Conn
	wmu      sync.Mutex
	observed string // the registrant's public host:port as we saw it (#27)
}

func (c *control) write(fr ctrl) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	return writeCtrl(c.conn, fr)
}

type pendingSplice struct {
	src    net.Conn // the connector, parked until the target accepts
	target ports.NodeID
	timer  *time.Timer
}

// Serve starts a relay listener at addr with ident's TLS certificate.
func Serve(addr string, ident *identity.Identity, cfg Config, lg ports.Logger) (*Server, error) {
	cert, err := ident.Certificate()
	if err != nil {
		return nil, fmt.Errorf("relay: %w", err)
	}
	ln, err := tls.Listen("tcp", addr, &tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientAuth:   tls.RequireAnyClientCert,
		MinVersion:   tls.VersionTLS13,
	})
	if err != nil {
		return nil, fmt.Errorf("relay: %w", err)
	}
	s := &Server{
		cfg:     cfg.withDefaults(),
		ident:   ident,
		ln:      ln,
		lg:      lg,
		regs:    make(map[ports.NodeID]*control),
		pend:    make(map[uint64]*pendingSplice),
		perPeer: make(map[ports.NodeID]int),
	}
	go s.acceptLoop()
	return s, nil
}

func (s *Server) Addr() string { return s.ln.Addr().String() }
func (s *Server) Close() error { return s.ln.Close() }

// Registered reports how many NATed peers currently lean on this relay.
func (s *Server) Registered() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.regs)
}

func (s *Server) logf(lvl ports.LogLevel, event string, kv ...any) {
	ports.LogIf(s.lg, lvl, event, kv...)
}

func (s *Server) acceptLoop() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return // listener closed
		}
		go s.handle(conn.(*tls.Conn))
	}
}

// handle authenticates a fresh conn and routes its first frame. The
// conn's fate depends on the op: register conns live here for their
// whole life; connect conns are parked for splicing (ownership moves to
// the pending table); accept conns are spliced immediately.
func (s *Server) handle(conn *tls.Conn) {
	conn.SetDeadline(time.Now().Add(opTimeout))
	if err := conn.Handshake(); err != nil {
		conn.Close()
		return
	}
	from, err := identity.PeerID(conn.ConnectionState())
	if err != nil {
		conn.Close()
		return
	}
	fr, err := readCtrl(conn)
	if err != nil {
		conn.Close()
		return
	}
	switch fr.Op {
	case "register":
		s.serveControl(from, conn)
	case "connect":
		s.connect(from, conn, fr)
	case "accept":
		s.accept(from, conn, fr)
	default:
		writeCtrl(conn, ctrl{Op: "err", Err: "unknown op"})
		conn.Close()
	}
}

// serveControl owns a registration for its lifetime: answer pings,
// carry incoming notices, drop the registration when the conn dies. A
// re-register from the same identity replaces the old conn (the client
// reconnected before the idle reaper noticed).
func (s *Server) serveControl(from ports.NodeID, conn *tls.Conn) {
	// The remote address is the registrant's NAT mapping as we see it — hand
	// it back (STUN-style) so a NATed node learns its own public endpoint, and
	// keep it to hand a hole-punch initiator (#27).
	c := &control{conn: conn, observed: conn.RemoteAddr().String()}
	s.mu.Lock()
	if old := s.regs[from]; old != nil {
		old.conn.Close()
	}
	s.regs[from] = c
	s.mu.Unlock()
	s.logf(ports.LogInfo, "relay registered", "peer", from, "observed", c.observed)
	if c.write(ctrl{Op: "ok", Addr: c.observed}) != nil {
		s.unregister(from, c)
		return
	}
	for {
		conn.SetDeadline(time.Now().Add(ctrlIdle))
		fr, err := readCtrl(conn)
		if err != nil {
			s.unregister(from, c)
			return
		}
		switch fr.Op {
		case "ping":
			if c.write(ctrl{Op: "pong"}) != nil {
				s.unregister(from, c)
				return
			}
		case "punch":
			s.coordinatePunch(from, c, fr)
		}
	}
}

// coordinatePunch relays a hole-punch request from `from` for the target in fr:
// it tells each registered peer the OTHER's observed endpoint, so both dial it
// at once (#27). The relay only swaps addresses — it forwards no bytes for the
// direct path. If the target isn't registered here, nothing happens and the
// requester keeps using the relay.
func (s *Server) coordinatePunch(from ports.NodeID, cFrom *control, fr ctrl) {
	if len(fr.Target) != 32 {
		return
	}
	var target ports.NodeID
	copy(target[:], fr.Target)
	s.mu.Lock()
	cTarget := s.regs[target]
	s.mu.Unlock()
	if cTarget == nil || target == from {
		return
	}
	// tell the target to punch the requester, and the requester to punch the target
	_ = cTarget.write(ctrl{Op: "punch", Target: from[:], Addr: cFrom.observed})
	_ = cFrom.write(ctrl{Op: "punch", Target: target[:], Addr: cTarget.observed})
	s.logf(ports.LogInfo, "relay punch coordinated", "a", from, "b", target)
}

func (s *Server) unregister(from ports.NodeID, c *control) {
	s.mu.Lock()
	if s.regs[from] == c {
		delete(s.regs, from)
	}
	s.mu.Unlock()
	c.conn.Close()
	s.logf(ports.LogInfo, "relay unregistered", "peer", from)
}

// connect parks the connector and notifies the target. Refusals are
// explicit frames so the dialer can distinguish "target unknown here"
// from a dead relay.
func (s *Server) connect(from ports.NodeID, conn *tls.Conn, fr ctrl) {
	var target ports.NodeID
	if len(fr.Target) != len(target) {
		writeCtrl(conn, ctrl{Op: "err", Err: "bad target"})
		conn.Close()
		return
	}
	copy(target[:], fr.Target)

	s.mu.Lock()
	reg := s.regs[target]
	full := s.sessions >= s.cfg.MaxSessions || s.perPeer[target] >= s.cfg.PerPeerSessions
	var id uint64
	if reg != nil && !full {
		s.seq++
		id = s.seq
		p := &pendingSplice{src: conn, target: target}
		p.timer = time.AfterFunc(opTimeout, func() { s.expire(id) })
		s.pend[id] = p
	}
	s.mu.Unlock()

	switch {
	case reg == nil:
		writeCtrl(conn, ctrl{Op: "err", Err: "target not registered"})
		conn.Close()
	case full:
		writeCtrl(conn, ctrl{Op: "err", Err: "relay at capacity"})
		conn.Close()
		s.logf(ports.LogWarn, "relay refused: at capacity", "target", target, "from", from)
	default:
		if reg.write(ctrl{Op: "incoming", Stream: id}) != nil {
			s.expire(id) // target's control conn just died
		}
	}
}

func (s *Server) expire(id uint64) {
	s.mu.Lock()
	p := s.pend[id]
	delete(s.pend, id)
	s.mu.Unlock()
	if p != nil {
		writeCtrl(p.src, ctrl{Op: "err", Err: "target did not accept"})
		p.src.Close()
	}
}

// accept matches the target's dial-back to a parked connector and
// splices. Only the identity the connector asked for may claim the
// stream — a third party guessing stream ids gets nothing.
func (s *Server) accept(from ports.NodeID, conn *tls.Conn, fr ctrl) {
	s.mu.Lock()
	p := s.pend[fr.Stream]
	if p != nil && p.target == from {
		delete(s.pend, fr.Stream)
		p.timer.Stop()
		s.sessions++
		s.perPeer[from]++
	} else {
		p = nil
	}
	s.mu.Unlock()
	if p == nil {
		writeCtrl(conn, ctrl{Op: "err", Err: "no such stream"})
		conn.Close()
		return
	}
	// Both ends hear "ok" and from then on own the pipe: the connector
	// starts its end-to-end handshake, the target answers it.
	if writeCtrl(p.src, ctrl{Op: "ok"}) != nil || writeCtrl(conn, ctrl{Op: "ok"}) != nil {
		p.src.Close()
		conn.Close()
		s.done(from)
		return
	}
	p.src.SetDeadline(time.Time{})
	conn.SetDeadline(time.Time{})
	s.logf(ports.LogInfo, "relay splice", "target", from)
	go s.splice(from, p.src, conn)
}

func (s *Server) done(target ports.NodeID) {
	s.mu.Lock()
	s.sessions--
	if s.perPeer[target]--; s.perPeer[target] <= 0 {
		delete(s.perPeer, target)
	}
	s.mu.Unlock()
}

// splice pumps bytes both ways until either side closes or the byte cap
// trips. Closing both on the first EOF is deliberate: swarm exchanges
// are short-lived (a frame and maybe a reply), not long streams.
func (s *Server) splice(target ports.NodeID, a, b net.Conn) {
	var wg sync.WaitGroup
	pump := func(dst, src net.Conn) {
		defer wg.Done()
		io.Copy(dst, io.LimitReader(src, s.cfg.MaxSessionBytes))
		a.Close() // both closed on the first EOF or the byte cap;
		b.Close() // Close is idempotent on a net.Conn
	}
	wg.Add(2)
	go pump(a, b)
	go pump(b, a)
	wg.Wait()
	s.done(target)
}
