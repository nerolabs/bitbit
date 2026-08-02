package tcpnet

import (
	"bytes"
	"crypto/tls"
	"net"
	"time"

	"github.com/nerolabs/silt/adapters/identity"
	"github.com/nerolabs/silt/internal/reuseport"
	"github.com/nerolabs/silt/ports"
)

// punchCooldown throttles how often we ask the relay to coordinate a punch
// with the same peer, so a stream of relayed frames doesn't spam requests.
const punchCooldown = 30 * time.Second

// SetRequestPunch wires the relay's punch-request path (relay.Client.RequestPunch).
// Without it, the transport simply never tries to upgrade a relay to a direct
// link — it just keeps relaying. Set by the daemon when it leans on a relay.
func (t *Transport) SetRequestPunch(fn func(ports.NodeID)) { t.requestPunch = fn }

// maybeRequestPunch asks the relay to coordinate a hole-punch with `to`, at
// most once per cooldown. Called after we reach a peer through the relay: if
// the punch lands, future frames go direct; if not, nothing is lost.
func (t *Transport) maybeRequestPunch(to ports.NodeID) {
	if t.requestPunch == nil || !reuseport.Supported {
		return
	}
	t.mu.Lock()
	last, seen := t.punchedAt[to]
	if seen && time.Since(last) < punchCooldown {
		t.mu.Unlock()
		return
	}
	t.punchedAt[to] = time.Now()
	t.mu.Unlock()
	go t.requestPunch(to)
}

// HolePunch attempts a DIRECT connection to peer at its observed endpoint
// peerAddr, dialing from localPort (the relay-registration port, reused so the
// NAT reuses the mapping the relay observed) while the peer does the same — a
// coordinated TCP simultaneous-open (#27). The crossing SYNs establish a direct
// path through both cone NATs; on symmetric NAT this just fails and the relay
// path stays. On success the direct conn is adopted (via the normal read loop),
// so subsequent sends to peer bypass the relay. This is the onPunch callback
// the relay client fires.
func (t *Transport) HolePunch(peer ports.NodeID, peerAddr string, localPort int) {
	if !reuseport.Supported || peerAddr == "" || localPort == 0 || peer == t.self {
		return
	}
	d := net.Dialer{
		Timeout:   3 * time.Second,
		LocalAddr: &net.TCPAddr{Port: localPort},
		Control:   reuseport.Control,
	}
	var raw net.Conn
	var err error
	for i := 0; i < 15; i++ { // keep firing SYNs until they cross (or give up)
		if raw, err = d.Dial("tcp", peerAddr); err == nil {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if err != nil {
		t.logf(ports.LogDebug, "hole-punch failed; staying on relay", "peer", peer, "addr", peerAddr)
		return
	}
	// Both sides dialed (connect), so assign the TLS roles deterministically by
	// NodeID — lower id is the server — and let the normal read loop run the
	// handshake, verify the peer's pinned identity, and adopt the conn.
	var conn *tls.Conn
	if bytes.Compare(t.self[:], peer[:]) < 0 {
		conn = tls.Server(raw, &tls.Config{
			Certificates: []tls.Certificate{t.cert},
			ClientAuth:   tls.RequireAnyClientCert,
			MinVersion:   tls.VersionTLS13,
		})
	} else {
		conn = tls.Client(raw, identity.ClientConfig(t.cert, peer))
	}
	t.logf(ports.LogInfo, "hole-punch: direct connection established", "peer", peer, "addr", peerAddr)
	t.readLoop(conn, false) // direct now; adopt replaces the relay conn so reuse stops requesting punches
}
