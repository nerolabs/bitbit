// Package tcpnet is the real-network Transport: length-prefixed CBOR
// frames over mutual TLS. This adapter is the HANDOFF's bet made good —
// the swap from simnet to real sockets touches zero core logic.
//
// Security model (M10): every connection is TLS 1.3 with BOTH ends
// presenting self-signed certificates, verified by pubkey pinning
// rather than PKI — a peer is authentic iff the key it presents hashes
// to the NodeID you addressed (dialing) or the NodeID it claims to be
// (accepting). Spoofed sender IDs die at the handshake; there is no CA
// because the identity IS the key (see adapters/identity).
//
// Two realities of leaving the sim are handled here, both invisibly to
// the core:
//
//   - Addressing. Core speaks pure NodeIDs; TCP needs ip:port. The
//     adapter keeps an address book, stamps every outgoing frame with
//     the sender's own listen address, and attaches known addresses for
//     any NodeIDs mentioned in the message. Receivers learn as they
//     listen — address gossip as an envelope concern.
//
//   - Concurrency. Sockets mean goroutines; core code is lock-free and
//     single-threaded by contract. Every delivery is posted onto the
//     node's event loop, never invoked from a reader goroutine.
//
//   - Conversations. A message rides the live connection with its peer
//     when one exists, and otherwise dials fresh and KEEPS the conn.
//     The crucial case is a reply riding the very conn the request
//     arrived on — the only road back to a NATed caller, who can dial
//     out but can never be dialed. Loss semantics stay UDP-ish: a
//     failed write or dial just drops the message and the core's
//     timeout machinery owns recovery; there is no retransmit here.
//     One deliberate exception: a reachability dial-back never reuses
//     a conn, because its entire meaning is "a fresh inbound dial to
//     your advertised address landed".
package tcpnet

import (
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sort"
	"sync"
	"time"

	"github.com/fxamacker/cbor/v2"

	"github.com/nerolabs/silt/adapters/eventloop"
	"github.com/nerolabs/silt/adapters/identity"
	"github.com/nerolabs/silt/adapters/relay"
	"github.com/nerolabs/silt/ports"
)

const maxFrame = 32 << 20 // sanity cap; a frame carries at most one chunk

// Peer is an entry in the address book.
type Peer struct {
	ID   ports.NodeID
	Addr string
}

type Transport struct {
	loop       *eventloop.Loop
	ident      *identity.Identity
	cert       tls.Certificate
	self       ports.NodeID
	listenAddr string
	ln         net.Listener
	handler    func(from ports.NodeID, msg ports.Message)

	mu    sync.Mutex
	peers map[ports.NodeID]string
	// conns holds the live conversation per peer, either direction.
	// The newest conn wins the slot; a displaced one keeps serving its
	// own readLoop until it dies naturally.
	conns map[ports.NodeID]*peerConn
	// adv, when set, replaces listenAddr in outgoing envelope stamps — a
	// NATed node advertising "reach me via relay R" instead of a
	// LAN address nobody outside the house can dial.
	adv string

	// lg narrates transport failures (dials, handshakes, forgeries) —
	// exactly the events that are invisible-but-fatal across real
	// networks. nil = off.
	lg ports.Logger
}

var _ ports.Transport = (*Transport)(nil)

// peerConn is one live connection. Frames from concurrent senders are
// serialized by wmu so the length-prefixed framing can never interleave.
type peerConn struct {
	conn *tls.Conn
	wmu  sync.Mutex
}

func (p *peerConn) write(frame []byte) error {
	p.wmu.Lock()
	defer p.wmu.Unlock()
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(frame)))
	if _, err := p.conn.Write(hdr[:]); err != nil {
		return err
	}
	_, err := p.conn.Write(frame)
	return err
}

// New starts listening on listenAddr with ident's TLS certificate.
func New(loop *eventloop.Loop, ident *identity.Identity, listenAddr string) (*Transport, error) {
	cert, err := ident.Certificate()
	if err != nil {
		return nil, fmt.Errorf("tcpnet: %w", err)
	}
	ln, err := tls.Listen("tcp", listenAddr, &tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientAuth:   tls.RequireAnyClientCert, // verified by pubkey hash after handshake
		MinVersion:   tls.VersionTLS13,
	})
	if err != nil {
		return nil, fmt.Errorf("tcpnet: %w", err)
	}
	t := &Transport{
		loop:       loop,
		ident:      ident,
		cert:       cert,
		self:       ident.NodeID(),
		listenAddr: ln.Addr().String(),
		ln:         ln,
		peers:      make(map[ports.NodeID]string),
		conns:      make(map[ports.NodeID]*peerConn),
	}
	go t.acceptLoop()
	return t, nil
}

func (t *Transport) Addr() string       { return t.listenAddr }
func (t *Transport) Self() ports.NodeID { return t.self }

func (t *Transport) Close() error {
	err := t.ln.Close()
	t.mu.Lock()
	open := make([]*peerConn, 0, len(t.conns))
	for _, pc := range t.conns {
		open = append(open, pc)
	}
	t.conns = make(map[ports.NodeID]*peerConn)
	t.mu.Unlock()
	for _, pc := range open {
		pc.conn.Close()
	}
	return err
}

// SetAdvertise overrides the address stamped on outgoing envelopes
// (default: the listen address). "" restores the default.
func (t *Transport) SetAdvertise(addr string) {
	t.mu.Lock()
	t.adv = addr
	t.mu.Unlock()
}

func (t *Transport) advertised() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.adv != "" {
		return t.adv
	}
	if isWildcard(t.listenAddr) {
		// A wildcard bind ("0.0.0.0" / "[::]") is not a dialable
		// address; stamping it would poison peers' address books — a
		// receiver "learns" to dial its own loopback. Stamp nothing:
		// peers that know a real address for us keep it, and everyone
		// else answers us over the conns we opened, or reaches us via
		// the relay we advertise once registered. Public daemons set
		// -advertise (or a concrete -listen) to be gossipable.
		return ""
	}
	return t.listenAddr
}

func isWildcard(hostport string) bool {
	host, _, err := net.SplitHostPort(hostport)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsUnspecified()
}

// AddPeer seeds the address book (bootstrap wiring).
func (t *Transport) AddPeer(id ports.NodeID, addr string) {
	t.mu.Lock()
	t.peers[id] = addr
	t.mu.Unlock()
}

// Peers snapshots the address book, sorted for deterministic output —
// this is what gets persisted for warm restarts (discovery).
func (t *Transport) Peers() []Peer {
	t.mu.Lock()
	out := make([]Peer, 0, len(t.peers))
	for id, addr := range t.peers {
		out = append(out, Peer{ID: id, Addr: addr})
	}
	t.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].ID.String() < out[j].ID.String() })
	return out
}

func (t *Transport) lookupAddr(id ports.NodeID) (string, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	addr, ok := t.peers[id]
	return addr, ok
}

func (t *Transport) learn(id ports.NodeID, addr string, overwrite bool) {
	if addr == "" {
		return
	}
	t.mu.Lock()
	if _, known := t.peers[id]; overwrite || !known {
		t.peers[id] = addr
	}
	t.mu.Unlock()
}

func (t *Transport) SetHandler(h func(from ports.NodeID, msg ports.Message)) {
	t.handler = h
}

// SetLogger wires the observability port; nil disables it.
func (t *Transport) SetLogger(lg ports.Logger) { t.lg = lg }

func (t *Transport) logf(lvl ports.LogLevel, event string, kv ...any) {
	ports.LogIf(t.lg, lvl, event, kv...)
}

// Send builds the envelope and hands delivery to a goroutine so the
// loop never blocks. Delivery prefers the live conversation with the
// peer; an address is only required when there isn't one (or when the
// message semantics demand a fresh dial).
func (t *Transport) Send(to ports.NodeID, msg ports.Message) error {
	// A reachability dial-back must NOT ride an existing conversation:
	// its entire meaning is "a fresh inbound dial to your advertised
	// address landed". Reusing the checker's own outbound conn would
	// report every NATed node as public. This is the one place the
	// transport looks at a message kind; the alternative is a second
	// port method, which buys nothing.
	freshDial := msg.Kind == ports.MsgReachabilityReply
	addr, hasAddr := t.lookupAddr(to)
	if !hasAddr && (freshDial || t.liveConn(to) == nil) {
		t.logf(ports.LogDebug, "send with no known address", "to", to)
		return fmt.Errorf("tcpnet: no known address for %s", to)
	}
	env := envelope{From: t.self[:], Addr: t.advertised(), Msg: toWire(msg)}
	contacts := make(map[string]string)
	for _, list := range [][]ports.NodeID{msg.Nodes, msg.Providers} {
		for _, id := range list {
			if a, known := t.lookupAddr(id); known {
				contacts[id.String()] = a
			}
		}
	}
	if len(contacts) > 0 {
		env.Contacts = contacts
	}
	frame, err := encMode.Marshal(env)
	if err != nil {
		return fmt.Errorf("tcpnet: encode: %w", err)
	}
	go t.deliver(to, addr, frame, freshDial)
	return nil
}

// deliver rides the live conversation with to when one exists — this is
// what lets a NATed peer be answered: it dialed us, that socket is
// open, and the reply belongs on it. Otherwise it dials (direct or via
// relay) and keeps the conn: its readLoop serves the peer's frames and
// future sends skip the dial+handshake toll.
func (t *Transport) deliver(to ports.NodeID, addr string, frame []byte, freshDial bool) {
	if !freshDial {
		if pc := t.liveConn(to); pc != nil {
			if pc.write(frame) == nil {
				return
			}
			t.dropConn(to, pc) // conversation died; try a fresh dial
		}
	}
	if addr == "" {
		t.logf(ports.LogDebug, "no path to peer", "to", to)
		return
	}
	conn, err := t.dialPeer(to, addr)
	if err != nil {
		return // logged in dialPeer; timeouts upstream handle the loss
	}
	pc := t.adopt(to, conn)
	if err := pc.write(frame); err != nil {
		t.dropConn(to, pc)
		return
	}
	go t.readLoop(conn)
}

// dialPeer dials with the target's identity pinned: if the far end's
// key doesn't hash to the NodeID we meant, the handshake fails and the
// message is dropped — impostors get silence, not data. A relay-form
// address changes only how the socket is reached: the pinned TLS
// session with the TARGET runs end-to-end through the relay's splice,
// so a relay (or anyone) injecting frames still dies at the handshake.
func (t *Transport) dialPeer(to ports.NodeID, addr string) (*tls.Conn, error) {
	cfg := identity.ClientConfig(t.cert, to)
	if relayID, relayAddr, ok := relay.SplitAddr(addr); ok {
		raw, err := relay.DialThrough(t.cert, relayID, relayAddr, to)
		if err != nil {
			t.logf(ports.LogWarn, "relay dial failed", "to", to, "addr", addr, "err", err)
			return nil, err
		}
		conn := tls.Client(raw, cfg)
		conn.SetDeadline(time.Now().Add(5 * time.Second))
		if err := conn.Handshake(); err != nil {
			t.logf(ports.LogWarn, "relayed handshake failed", "to", to, "addr", addr, "err", err)
			conn.Close()
			return nil, err
		}
		conn.SetDeadline(time.Time{})
		return conn, nil
	}
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 2 * time.Second}, "tcp", addr, cfg)
	if err != nil {
		t.logf(ports.LogWarn, "dial failed", "to", to, "addr", addr, "err", err)
		return nil, err
	}
	return conn, nil
}

// adopt records conn as the live conversation with id. The newest conn
// wins the slot; a displaced conn is not closed — its readLoop keeps
// serving whatever the peer still says on it until it dies naturally.
func (t *Transport) adopt(id ports.NodeID, conn *tls.Conn) *peerConn {
	t.mu.Lock()
	defer t.mu.Unlock()
	if pc, ok := t.conns[id]; ok && pc.conn == conn {
		return pc
	}
	pc := &peerConn{conn: conn}
	t.conns[id] = pc
	return pc
}

func (t *Transport) liveConn(id ports.NodeID) *peerConn {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.conns[id]
}

// dropConn forgets pc (if it is still the live conversation) and
// closes it.
func (t *Transport) dropConn(id ports.NodeID, pc *peerConn) {
	t.mu.Lock()
	if t.conns[id] == pc {
		delete(t.conns, id)
	}
	t.mu.Unlock()
	pc.conn.Close()
}

// RelayInbound serves one spliced conn handed over by a relay
// registration (see adapters/relay.Client). The inner TLS server
// handshake happens inside readLoop exactly as for a direct accept, so
// a relayed sender is authenticated by the same pinning rule — the
// relay contributed a pipe, not an identity.
func (t *Transport) RelayInbound(raw net.Conn) {
	t.readLoop(tls.Server(raw, &tls.Config{
		Certificates: []tls.Certificate{t.cert},
		ClientAuth:   tls.RequireAnyClientCert,
		MinVersion:   tls.VersionTLS13,
	}))
}

func (t *Transport) acceptLoop() {
	for {
		conn, err := t.ln.Accept()
		if err != nil {
			return // listener closed
		}
		go t.readLoop(conn.(*tls.Conn))
	}
}

func (t *Transport) readLoop(conn *tls.Conn) {
	if err := conn.Handshake(); err != nil {
		t.logf(ports.LogDebug, "inbound handshake failed", "remote", conn.RemoteAddr(), "err", err)
		conn.Close()
		return
	}
	// The sender's identity comes from the TLS handshake, not from
	// anything it writes in a frame. (Handshake is a no-op on a conn we
	// dialed ourselves; PeerID works on either end's certificate.)
	from, err := identity.PeerID(conn.ConnectionState())
	if err != nil {
		t.logf(ports.LogDebug, "inbound peer id rejected", "remote", conn.RemoteAddr(), "err", err)
		conn.Close()
		return
	}
	// This conn is now the live conversation with from — in particular,
	// our replies to a NATed peer ride it, because no dial can ever go
	// the other way.
	pc := t.adopt(from, conn)
	defer t.dropConn(from, pc)
	for {
		var hdr [4]byte
		if _, err := io.ReadFull(conn, hdr[:]); err != nil {
			return
		}
		n := binary.BigEndian.Uint32(hdr[:])
		if n == 0 || n > maxFrame {
			return
		}
		frame := make([]byte, n)
		if _, err := io.ReadFull(conn, frame); err != nil {
			return
		}
		var env envelope
		if err := cbor.Unmarshal(frame, &env); err != nil {
			return
		}
		// A frame claiming to be from someone other than the
		// authenticated key is a forgery; kill the connection.
		var claimed ports.NodeID
		copy(claimed[:], env.From)
		if claimed != from {
			t.logf(ports.LogWarn, "forged frame dropped", "authenticated", from, "claimed", claimed)
			return
		}
		t.learn(from, env.Addr, true)
		for idHex, addr := range env.Contacts {
			if id, err := ports.ParseHash(idHex); err == nil {
				t.learn(id, addr, false)
			}
		}
		msg := fromWire(env.Msg)
		t.loop.Post(func() {
			if t.handler != nil {
				t.handler(from, msg)
			}
		})
	}
}
