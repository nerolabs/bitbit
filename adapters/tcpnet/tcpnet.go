// Package tcpnet is the real-network Transport: length-prefixed CBOR
// frames over TCP. This adapter is the HANDOFF's bet made good — the
// swap from simnet to real sockets touches zero core logic.
//
// Two realities of leaving the sim are handled here, both invisibly to
// the core:
//
//   - Addressing. Core speaks pure NodeIDs; TCP needs ip:port. The
//     adapter keeps an address book, stamps every outgoing frame with
//     the sender's own listen address, and attaches known addresses for
//     any NodeIDs mentioned in the message (the Nodes/Providers lists).
//     Receivers learn as they listen — this is why real Kademlia
//     messages carry contact info, done here as an envelope concern.
//   - Concurrency. Sockets mean goroutines; core code is lock-free and
//     single-threaded by contract. Every delivery is posted onto the
//     node's event loop, never invoked from a reader goroutine.
//
// Loss semantics are UDP-ish on purpose: Send never blocks and dial or
// write failures just drop the message — the core's timeout machinery
// already knows how to live in that world, because the sim taught it.
// No TLS, no auth, no framing versioning: this is a demo transport for
// a trusted network, and says so out loud.
package tcpnet

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/fxamacker/cbor/v2"

	"github.com/nerolabs/bitbit/adapters/eventloop"
	"github.com/nerolabs/bitbit/ports"
)

const maxFrame = 32 << 20 // sanity cap; a frame carries at most one chunk

type Transport struct {
	loop       *eventloop.Loop
	self       ports.NodeID
	listenAddr string
	ln         net.Listener
	handler    func(from ports.NodeID, msg ports.Message)
	peers      map[ports.NodeID]string // address book; touched only on the loop
}

var _ ports.Transport = (*Transport)(nil)

// New starts listening on listenAddr (use "127.0.0.1:0" for an
// ephemeral port; Addr() reports what was bound).
func New(loop *eventloop.Loop, self ports.NodeID, listenAddr string) (*Transport, error) {
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return nil, fmt.Errorf("tcpnet: %w", err)
	}
	t := &Transport{
		loop:       loop,
		self:       self,
		listenAddr: ln.Addr().String(),
		ln:         ln,
		peers:      make(map[ports.NodeID]string),
	}
	go t.acceptLoop()
	return t, nil
}

func (t *Transport) Addr() string { return t.listenAddr }

func (t *Transport) Close() error { return t.ln.Close() }

// AddPeer seeds the address book (bootstrap wiring).
func (t *Transport) AddPeer(id ports.NodeID, addr string) {
	t.loop.Post(func() { t.peers[id] = addr })
}

func (t *Transport) SetHandler(h func(from ports.NodeID, msg ports.Message)) {
	t.handler = h
}

// Send runs on the loop (core's thread). It resolves the address,
// builds the envelope, then hands the actual dial+write to a goroutine
// so the loop never blocks on the network.
func (t *Transport) Send(to ports.NodeID, msg ports.Message) error {
	addr, ok := t.peers[to]
	if !ok {
		return fmt.Errorf("tcpnet: no known address for %s", to)
	}
	env := envelope{From: t.self[:], Addr: t.listenAddr, Msg: toWire(msg)}
	contacts := make(map[string]string)
	for _, list := range [][]ports.NodeID{msg.Nodes, msg.Providers} {
		for _, id := range list {
			if a, known := t.peers[id]; known {
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
	go writeFrame(addr, frame)
	return nil
}

func writeFrame(addr string, frame []byte) {
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
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
		go t.readLoop(conn)
	}
}

func (t *Transport) readLoop(conn net.Conn) {
	defer conn.Close()
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
		if len(env.From) != len(ports.NodeID{}) {
			return
		}
		var from ports.NodeID
		copy(from[:], env.From)
		msg := fromWire(env.Msg)
		t.loop.Post(func() {
			if env.Addr != "" {
				t.peers[from] = env.Addr
			}
			for idHex, addr := range env.Contacts {
				if id, err := ports.ParseHash(idHex); err == nil {
					if _, known := t.peers[id]; !known {
						t.peers[id] = addr
					}
				}
			}
			if t.handler != nil {
				t.handler(from, msg)
			}
		})
	}
}
