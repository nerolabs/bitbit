package main

// The embedded web UI (M13): one Go binary, zero extra runtime. The
// daemon serves static pages (go:embed) plus a JSON API; all node-state
// reads post onto the daemon's event loop, and publish/fetch run
// through an in-process ephemeral swarm client so the daemon's storage
// pledge is never touched by UI operations.
//
// The local surface is LOCKED (Gate 1 / I1, #89): the API used to send
// CORS `*`, so any web page the operator visited could enumerate or drive
// their node. Now every request must arrive with a localhost Host (no DNS
// rebinding), any cross-origin request from a non-localhost page is
// refused outright, and every state-changing call must carry the
// per-daemon bearer token minted on first run. Read-only localhost
// ergonomics are preserved, including the observatory aggregating several
// local daemons from one browser tab (localhost origins are reflected, not
// blanket-allowed). Traces to Don't #3 (access-unsurveilled), B4 (privacy
// by construction), and S4 (no seizable single point).

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nerolabs/silt/adapters/eventloop"
	"github.com/nerolabs/silt/adapters/linkbook"
	"github.com/nerolabs/silt/core/crypto"
	"github.com/nerolabs/silt/core/link"
	"github.com/nerolabs/silt/core/node"
	"github.com/nerolabs/silt/core/pipeline"
	"github.com/nerolabs/silt/ports"
)

//go:embed ui
var uiFiles embed.FS

type uiServer struct {
	loop          *eventloop.Loop
	nd            *node.Node
	reg           ports.Registry // may be nil (no registry configured)
	capRep        ports.CapacityReporter
	selfPeer      string // "id@addr" for the ephemeral clients
	validator     bool
	started       time.Time
	peerCount     func() int
	links         *linkbook.Book // client mode only (nil on a plain daemon)
	carePublished bool           // daemon repairs content published through its own UI (#44)
	token         string         // per-daemon bearer token gating state-changing calls (#89)
}

func (s *uiServer) onLoop(fn func()) {
	ch := make(chan struct{})
	s.loop.Post(func() { fn(); close(ch) })
	select {
	case <-ch:
	case <-time.After(30 * time.Second):
	}
}

func (s *uiServer) serve(addr string) (string, error) {
	mux := http.NewServeMux()
	static, _ := fs.Sub(uiFiles, "ui")
	mux.Handle("/", http.FileServer(http.FS(static)))
	mux.HandleFunc("GET /api/status", s.apiStatus)
	mux.HandleFunc("GET /api/roots", s.apiRoots)
	mux.HandleFunc("GET /api/registry", s.apiRegistry)
	mux.HandleFunc("GET /api/chain", s.apiChain)
	mux.HandleFunc("POST /api/publish", s.apiPublish)
	mux.HandleFunc("GET /api/fetch", s.apiFetch)
	mux.HandleFunc("GET /api/library", s.apiLibrary)
	mux.HandleFunc("POST /api/library/add", s.apiLibraryAdd)
	mux.HandleFunc("POST /api/library/remove", s.apiLibraryRemove)

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return "", err
	}
	go http.Serve(ln, s.guard(mux))
	return ln.Addr().String(), nil
}

// guard locks the local surface (#89). Order matters: reject a non-local
// Host (DNS-rebinding) and a cross-origin drive-by before doing any work,
// answer CORS preflight, then require the bearer token on anything that
// changes state. Static file requests (no /api/ prefix) still pass the
// Host and Origin checks — a rebinding page must not read them either — but
// never need the token.
func (s *uiServer) guard(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 1. Host allow-list: the browser echoes the host it was pointed at,
		// so a DNS-rebinding page (evil.com → 127.0.0.1) arrives with a
		// non-local Host and is refused here.
		if !isLocalHost(r.Host) {
			httpError(w, http.StatusForbidden, fmt.Errorf("non-local Host %q refused", r.Host))
			return
		}
		// 2. Origin allow-list, replacing CORS `*`. A same-origin GET sends
		// no Origin (skip). A cross-origin page (drive-by or observatory)
		// sends one: reflect localhost origins so the observatory keeps
		// working, refuse everything else.
		if origin := r.Header.Get("Origin"); origin != "" {
			if !isLocalOrigin(origin) {
				httpError(w, http.StatusForbidden, fmt.Errorf("cross-origin request from %q refused", origin))
				return
			}
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		}
		// 3. CORS preflight: answer after the checks, before the token gate.
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		// 4. Token gate on state-changing methods only — reads keep their
		// no-token localhost ergonomics.
		if isMutating(r.Method) && !s.validToken(r) {
			httpError(w, http.StatusUnauthorized, fmt.Errorf("missing or invalid API token"))
			return
		}
		h.ServeHTTP(w, r)
	})
}

func isMutating(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

// validToken accepts the bearer token from the Authorization header or,
// for form-driven POSTs and download links that can't set headers, a
// `token` field/query param. Compared in constant time.
func (s *uiServer) validToken(r *http.Request) bool {
	if s.token == "" {
		return false // never allow mutation on a daemon with no token
	}
	got := ""
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		got = strings.TrimPrefix(h, "Bearer ")
	} else if t := r.URL.Query().Get("token"); t != "" {
		got = t
	} else {
		got = r.FormValue("token")
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) == 1
}

// isLocalHost reports whether the request's Host authority names the local
// machine (any port). Anything else — a rebinding page's real hostname —
// is refused.
func isLocalHost(host string) bool {
	h, _, err := net.SplitHostPort(host)
	if err != nil {
		h = host // no port
	}
	switch h {
	case "localhost", "127.0.0.1", "::1", "[::1]":
		return true
	}
	// Any loopback literal (127.0.0.0/8, ::1) counts.
	if ip := net.ParseIP(strings.Trim(h, "[]")); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// isLocalOrigin reports whether an Origin header names a localhost page —
// the daemon's own UI or a sibling local daemon's observatory.
func isLocalOrigin(origin string) bool {
	rest, ok := strings.CutPrefix(origin, "http://")
	if !ok {
		rest, ok = strings.CutPrefix(origin, "https://")
	}
	if !ok {
		return false
	}
	return isLocalHost(rest)
}

// loadOrCreateUIToken returns the daemon's persistent UI bearer token,
// minting one on first run. Persisted 0600 in the store dir so it survives
// restarts and the same operator's browser keeps working.
func loadOrCreateUIToken(storeDir string) (string, error) {
	path := filepath.Join(storeDir, "ui-token")
	if b, err := os.ReadFile(path); err == nil {
		if tok := strings.TrimSpace(string(b)); tok != "" {
			return tok, nil
		}
	}
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	tok := hex.EncodeToString(raw[:])
	if err := os.WriteFile(path, []byte(tok), 0o600); err != nil {
		return "", err
	}
	return tok, nil
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func httpError(w http.ResponseWriter, code int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

func (s *uiServer) apiStatus(w http.ResponseWriter, _ *http.Request) {
	type chainInfo struct {
		Height  int `json:"height"`
		Entries int `json:"entries"`
	}
	var out struct {
		ID           string           `json:"id"`
		Peer         string           `json:"peer"`
		UptimeSec    int64            `json:"uptimeSec"`
		CapUsed      int64            `json:"capUsed"`
		CapTotal     int64            `json:"capTotal"`
		Chunks       int              `json:"chunks"`
		Peers        int              `json:"peers"`
		Stats        node.Stats       `json:"stats"`
		Network      node.NetEstimate `json:"network"`
		Validator    bool             `json:"validator"`
		Reachability string           `json:"reachability"`
		Chain        *chainInfo       `json:"chain,omitempty"`
	}
	out.ID = s.nd.ID().String()
	out.Peer = s.selfPeer
	out.UptimeSec = int64(time.Since(s.started).Seconds())
	out.Validator = s.validator
	out.Peers = s.peerCount()
	s.onLoop(func() {
		out.Reachability = s.nd.Reachability().String()
		if s.capRep != nil {
			out.CapUsed, out.CapTotal = s.capRep.Capacity()
		}
		if ids, err := s.nd.Store().List(context.Background()); err == nil {
			out.Chunks = len(ids)
		}
		out.Stats = s.nd.Stats
		out.Network = s.nd.EstimateNetwork()
		if ch := s.nd.Chain(); ch != nil {
			out.Chain = &chainInfo{Height: ch.Len(), Entries: len(ch.AllEntries())}
		}
	})
	writeJSON(w, out)
}

const (
	defaultRootsPage = 50
	maxRootsPage     = 500
)

type rootRow struct {
	Root   string `json:"root"`
	Shards int    `json:"shards"`
}

func (s *uiServer) apiRoots(w http.ResponseWriter, r *http.Request) {
	var rows []rootRow
	s.onLoop(func() {
		for root, count := range s.nd.HeldRoots() {
			rows = append(rows, rootRow{Root: root.String(), Shards: count})
		}
	})
	page, total := paginateRoots(rows,
		atoiOr(r.URL.Query().Get("limit"), defaultRootsPage),
		atoiOr(r.URL.Query().Get("offset"), 0))
	writeJSON(w, map[string]any{"total": total, "rows": page})
}

// paginateRoots sorts rows by shard count (desc — most-hosted first), then
// root for a stable order, and returns the [offset, offset+limit) window
// plus the total. A limit <= 0 uses the default page and is capped at
// maxRootsPage; offset is clamped, so out-of-range paging yields an empty
// page rather than a panic.
func paginateRoots(rows []rootRow, limit, offset int) (page []rootRow, total int) {
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Shards != rows[j].Shards {
			return rows[i].Shards > rows[j].Shards
		}
		return rows[i].Root < rows[j].Root
	})
	total = len(rows)
	switch {
	case limit <= 0:
		limit = defaultRootsPage
	case limit > maxRootsPage:
		limit = maxRootsPage
	}
	if offset < 0 {
		offset = 0
	}
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	return rows[offset:end], total
}

func atoiOr(s string, def int) int {
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	return def
}

func (s *uiServer) apiRegistry(w http.ResponseWriter, r *http.Request) {
	if s.reg == nil {
		writeJSON(w, []any{})
		return
	}
	entries, err := s.reg.All(r.Context())
	if err != nil {
		// A remote public registry no longer serves a bulk /all dump (F-3), so a UI
		// pointed at one degrades to an empty list rather than erroring. An operator's
		// OWN registry (-serve-registry) is read in-process and lists fully.
		writeJSON(w, []any{})
		return
	}
	type row struct {
		Root           string `json:"root"`
		FileSize       int64  `json:"fileSize"`
		ManifestChunks int    `json:"manifestChunks"`
	}
	rows := make([]row, 0, len(entries))
	for _, e := range entries {
		rows = append(rows, row{Root: e.Root.String(), FileSize: e.FileSize, ManifestChunks: len(e.ManifestChunks)})
	}
	writeJSON(w, rows)
}

func (s *uiServer) apiChain(w http.ResponseWriter, _ *http.Request) {
	type row struct {
		Height   uint64 `json:"height"`
		Hash     string `json:"hash"`
		Entries  int    `json:"entries"`
		Proposer string `json:"proposer"`
		Atts     int    `json:"atts"`
	}
	rows := []row{}
	s.onLoop(func() {
		ch := s.nd.Chain()
		if ch == nil {
			return
		}
		for _, b := range ch.Blocks(0) {
			rows = append(rows, row{
				Height: b.Height, Hash: b.Hash().String(),
				Entries: len(b.Entries), Proposer: b.ProposerID().String(), Atts: len(b.Atts),
			})
		}
	})
	writeJSON(w, rows)
}

// apiPublish: multipart upload → chunk, encrypt, scatter, register —
// through an ephemeral client, exactly like `silt swarm add`.
func (s *uiServer) apiPublish(w http.ResponseWriter, r *http.Request) {
	if s.reg == nil {
		httpError(w, 400, fmt.Errorf("this daemon has no registry configured"))
		return
	}
	if err := r.ParseMultipartForm(512 << 20); err != nil {
		httpError(w, 400, err)
		return
	}
	f, hdr, err := r.FormFile("file")
	if err != nil {
		httpError(w, 400, err)
		return
	}
	defer f.Close()
	mode := crypto.Private // H6: private by default — convergent must be opted into explicitly
	if r.FormValue("mode") == "convergent" {
		mode = crypto.Convergent
	}

	e, run, err := joinSwarm(s.selfPeer)
	if err != nil {
		httpError(w, 502, err)
		return
	}
	defer e.close()

	var h link.Handle
	var placed int
	var opErr error
	rerr := run(func(done func()) {
		var aerr error
		var entry ports.Entry
		// No Publisher: a UI publish is the default user path and must be
		// unlinkable (M0/#97). A durable Publisher→root record is permanent
		// on the chain; the UI never opts into it.
		//
		// Stage (not Add): store the content, register only after a confirmed
		// scatter, so a placement failure never leaves a dangling entry (#65).
		h, entry, aerr = pipeline.Stage(context.Background(), e.nd.Store(), f, pipeline.Options{
			Mode: mode, Rand: rand.Reader,
		})
		if aerr != nil {
			opErr = aerr
			done()
			return
		}
		m, merr := pipeline.LoadFull(context.Background(), e.nd.Store(), entry, h)
		if merr != nil {
			opErr = merr
			done()
			return
		}
		e.nd.DistributeFrom(e.nd.Store(), entry, m, node.DerivePorKey(h.LayoutKey()), func(p int, derr error) {
			// Publish only on a confirmed scatter; a failed one leaves the
			// registry untouched so no dangling entry survives (#65).
			placed, opErr = pipeline.RegisterAfterDistribute(context.Background(), s.reg, entry, p, derr)
			done()
		})
	})
	if rerr != nil {
		httpError(w, 504, rerr)
		return
	}
	if opErr != nil {
		httpError(w, 502, opErr)
		return
	}
	// Auto-caretake our own content: without a caretaker, a published
	// file's redundancy only decays as nodes churn. The publishing daemon
	// is the natural first caretaker, so it starts repairing this root (its
	// manifest now counts toward this node's pledge). Opt out with
	// -care-published=false.
	cared := false
	if s.carePublished && s.reg != nil {
		ch := h.Care()
		s.onLoop(func() { s.nd.Care(s.reg, ch) })
		cared = true
	}
	writeJSON(w, map[string]any{
		"name":      hdr.Filename,
		"link":      h.String(),
		"careLink":  h.Care().String(),
		"placed":    placed,
		"caretaker": cared,
	})
}

// apiFetch: ?link=silt:v1:… → the decrypted file, verified end to end.
func (s *uiServer) apiFetch(w http.ResponseWriter, r *http.Request) {
	if s.reg == nil {
		httpError(w, 400, fmt.Errorf("this daemon has no registry configured"))
		return
	}
	h, err := link.Parse(r.URL.Query().Get("link"))
	if err != nil {
		httpError(w, 400, err)
		return
	}
	e, run, err := joinSwarm(s.selfPeer)
	if err != nil {
		httpError(w, 502, err)
		return
	}
	defer e.close()

	var buf bytes.Buffer
	var opErr error
	rerr := run(func(done func()) {
		e.nd.NetGet(s.reg, h, &buf, func(err error) { opErr = err; done() })
	})
	if rerr != nil {
		httpError(w, 504, rerr)
		return
	}
	if opErr != nil {
		httpError(w, 502, opErr)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf("attachment; filename=%q", h.Root.String()[:16]+".bin"))
	io.Copy(w, &buf)
}

// apiLibrary lists the user's saved links (files they hold keys for),
// each annotated with what the network/chain currently knows about that
// root — so the library distinguishes "yours and available" from "yours
// but the network seems to have lost it".
func (s *uiServer) apiLibrary(w http.ResponseWriter, r *http.Request) {
	type row struct {
		Root     string `json:"root"`
		Link     string `json:"link"`
		Label    string `json:"label"`
		Added    int64  `json:"added"`
		OnChain  bool   `json:"onChain"`
		FileSize int64  `json:"fileSize"`
	}
	var rows []row
	items := []linkbook.Item{}
	if s.links != nil {
		items = s.links.Items()
	}
	// Build a root→entry index from the registry once.
	index := map[string]ports.Entry{}
	networkTotal := 0
	if s.reg != nil {
		if entries, err := s.reg.All(r.Context()); err == nil {
			networkTotal = len(entries)
			for _, e := range entries {
				index[e.Root.String()] = e
			}
		}
	}
	held := map[string]bool{}
	for _, it := range items {
		h, err := link.Parse(it.Link)
		if err != nil {
			continue
		}
		rootHex := h.Root.String()
		held[rootHex] = true
		e, on := index[rootHex]
		rows = append(rows, row{
			Root: rootHex, Link: it.Link, Label: it.Label, Added: it.Added,
			OnChain: on, FileSize: e.FileSize,
		})
	}
	// How many identifiers the network hosts that the user has NO key for.
	opaque := 0
	for root := range index {
		if !held[root] {
			opaque++
		}
	}
	writeJSON(w, map[string]any{
		"library":      rows,
		"networkFiles": networkTotal,
		"opaqueToYou":  opaque,
	})
}

func (s *uiServer) apiLibraryAdd(w http.ResponseWriter, r *http.Request) {
	if s.links == nil {
		httpError(w, 400, fmt.Errorf("no link book (client mode only)"))
		return
	}
	root, err := s.links.Add(r.FormValue("link"), r.FormValue("label"))
	if err != nil {
		httpError(w, 400, err)
		return
	}
	writeJSON(w, map[string]string{"root": root})
}

func (s *uiServer) apiLibraryRemove(w http.ResponseWriter, r *http.Request) {
	if s.links == nil {
		httpError(w, 400, fmt.Errorf("no link book"))
		return
	}
	if err := s.links.Remove(r.FormValue("root")); err != nil {
		httpError(w, 400, err)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}
