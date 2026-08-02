package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// guarded wraps a trivial handler that records whether it ran, so a test
// can tell "the guard let it through" from "the guard blocked it".
func guarded(token string) (http.Handler, *bool) {
	reached := new(bool)
	s := &uiServer{token: token}
	h := s.guard(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		*reached = true
		w.WriteHeader(http.StatusOK)
	}))
	return h, reached
}

func do(h http.Handler, method, host, origin, auth string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://"+host+"/api/publish", nil)
	r.Host = host
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	if auth != "" {
		r.Header.Set("Authorization", auth)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// The headline regression: CORS `*` is gone. No request, of any shape, is
// answered with a wildcard allow-origin.
func TestNoWildcardCORS(t *testing.T) {
	h, _ := guarded("secret")
	for _, origin := range []string{"", "http://127.0.0.1:9000", "http://evil.com"} {
		w := do(h, http.MethodGet, "127.0.0.1:8081", origin, "")
		if got := w.Header().Get("Access-Control-Allow-Origin"); got == "*" {
			t.Fatalf("wildcard CORS leaked for origin %q", origin)
		}
	}
}

func TestNonLocalHostRejected(t *testing.T) {
	h, reached := guarded("secret")
	w := do(h, http.MethodGet, "evil.com", "", "")
	if w.Code != http.StatusForbidden {
		t.Fatalf("non-local Host got %d, want 403", w.Code)
	}
	if *reached {
		t.Fatal("handler ran despite a non-local Host (DNS-rebinding not blocked)")
	}
}

func TestCrossOriginRejected(t *testing.T) {
	h, reached := guarded("secret")
	w := do(h, http.MethodGet, "127.0.0.1:8081", "http://evil.com", "")
	if w.Code != http.StatusForbidden {
		t.Fatalf("cross-origin got %d, want 403", w.Code)
	}
	if *reached {
		t.Fatal("handler ran on a cross-origin drive-by request")
	}
}

func TestLocalOriginReflectedForObservatory(t *testing.T) {
	h, reached := guarded("secret")
	// A sibling local daemon's observatory page reads this one cross-origin.
	w := do(h, http.MethodGet, "127.0.0.1:8081", "http://127.0.0.1:9000", "")
	if w.Code != http.StatusOK || !*reached {
		t.Fatalf("local cross-origin read blocked: code=%d reached=%v", w.Code, *reached)
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "http://127.0.0.1:9000" {
		t.Fatalf("local origin not reflected, got %q", got)
	}
}

func TestReadNeedsNoToken(t *testing.T) {
	h, reached := guarded("secret")
	w := do(h, http.MethodGet, "localhost:8081", "", "")
	if w.Code != http.StatusOK || !*reached {
		t.Fatalf("localhost read should not need a token: code=%d reached=%v", w.Code, *reached)
	}
}

func TestMutationRequiresToken(t *testing.T) {
	h, reached := guarded("secret")

	// No token → 401.
	if w := do(h, http.MethodPost, "127.0.0.1:8081", "", ""); w.Code != http.StatusUnauthorized || *reached {
		t.Fatalf("POST without token: code=%d reached=%v, want 401 and not reached", w.Code, *reached)
	}
	// Wrong token → 401.
	*reached = false
	if w := do(h, http.MethodPost, "127.0.0.1:8081", "", "Bearer nope"); w.Code != http.StatusUnauthorized || *reached {
		t.Fatalf("POST with wrong token: code=%d reached=%v, want 401", w.Code, *reached)
	}
	// Correct token → through.
	*reached = false
	if w := do(h, http.MethodPost, "127.0.0.1:8081", "", "Bearer secret"); w.Code != http.StatusOK || !*reached {
		t.Fatalf("POST with correct token: code=%d reached=%v, want 200", w.Code, *reached)
	}
}

func TestMutationTokenViaQueryParam(t *testing.T) {
	h, reached := guarded("secret")
	r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8081/api/publish?token=secret", nil)
	r.Host = "127.0.0.1:8081"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK || !*reached {
		t.Fatalf("query-param token: code=%d reached=%v, want 200", w.Code, *reached)
	}
}

func TestPreflightAnswered(t *testing.T) {
	h, reached := guarded("secret")
	w := do(h, http.MethodOptions, "127.0.0.1:8081", "http://127.0.0.1:9000", "")
	if w.Code != http.StatusNoContent {
		t.Fatalf("preflight got %d, want 204", w.Code)
	}
	if *reached {
		t.Fatal("preflight should not reach the handler")
	}
}

func TestTokenlessDaemonRefusesAllMutation(t *testing.T) {
	h, _ := guarded("") // no token minted
	if w := do(h, http.MethodPost, "127.0.0.1:8081", "", "Bearer secret"); w.Code != http.StatusUnauthorized {
		t.Fatalf("a tokenless daemon must refuse mutation, got %d", w.Code)
	}
}

func TestIsLocalHostAndOrigin(t *testing.T) {
	local := []string{"localhost", "127.0.0.1", "127.0.0.1:8081", "[::1]:80", "127.0.0.5:9"}
	for _, h := range local {
		if !isLocalHost(h) {
			t.Errorf("isLocalHost(%q) = false, want true", h)
		}
	}
	for _, h := range []string{"evil.com", "10.0.0.1:80", "example.com:8081", "8.8.8.8"} {
		if isLocalHost(h) {
			t.Errorf("isLocalHost(%q) = true, want false", h)
		}
	}
	if !isLocalOrigin("http://127.0.0.1:9000") || !isLocalOrigin("https://localhost") {
		t.Error("local origins should be recognized")
	}
	for _, o := range []string{"http://evil.com", "https://example.com:8081", "null", "file://x"} {
		if isLocalOrigin(o) {
			t.Errorf("isLocalOrigin(%q) = true, want false", o)
		}
	}
}

func TestLoadOrCreateUITokenPersists(t *testing.T) {
	dir := t.TempDir()
	tok, err := loadOrCreateUIToken(dir)
	if err != nil || tok == "" {
		t.Fatalf("mint token: %v (tok=%q)", err, tok)
	}
	again, err := loadOrCreateUIToken(dir)
	if err != nil || again != tok {
		t.Fatalf("token not stable across calls: %q vs %q (%v)", tok, again, err)
	}
	// Persisted with owner-only permissions.
	info, err := os.Stat(filepath.Join(dir, "ui-token"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("token file perm = %o, want 600", perm)
	}
}
