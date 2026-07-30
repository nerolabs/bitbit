package main

// The embedded web UI (M13): one Go binary, zero extra runtime. The
// daemon serves static pages (go:embed) plus a JSON API; all node-state
// reads post onto the daemon's event loop, and publish/fetch run
// through an in-process ephemeral swarm client so the daemon's storage
// pledge is never touched by UI operations. CORS is open on the API so
// the observatory page can aggregate many daemons from one browser tab.

import (
	"bytes"
	"context"
	"crypto/rand"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"sort"
	"strconv"
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
	go http.Serve(ln, cors(mux))
	return ln.Addr().String(), nil
}

func cors(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		h.ServeHTTP(w, r)
	})
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
		httpError(w, 502, err)
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
	mode := crypto.Convergent
	if r.FormValue("mode") == "private" {
		mode = crypto.Private
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
		h, aerr = pipeline.Add(context.Background(), e.nd.Store(), s.reg, f, pipeline.Options{
			Mode: mode, Rand: rand.Reader, Publisher: e.nd.ID(),
		})
		if aerr != nil {
			opErr = aerr
			done()
			return
		}
		entry, _, _ := s.reg.Lookup(context.Background(), h.Root)
		m, merr := pipeline.LoadFull(context.Background(), e.nd.Store(), entry, h)
		if merr != nil {
			opErr = merr
			done()
			return
		}
		e.nd.DistributeFrom(e.nd.Store(), entry, m, func(p int, derr error) { placed = p; opErr = derr; done() })
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
