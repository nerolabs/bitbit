// Package relay is the universal NAT fallback (#27, cross-network
// design 2c): when two peers are both behind home routers, neither can
// accept the other's inbound connection — but both can make outbound
// ones, so they meet at a third node that is reachable.
//
// The shape (libp2p Circuit Relay v2's, without the dependency):
//
//	NATed B ──standing control conn──► Relay R ◄──per-message dial── S
//
//	1. B registers: one outbound TLS conn to R, kept alive with pings.
//	   B advertises "relay:R@host:port" as its address.
//	2. S wants B: dials R, sends connect{B}. R tells B over the control
//	   conn: incoming{stream}. R parks S's conn.
//	3. B dials a SECOND outbound conn to R: accept{stream}. R splices
//	   the two conns into one raw pipe and tells both ends "ok".
//	4. S runs its normal end-to-end TLS handshake with B THROUGH the
//	   pipe. This is the crucial part: Silt's security model says a
//	   frame's sender is whoever the TLS handshake authenticated, and
//	   that must hold across a relay. It does, because the relay only
//	   ever carries S↔B TLS bytes it cannot read, alter, or forge —
//	   content-blind by construction, not by promise.
//
// The relay learns which two NodeIDs talked, when, and how much —
// metadata, the same the threat model already concedes to any on-path
// router. It never sees plaintext, and Silt payloads are ciphertext
// under yet another layer anyway.
//
// Relaying is a node capability (-relay), not special infrastructure:
// any daemon that is publicly reachable can offer it, capped so it
// can't be trivially drained. Nothing in the protocol privileges any
// particular relay, and no relay address is baked into the binary.
package relay

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"github.com/fxamacker/cbor/v2"

	"github.com/nerolabs/silt/ports"
)

// ctrl is the relay control frame: length-prefixed CBOR on the outer
// (peer↔relay) TLS connections. Once a splice is live the framing
// stops — everything after "ok" is the two peers' own raw bytes.
type ctrl struct {
	Op     string `cbor:"o"`           // register|connect|incoming|accept|ok|err|ping|pong
	Target []byte `cbor:"t,omitempty"` // connect: the NodeID being asked for
	Stream uint64 `cbor:"s,omitempty"` // incoming/accept: splice correlation id
	Err    string `cbor:"e,omitempty"` // err: human-readable refusal
}

const (
	maxCtrlFrame = 4096
	// ctrlIdle bounds how long a control conn may go silent before the
	// relay declares it dead. Clients ping every pingEvery, so a healthy
	// registration always beats this.
	ctrlIdle  = 90 * time.Second
	pingEvery = 25 * time.Second
	// opTimeout bounds one connect/accept exchange.
	opTimeout = 10 * time.Second
)

var encMode cbor.EncMode

func init() {
	opts := cbor.CanonicalEncOptions()
	em, err := opts.EncMode()
	if err != nil {
		panic(err)
	}
	encMode = em
}

func writeCtrl(conn net.Conn, c ctrl) error {
	b, err := encMode.Marshal(c)
	if err != nil {
		return err
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(b)))
	if _, err := conn.Write(hdr[:]); err != nil {
		return err
	}
	_, err = conn.Write(b)
	return err
}

func readCtrl(conn net.Conn) (ctrl, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(conn, hdr[:]); err != nil {
		return ctrl{}, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n == 0 || n > maxCtrlFrame {
		return ctrl{}, fmt.Errorf("relay: bad ctrl frame size %d", n)
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(conn, b); err != nil {
		return ctrl{}, err
	}
	var c ctrl
	if err := cbor.Unmarshal(b, &c); err != nil {
		return ctrl{}, err
	}
	return c, nil
}

// Addr renders the address-book form of "reach this peer through the
// relay relayID at hostport": relay:<relayID>@<hostport>. It goes
// wherever a direct host:port would — envelope stamps, peers.json — and
// tcpnet recognizes the prefix at dial time.
func Addr(relayID ports.NodeID, hostport string) string {
	return "relay:" + relayID.String() + "@" + hostport
}

// SplitAddr parses Addr's form; ok is false for direct addresses.
func SplitAddr(s string) (relayID ports.NodeID, hostport string, ok bool) {
	rest, found := strings.CutPrefix(s, "relay:")
	if !found {
		return relayID, "", false
	}
	at := strings.Index(rest, "@")
	if at < 0 {
		return relayID, "", false
	}
	id, err := ports.ParseHash(rest[:at])
	if err != nil || rest[at+1:] == "" {
		return relayID, "", false
	}
	return id, rest[at+1:], true
}
