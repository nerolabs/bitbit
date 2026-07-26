// Package httpregistry puts the registry on the network: a Server that
// exposes any ports.Registry over plain HTTP/JSON, and a Client that
// implements ports.Registry against such a server. This is what lets
// separate PROCESSES share the v1 "single honest instance" — one daemon
// hosts it, everyone else dials it. The interface stays the seam: a
// chain-backed registry would replace the server, and no client would
// know.
//
// No TLS, no auth — same trusted-network stance as tcpnet, stated out
// loud.
package httpregistry

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/nerolabs/silt/adapters/identity"
	"github.com/nerolabs/silt/ports"
)

// entryJSON is the wire form (hex strings, greppable), mirroring
// fileregistry's on-disk format.
type entryJSON struct {
	Root           string   `json:"root"`
	ManifestChunks []string `json:"manifest_chunks"`
	FileSize       int64    `json:"file_size"`
	Publisher      string   `json:"publisher,omitempty"`
}

func toJSON(e ports.Entry) entryJSON {
	ej := entryJSON{Root: e.Root.String(), FileSize: e.FileSize}
	for _, id := range e.ManifestChunks {
		ej.ManifestChunks = append(ej.ManifestChunks, id.String())
	}
	if e.Publisher != (ports.NodeID{}) {
		ej.Publisher = e.Publisher.String()
	}
	return ej
}

func fromJSON(ej entryJSON) (ports.Entry, error) {
	root, err := ports.ParseHash(ej.Root)
	if err != nil {
		return ports.Entry{}, err
	}
	e := ports.Entry{Root: root, FileSize: ej.FileSize}
	for _, s := range ej.ManifestChunks {
		id, err := ports.ParseHash(s)
		if err != nil {
			return ports.Entry{}, err
		}
		e.ManifestChunks = append(e.ManifestChunks, id)
	}
	if ej.Publisher != "" {
		if e.Publisher, err = ports.ParseHash(ej.Publisher); err != nil {
			return ports.Entry{}, err
		}
	}
	return e, nil
}

// Serve exposes reg on addr over plain HTTP (tests, trusted loopback).
func Serve(addr string, reg ports.Registry) (boundAddr string, shutdown func(), err error) {
	return serve(addr, reg, nil)
}

// ServeTLS exposes reg over TLS with the daemon's identity certificate;
// clients pin the daemon's NodeID (NewPinnedClient) — same
// key-is-identity scheme as tcpnet, no CA anywhere.
func ServeTLS(addr string, ident *identity.Identity, reg ports.Registry) (boundAddr string, shutdown func(), err error) {
	cert, err := ident.Certificate()
	if err != nil {
		return "", nil, err
	}
	return serve(addr, reg, &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS13,
	})
}

func serve(addr string, reg ports.Registry, tlsCfg *tls.Config) (boundAddr string, shutdown func(), err error) {
	mux := http.NewServeMux()

	mux.HandleFunc("POST /publish", func(w http.ResponseWriter, r *http.Request) {
		var ej entryJSON
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&ej); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		e, err := fromJSON(ej)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		switch err := reg.Publish(r.Context(), e); {
		case err == nil:
			w.WriteHeader(http.StatusOK)
		case errors.Is(err, ports.ErrDupPublish):
			http.Error(w, err.Error(), http.StatusConflict)
		case errors.Is(err, ports.ErrInsufficientCredit):
			http.Error(w, err.Error(), http.StatusPaymentRequired)
		case errors.Is(err, ports.ErrPublisherRequired):
			http.Error(w, err.Error(), http.StatusBadRequest)
		default:
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})

	mux.HandleFunc("GET /lookup", func(w http.ResponseWriter, r *http.Request) {
		root, err := ports.ParseHash(r.URL.Query().Get("root"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		e, ok, err := reg.Lookup(r.Context(), root)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if !ok {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(toJSON(e))
	})

	mux.HandleFunc("GET /all", func(w http.ResponseWriter, r *http.Request) {
		entries, err := reg.All(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		out := make([]entryJSON, 0, len(entries))
		for _, e := range entries {
			out = append(out, toJSON(e))
		}
		json.NewEncoder(w).Encode(out)
	})

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return "", nil, err
	}
	if tlsCfg != nil {
		ln = tls.NewListener(ln, tlsCfg)
	}
	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)
	return ln.Addr().String(), func() { srv.Close() }, nil
}

// Client implements ports.Registry against a Serve endpoint.
type Client struct {
	base string
	hc   *http.Client
}

var _ ports.Registry = (*Client)(nil)

func NewClient(baseURL string) *Client {
	return &Client{base: baseURL, hc: &http.Client{Timeout: 10 * time.Second}}
}

// NewPinnedClient talks https to a registry hosted by the daemon whose
// NodeID is expect; any other key at that address fails the handshake.
func NewPinnedClient(baseURL string, expect ports.NodeID) *Client {
	return &Client{base: baseURL, hc: &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{
			InsecureSkipVerify:    true, // replaced by pinning, not skipped
			VerifyPeerCertificate: identity.VerifyExpected(expect),
			MinVersion:            tls.VersionTLS13,
		}},
	}}
}

func (c *Client) Publish(ctx context.Context, e ports.Entry) error {
	body, err := json.Marshal(toJSON(e))
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", c.base+"/publish", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("httpregistry publish: %w", err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusConflict:
		return ports.ErrDupPublish
	case http.StatusPaymentRequired:
		return ports.ErrInsufficientCredit
	default:
		msg := bytes.TrimSpace(mustRead(resp.Body))
		// The classic first-run mistake: http:// against a pinned HTTPS
		// registry. The server says so; translate it into the fix.
		if bytes.Contains(msg, []byte("HTTP request to an HTTPS server")) {
			return fmt.Errorf("httpregistry publish: this registry is key-pinned HTTPS — use the ID@https://host:port ref the daemon prints, not http://")
		}
		return fmt.Errorf("httpregistry publish: %s: %s", resp.Status, msg)
	}
}

func mustRead(r io.Reader) []byte {
	b, _ := io.ReadAll(io.LimitReader(r, 4096))
	return b
}

func (c *Client) Lookup(ctx context.Context, root ports.Hash) (ports.Entry, bool, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.base+"/lookup?root="+root.String(), nil)
	if err != nil {
		return ports.Entry{}, false, err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return ports.Entry{}, false, fmt.Errorf("httpregistry lookup: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return ports.Entry{}, false, nil
	}
	if resp.StatusCode != http.StatusOK {
		return ports.Entry{}, false, fmt.Errorf("httpregistry lookup: %s", resp.Status)
	}
	var ej entryJSON
	if err := json.NewDecoder(resp.Body).Decode(&ej); err != nil {
		return ports.Entry{}, false, err
	}
	e, err := fromJSON(ej)
	return e, err == nil, err
}

func (c *Client) All(ctx context.Context) ([]ports.Entry, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.base+"/all", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("httpregistry all: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("httpregistry all: %s", resp.Status)
	}
	var ejs []entryJSON
	if err := json.NewDecoder(resp.Body).Decode(&ejs); err != nil {
		return nil, err
	}
	entries := make([]ports.Entry, 0, len(ejs))
	for _, ej := range ejs {
		e, err := fromJSON(ej)
		if err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, nil
}
