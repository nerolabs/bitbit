// Package discovery finds peers the way Bitcoin does, in layers:
//
//  1. explicit seeds — the -bootstrap flag, peer strings you were told;
//  2. DNS seeds — TXT records under a well-known domain, each one a
//     peer string, so joining the network needs only a domain name;
//  3. peer exchange — tcpnet envelopes already gossip contacts; this
//     package persists the learned address book to disk so a restarted
//     daemon rejoins warm, without any flags at all.
//
// A peer string is self-authenticating: "ID@host:port", where the TLS
// handshake must present a key hashing to ID (see adapters/identity).
// A DNS server can therefore direct you but never impersonate anyone.
package discovery

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/nerolabs/silt/adapters/tcpnet"
	"github.com/nerolabs/silt/ports"
)

// Parse reads one "ID@addr" peer string. The split is on the FIRST "@":
// an ID is fixed-form hex and can never contain one, but an addr can —
// a relay-form address ("relay:RELAYID@host:port") carries its own "@",
// and splitting on the last one would shear it in half.
func Parse(s string) (tcpnet.Peer, error) {
	at := strings.Index(s, "@")
	if at < 0 {
		return tcpnet.Peer{}, fmt.Errorf("discovery: peer %q: want ID@HOST:PORT", s)
	}
	id, err := ports.ParseHash(strings.TrimSpace(s[:at]))
	if err != nil {
		return tcpnet.Peer{}, fmt.Errorf("discovery: peer %q: %w", s, err)
	}
	return tcpnet.Peer{ID: id, Addr: strings.TrimSpace(s[at+1:])}, nil
}

// Format renders a peer string.
func Format(p tcpnet.Peer) string { return p.ID.String() + "@" + p.Addr }

// ParseList reads a comma-separated peer list, skipping blanks.
func ParseList(s string) ([]tcpnet.Peer, error) {
	var out []tcpnet.Peer
	for _, part := range strings.Split(s, ",") {
		if strings.TrimSpace(part) == "" {
			continue
		}
		p, err := Parse(part)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// LoadFile reads a persisted address book (JSON array of peer strings).
// A missing file is an empty book, not an error.
func LoadFile(path string) ([]tcpnet.Peer, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var strs []string
	if err := json.Unmarshal(raw, &strs); err != nil {
		return nil, fmt.Errorf("discovery: %s: %w", path, err)
	}
	var out []tcpnet.Peer
	for _, s := range strs {
		p, err := Parse(s)
		if err != nil {
			continue // a corrupt line loses one peer, not the book
		}
		out = append(out, p)
	}
	return out, nil
}

// SaveFile persists the address book atomically.
func SaveFile(path string, peers []tcpnet.Peer) error {
	strs := make([]string, len(peers))
	for i, p := range peers {
		strs[i] = Format(p)
	}
	raw, err := json.MarshalIndent(strs, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// FromDNS resolves a DNS seed: every TXT record at the domain that
// parses as a peer string is a bootstrap candidate.
func FromDNS(domain string) ([]tcpnet.Peer, error) {
	txts, err := net.LookupTXT(domain)
	if err != nil {
		return nil, fmt.Errorf("discovery: dns seed %s: %w", domain, err)
	}
	return ParseTXT(txts), nil
}

// ParseTXT extracts peers from TXT record values (the testable half of
// FromDNS). Unparseable records are ignored — DNS is full of strangers.
func ParseTXT(records []string) []tcpnet.Peer {
	var out []tcpnet.Peer
	for _, r := range records {
		if p, err := Parse(r); err == nil {
			out = append(out, p)
		}
	}
	return out
}
