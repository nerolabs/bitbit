// Package lan discovers peers on the same local network with zero
// configuration, over link-local multicast UDP.
//
// It is the cheapest rung of the discovery ladder (see
// docs/design/cross-network.md): two silt nodes on one LAN find each
// other with no -bootstrap, no DNS seed, and no public infrastructure at
// all. The mechanism is the same idea as mDNS/DNS-SD — periodic multicast
// announcements, scoped to the link — but purpose-built for silt rather
// than the full DNS-SD wire format: a node multicasts its own peer string
// ("ID@host:port") every few seconds and listens for others'. Because a
// peer string is self-authenticating (the TLS handshake must present a key
// hashing to ID), a multicast announcement can direct you to a node but can
// never impersonate one — a hostile beacon just wastes a dial.
//
// Scope: announcements go to an administratively-scoped multicast group at
// the default multicast TTL of 1, so routers never forward them off the
// link. This finds peers in the same house; it does nothing across the
// internet (that is the relay's job).
package lan

import (
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/nerolabs/silt/adapters/discovery"
	"github.com/nerolabs/silt/adapters/tcpnet"
)

const (
	// group is administratively scoped (239.0.0.0/8); the default
	// multicast TTL of 1 keeps every packet on the local link.
	group = "239.255.90.90:5354"
	// magic prefixes every packet so we never mistake unrelated multicast
	// traffic on the same group for a silt announcement.
	magic     = "silt-lan/1 "
	interval  = 5 * time.Second
	maxPacket = 512
)

// Beacon announces this node on the local network and reports the peers it
// hears. Construct with New, then Start; Close stops both directions.
type Beacon struct {
	self   tcpnet.Peer
	onPeer func(tcpnet.Peer)

	stop chan struct{}
	once sync.Once
	recv *net.UDPConn
	send *net.UDPConn

	mu   sync.Mutex
	seen map[tcpnet.Peer]bool // suppress repeat reports of an unchanged peer
}

// New builds a beacon that advertises self and calls onPeer once for each
// distinct peer (by ID+address) it discovers. onPeer runs on the beacon's
// own goroutine — hop back onto your event loop inside it.
func New(self tcpnet.Peer, onPeer func(tcpnet.Peer)) *Beacon {
	return &Beacon{
		self:   self,
		onPeer: onPeer,
		stop:   make(chan struct{}),
		seen:   make(map[tcpnet.Peer]bool),
	}
}

// Start joins the multicast group and begins announcing. It returns an
// error if the group can't be joined (e.g. no multicast-capable interface).
func (b *Beacon) Start() error {
	gaddr, err := net.ResolveUDPAddr("udp4", group)
	if err != nil {
		return fmt.Errorf("lan: resolve group: %w", err)
	}
	recv, err := net.ListenMulticastUDP("udp4", nil, gaddr)
	if err != nil {
		return fmt.Errorf("lan: join group: %w", err)
	}
	_ = recv.SetReadBuffer(maxPacket * 16)
	send, err := net.DialUDP("udp4", nil, gaddr)
	if err != nil {
		recv.Close()
		return fmt.Errorf("lan: open sender: %w", err)
	}
	b.recv, b.send = recv, send
	go b.readLoop()
	go b.announceLoop()
	return nil
}

// Close stops the beacon. It is safe to call more than once.
func (b *Beacon) Close() {
	b.once.Do(func() {
		close(b.stop)
		if b.recv != nil {
			b.recv.Close()
		}
		if b.send != nil {
			b.send.Close()
		}
	})
}

func (b *Beacon) announceLoop() {
	pkt := marshal(b.self)
	b.send.Write(pkt) // announce immediately, don't wait a full interval
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-b.stop:
			return
		case <-t.C:
			if _, err := b.send.Write(pkt); err != nil {
				return // socket closed
			}
		}
	}
}

func (b *Beacon) readLoop() {
	buf := make([]byte, maxPacket)
	for {
		n, _, err := b.recv.ReadFromUDP(buf)
		if err != nil {
			select {
			case <-b.stop:
				return // closed by us
			default:
				return // socket error; stop reading
			}
		}
		p, ok := parse(buf[:n])
		if !ok || p.ID == b.self.ID {
			continue // not ours, malformed, or our own echo
		}
		b.mu.Lock()
		fresh := !b.seen[p]
		b.seen[p] = true
		b.mu.Unlock()
		if fresh {
			b.onPeer(p)
		}
	}
}

// marshal renders an announcement packet: the magic prefix and the peer
// string.
func marshal(self tcpnet.Peer) []byte {
	return []byte(magic + discovery.Format(self))
}

// parse reads an announcement packet back into a peer, reporting ok=false
// for anything that isn't a well-formed silt announcement.
func parse(pkt []byte) (tcpnet.Peer, bool) {
	rest, ok := strings.CutPrefix(string(pkt), magic)
	if !ok {
		return tcpnet.Peer{}, false
	}
	p, err := discovery.Parse(strings.TrimSpace(rest))
	if err != nil {
		return tcpnet.Peer{}, false
	}
	return p, true
}

// AdvertiseAddr turns the transport's bound listen address into the
// host:port a LAN peer should dial. A loopback-only bind can't be reached
// across the LAN, so it returns an error explaining how to fix it; an
// unspecified bind (0.0.0.0 / "") is resolved to this host's LAN IPv4; a
// concrete address is used as-is.
func AdvertiseAddr(listenAddr string) (string, error) {
	host, port, err := net.SplitHostPort(listenAddr)
	if err != nil {
		return "", fmt.Errorf("lan: %q: %w", listenAddr, err)
	}
	ip := net.ParseIP(host)
	if ip != nil && ip.IsLoopback() {
		return "", fmt.Errorf("bound to loopback %s — listen on 0.0.0.0 to be LAN-discoverable", host)
	}
	if ip == nil || ip.IsUnspecified() {
		lan, err := lanIPv4()
		if err != nil {
			return "", err
		}
		return net.JoinHostPort(lan.String(), port), nil
	}
	return net.JoinHostPort(host, port), nil
}

// lanIPv4 returns this host's first non-loopback, non-link-local IPv4
// address on an interface that is up.
func lanIPv4() (net.IP, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	for _, ifi := range ifaces {
		if ifi.Flags&net.FlagUp == 0 || ifi.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifi.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			var ip net.IP
			switch v := a.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			ip4 := ip.To4()
			if ip4 == nil || ip4.IsLoopback() || ip4.IsLinkLocalUnicast() {
				continue
			}
			return ip4, nil
		}
	}
	return nil, fmt.Errorf("lan: no non-loopback IPv4 interface found")
}
