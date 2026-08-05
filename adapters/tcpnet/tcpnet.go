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
	"strings"
	"sync"
	"time"

	"github.com/fxamacker/cbor/v2"

	"github.com/nerolabs/silt/adapters/eventloop"
	"github.com/nerolabs/silt/adapters/identity"
	"github.com/nerolabs/silt/adapters/relay"
	"github.com/nerolabs/silt/core/manifest"
	"github.com/nerolabs/silt/internal/safe"
	"github.com/nerolabs/silt/ports"
)

// frameOverhead is the envelope wrapping around a chunk in one frame: the
// sender's From/Addr/Relay, gossiped Contacts, the storage proof path, and
// CBOR structure. It is tiny next to a chunk (a proof path is O(log n)
// hashes) — a few MiB is generous headroom.
const frameOverhead = 4 << 20

// maxFrame bounds an inbound frame: large enough to carry the biggest legal
// chunk (a frame carries at most one chunk) plus its envelope, small enough
// to still cap per-frame allocation against a hostile peer (#14). It is
// derived from the manifest chunk-size ceiling, not a standalone number, so
// the transport can always carry a chunk the manifest layer accepts — the
// two limits can't drift apart (#104). The minimum production chunk is 64
// MiB, so the old 32 MiB cap silently dropped every production chunk.
const maxFrame = manifest.MaxChunkSize + frameOverhead

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

	mu sync.Mutex
	// peers is the address book: up to two addresses per peer, one per
	// form. A NATed peer advertises a relay form to the world, but a
	// LAN-mate that heard its mDNS beacon holds a direct address too —
	// keeping both lets the dialer prefer the cheap path and fall back,
	// instead of one form clobbering the other (the old one-slot book).
	peers map[ports.NodeID]addrPair
	// relays records peers that gossip a relay *service* (they run
	// -relay at that host:port) — the pool a NATed node can lean on
	// without being handed -relay-via. First-hand only: a node stamps
	// its own service, never someone else's, so an entry is exactly as
	// trustworthy as the pinned conn it arrived on (and dialing pins the
	// relay's identity anyway).
	relays map[ports.NodeID]string
	// conns holds the live conversation per peer, either direction.
	// The newest conn wins the slot; a displaced one keeps serving its
	// own readLoop until it dies naturally.
	conns map[ports.NodeID]*peerConn
	// adv, when set, replaces listenAddr in outgoing envelope stamps — a
	// NATed node advertising "reach me via relay R" instead of a
	// LAN address nobody outside the house can dial.
	adv string
	// relaySvc, when set, is the relay service this node offers
	// (-relay), stamped on outgoing envelopes so peers can discover
	// relays instead of being configured with one.
	relaySvc string

	// lg narrates transport failures (dials, handshakes, forgeries) —
	// exactly the events that are invisible-but-fatal across real
	// networks. nil = off.
	lg ports.Logger

	// requestPunch asks our relay to coordinate a hole-punch with a peer we
	// currently reach through the relay (wired to relay.Client.RequestPunch by
	// the daemon; nil if we run no relay client). punchedAt rate-limits those
	// requests per peer so a busy relay path doesn't spam them (#27).
	requestPunch func(ports.NodeID)
	punchedAt    map[ports.NodeID]time.Time
}

var _ ports.Transport = (*Transport)(nil)

// addrPair is one peer's two possible addresses: a directly dialable
// host:port and a relay:R@host:port form. Either may be empty.
type addrPair struct {
	direct string
	relay  string
}

func (p addrPair) empty() bool { return p.direct == "" && p.relay == "" }

// gossip picks the form worth telling a third party about. A relay
// form exists only because the peer itself advertised it — meaning it
// believes it is NATed, and any direct address we hold is LAN-scoped
// (mDNS) or stale. The relay form is the one that dials from anywhere.
func (p addrPair) gossip() string {
	if p.relay != "" {
		return p.relay
	}
	return p.direct
}

// isRelayForm reports whether addr is a relay:R@host:port address.
func isRelayForm(addr string) bool { return strings.HasPrefix(addr, "relay:") }

// peerConn is one live connection. Frames from concurrent senders are
// serialized by wmu so the length-prefixed framing can never interleave.
type peerConn struct {
	conn *tls.Conn
	wmu  sync.Mutex
	// viaRelay marks a conn that rides the relay splice (either we dialed
	// the peer's relay form, or the peer reached us through the relay). A
	// relay conn, once established, is reused for every subsequent frame —
	// so the hole-punch upgrade (#27) must be triggered on that reuse, not
	// only at dial time, or a steady-state relay path never tries to go
	// direct. Set once at adopt; a later direct (punched) conn replaces the
	// slot with a fresh peerConn whose viaRelay is false.
	viaRelay bool
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
		peers:      make(map[ports.NodeID]addrPair),
		relays:     make(map[ports.NodeID]string),
		conns:      make(map[ports.NodeID]*peerConn),
		punchedAt:  make(map[ports.NodeID]time.Time),
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

func (t *Transport) relayService() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.relaySvc
}

func isWildcard(hostport string) bool {
	host, _, err := net.SplitHostPort(hostport)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsUnspecified()
}

// AddPeer seeds the address book (bootstrap wiring). The address lands
// in the slot matching its form; the other slot survives.
func (t *Transport) AddPeer(id ports.NodeID, addr string) {
	t.learn(id, addr, true)
}

// Peers snapshots the address book, sorted for deterministic output —
// this is what gets persisted for warm restarts (discovery). A peer
// with both a direct and a relay address yields two entries; loading
// them back through AddPeer refills both slots.
func (t *Transport) Peers() []Peer {
	t.mu.Lock()
	out := make([]Peer, 0, len(t.peers))
	for id, pair := range t.peers {
		if pair.direct != "" {
			out = append(out, Peer{ID: id, Addr: pair.direct})
		}
		if pair.relay != "" {
			out = append(out, Peer{ID: id, Addr: pair.relay})
		}
	}
	t.mu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		if out[i].ID != out[j].ID {
			return out[i].ID.String() < out[j].ID.String()
		}
		return out[i].Addr < out[j].Addr
	})
	return out
}

// PeerCount is the number of DISTINCT peers in the address book. Use
// this, not len(Peers()): Peers() emits one entry per address form, so
// a peer known by both a direct and a relay address counts twice there.
func (t *Transport) PeerCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.peers)
}

func (t *Transport) lookupAddrs(id ports.NodeID) addrPair {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.peers[id]
}

func (t *Transport) learn(id ports.NodeID, addr string, overwrite bool) {
	if addr == "" {
		return
	}
	t.mu.Lock()
	pair := t.peers[id]
	if isRelayForm(addr) {
		if overwrite || pair.relay == "" {
			pair.relay = addr
		}
	} else if overwrite || pair.direct == "" {
		pair.direct = addr
	}
	t.peers[id] = pair
	t.mu.Unlock()
}

// forgetDirect drops a direct address proven stale — called only when a
// direct dial failed AND the relay fallback reached the peer, so "the
// peer moved behind a NAT" is distinguished from "the peer is down".
func (t *Transport) forgetDirect(id ports.NodeID, addr string) {
	t.mu.Lock()
	if pair := t.peers[id]; pair.direct == addr {
		pair.direct = ""
		t.peers[id] = pair
	}
	t.mu.Unlock()
}

// SetRelayService announces on every outgoing envelope that this node
// offers relay service at hostport (the -relay listener, in a publicly
// dialable form). "" stops the announcement.
func (t *Transport) SetRelayService(hostport string) {
	t.mu.Lock()
	t.relaySvc = hostport
	t.mu.Unlock()
}

// KnownRelays lists peers heard first-hand offering relay service, as
// ID + service host:port — each usable exactly where a -relay-via
// RELAYID@HOST:PORT value would be. Sorted for determinism.
func (t *Transport) KnownRelays() []Peer {
	t.mu.Lock()
	out := make([]Peer, 0, len(t.relays))
	for id, addr := range t.relays {
		out = append(out, Peer{ID: id, Addr: addr})
	}
	t.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].ID.String() < out[j].ID.String() })
	return out
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
	pair := t.lookupAddrs(to)
	if freshDial {
		// The dial-back verdict is about DIRECT dialability: reaching
		// the checker through a relay is precisely not being public, so
		// only its direct address counts here.
		pair.relay = ""
	}
	if pair.empty() && (freshDial || t.liveConn(to) == nil) {
		t.logf(ports.LogDebug, "send with no known address", "to", to)
		return fmt.Errorf("tcpnet: no known address for %s", to)
	}
	env := envelope{From: t.self[:], Addr: t.advertised(), Relay: t.relayService(), Msg: toWire(msg)}
	contacts := make(map[string]string)
	provIDs := make([]ports.NodeID, 0, len(msg.ProviderRecs))
	for _, r := range msg.ProviderRecs { // H5: provider records carry the IDs now
		provIDs = append(provIDs, r.ID)
	}
	for _, list := range [][]ports.NodeID{msg.Nodes, msg.Providers, provIDs} {
		for _, id := range list {
			if a := t.lookupAddrs(id).gossip(); a != "" {
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
	// Fail loudly rather than emit a frame the peer's read loop will drop
	// on sight (a silent-loss shape S1/S3 forbids). The cap is the same one
	// the receiver enforces.
	if len(frame) > maxFrame {
		return fmt.Errorf("tcpnet: frame of %d bytes exceeds max %d", len(frame), maxFrame)
	}
	go t.deliver(to, pair, frame, freshDial)
	return nil
}

// deliver rides the live conversation with to when one exists — this is
// what lets a NATed peer be answered: it dialed us, that socket is
// open, and the reply belongs on it. Otherwise it dials and keeps the
// conn: its readLoop serves the peer's frames and future sends skip the
// dial+handshake toll. The direct address is tried before the relay
// form — direct is the cheap path (no third hop) and usually right on a
// LAN — and a direct failure falls back to the relay in the same
// delivery; if the relay then reaches the peer, the direct address was
// stale (the peer moved behind a NAT) and is dropped from the book.
func (t *Transport) deliver(to ports.NodeID, pair addrPair, frame []byte, freshDial bool) {
	if !freshDial {
		if pc := t.liveConn(to); pc != nil {
			if pc.write(frame) == nil {
				// A relay conn is reused for every frame, so this is the only
				// place a steady-state relay path can be nudged toward a direct
				// link. Cooldown-gated, so it's at most one request per peer per
				// interval regardless of traffic (#27).
				if pc.viaRelay {
					t.maybeRequestPunch(to)
				}
				return
			}
			t.dropConn(to, pc) // conversation died; try a fresh dial
		}
	}
	if pair.empty() {
		t.logf(ports.LogDebug, "no path to peer", "to", to)
		return
	}
	directFailed := false
	for _, addr := range []string{pair.direct, pair.relay} {
		if addr == "" {
			continue
		}
		conn, err := t.dialPeer(to, addr)
		if err != nil {
			directFailed = directFailed || addr == pair.direct
			continue
		}
		if directFailed && addr == pair.relay {
			t.forgetDirect(to, pair.direct)
		}
		viaRelay := addr == pair.relay
		pc := t.adopt(to, conn, viaRelay)
		if err := pc.write(frame); err != nil {
			t.dropConn(to, pc)
			return
		}
		go t.readLoop(conn, viaRelay)
		if viaRelay {
			// We reached the peer through the relay; try to upgrade to a direct
			// link so the bulk traffic leaves the relay (#27). Harmless if it
			// fails — this relay conn keeps serving.
			t.maybeRequestPunch(to)
		}
		return
	}
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
func (t *Transport) adopt(id ports.NodeID, conn *tls.Conn, viaRelay bool) *peerConn {
	t.mu.Lock()
	defer t.mu.Unlock()
	if pc, ok := t.conns[id]; ok && pc.conn == conn {
		return pc
	}
	pc := &peerConn{conn: conn, viaRelay: viaRelay}
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
	// Inbound through the relay splice: mark the conn relay-backed so reusing
	// it triggers the hole-punch upgrade (#27).
	t.readLoop(tls.Server(raw, &tls.Config{
		Certificates: []tls.Certificate{t.cert},
		ClientAuth:   tls.RequireAnyClientCert,
		MinVersion:   tls.VersionTLS13,
	}), true)
}

func (t *Transport) acceptLoop() {
	for {
		conn, err := t.ln.Accept()
		if err != nil {
			return // listener closed
		}
		go t.readLoop(conn.(*tls.Conn), false) // direct inbound
	}
}

func (t *Transport) readLoop(conn *tls.Conn, viaRelay bool) {
	// A malformed frame from a peer must fail this conversation, not the
	// node (Gate 1 / anti-persona #14). The decoders below are panic-free
	// by construction — the fuzz targets prove it — but this guard is the
	// net underneath: a panic anywhere in the read path drops the conn
	// instead of unwinding into the runtime and killing every other peer's
	// session too.
	defer safe.Guard(func(r any) {
		t.logf(ports.LogWarn, "recovered panic in read loop", "remote", conn.RemoteAddr(), "panic", r)
		conn.Close()
	})
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
	pc := t.adopt(from, conn, viaRelay)
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
		if env.Relay != "" {
			// First-hand relay-capability gossip: from itself offers
			// relay service there. Recorded for the moment this node
			// finds itself NATed with no -relay-via configured.
			t.mu.Lock()
			t.relays[from] = env.Relay
			t.mu.Unlock()
		}
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
