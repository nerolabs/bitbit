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
//   - Concurrency. Sockets mean goroutines; core code is lock-free and
//     single-threaded by contract. Every delivery is posted onto the
//     node's event loop, never invoked from a reader goroutine.
//
// Loss semantics are UDP-ish on purpose: Send never blocks and dial,
// handshake, or write failures just drop the message — the core's
// timeout machinery already knows how to live in that world.
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

	// lg narrates transport failures (dials, handshakes, forgeries) —
	// exactly the events that are invisible-but-fatal across real
	// networks. nil = off.
	lg ports.Logger
}

var _ ports.Transport = (*Transport)(nil)

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
	}
	go t.acceptLoop()
	return t, nil
}

func (t *Transport) Addr() string       { return t.listenAddr }
func (t *Transport) Self() ports.NodeID { return t.self }
func (t *Transport) Close() error       { return t.ln.Close() }

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
	if t.lg != nil && t.lg.Enabled(lvl) {
		t.lg.Log(lvl, event, kv...)
	}
}

// Send resolves the address, builds the envelope, and hands the
// dial+handshake+write to a goroutine so the loop never blocks.
func (t *Transport) Send(to ports.NodeID, msg ports.Message) error {
	addr, ok := t.lookupAddr(to)
	if !ok {
		t.logf(ports.LogDebug, "send with no known address", "to", to)
		return fmt.Errorf("tcpnet: no known address for %s", to)
	}
	env := envelope{From: t.self[:], Addr: t.listenAddr, Msg: toWire(msg)}
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
	go t.writeFrame(to, addr, frame)
	return nil
}

// writeFrame dials with the target's identity pinned: if the far end's
// key doesn't hash to the NodeID we meant, the handshake fails and the
// message is dropped — impostors get silence, not data.
func (t *Transport) writeFrame(to ports.NodeID, addr string, frame []byte) {
	cfg := &tls.Config{
		Certificates:          []tls.Certificate{t.cert},
		InsecureSkipVerify:    true, // replaced by pinning, not skipped
		VerifyPeerCertificate: identity.VerifyExpected(to),
		MinVersion:            tls.VersionTLS13,
	}
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 2 * time.Second}, "tcp", addr, cfg)
	if err != nil {
		t.logf(ports.LogWarn, "dial failed", "to", to, "addr", addr, "err", err)
		return // dropped; timeouts upstream handle it
	}
	defer conn.Close()
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(frame)))
	conn.Write(hdr[:])
	conn.Write(frame)
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
	defer conn.Close()
	if err := conn.Handshake(); err != nil {
		t.logf(ports.LogDebug, "inbound handshake failed", "remote", conn.RemoteAddr(), "err", err)
		return
	}
	// The sender's identity comes from the TLS handshake, not from
	// anything it writes in a frame.
	from, err := identity.PeerID(conn.ConnectionState())
	if err != nil {
		t.logf(ports.LogDebug, "inbound peer id rejected", "remote", conn.RemoteAddr(), "err", err)
		return
	}
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
