package httpregistry

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestRateLimiterBurstThenThrottleThenRefill: a client gets its full burst, is then
// throttled, and recovers as tokens refill — the read-cost bound that keeps a public
// registry cheap (#48).
func TestRateLimiterBurstThenThrottleThenRefill(t *testing.T) {
	l := newIPRateLimiter(10, 5) // 10/s, burst 5
	defer l.close()
	now := time.Unix(1_000_000, 0)

	// The full burst is allowed at a single instant.
	for i := 0; i < 5; i++ {
		if !l.allow("1.2.3.4", now) {
			t.Fatalf("request %d within burst should be allowed", i)
		}
	}
	// The next one, same instant, is throttled.
	if l.allow("1.2.3.4", now) {
		t.Fatal("a 6th request past the burst must be throttled")
	}
	// After 0.5s at 10/s → 5 tokens refilled → burst available again.
	later := now.Add(500 * time.Millisecond)
	for i := 0; i < 5; i++ {
		if !l.allow("1.2.3.4", later) {
			t.Fatalf("refilled request %d should be allowed", i)
		}
	}
	if l.allow("1.2.3.4", later) {
		t.Fatal("still throttled once the refilled burst is spent")
	}
}

// TestChargePricesAllByWork is the F-3 regression (red-team blind-2026-08-08): a big
// GET /all must cost the caller tokens PROPORTIONAL TO WORK, not one flat token, so a
// single source can't amplify one token into an unbounded full-registry dump. After
// serving a large /all, that IP's bucket is drained into debt and the next request is
// throttled — the amplification is now priced.
func TestChargePricesAllByWork(t *testing.T) {
	l := newIPRateLimiter(10, 5) // 10/s, burst 5
	defer l.close()
	now := time.Unix(3_000_000, 0)
	const ip = "198.51.100.9"

	// One /all-sized request is allowed (spends 1 of the 5 burst tokens)...
	if !l.allow(ip, now) {
		t.Fatal("the first request should be allowed")
	}
	// ...then priced by the work it did: a 20k-entry registry at 64 entries/token
	// costs ~312 tokens, far exceeding the burst → the bucket goes deep into debt.
	l.charge(ip, 20_000/allEntriesPerToken, now)

	// The very next request from the same IP, same instant, must be throttled — the
	// caller cannot immediately repeat the expensive dump (the pre-fix behavior the
	// red-team measured: 40 unpriced /all in one burst, zero 429).
	if l.allow(ip, now) {
		t.Fatal("F-3 regression: a source that just pulled a large /all must be throttled, not free to repeat it")
	}
	// A small /all (few entries) costs a fraction of a token, so normal use is
	// unaffected — the price tracks the work, it isn't a flat penalty on /all.
	l2 := newIPRateLimiter(10, 5)
	defer l2.close()
	l2.allow("10.0.0.5", now)
	l2.charge("10.0.0.5", 10/allEntriesPerToken, now) // 10 entries → ~0.16 tokens
	if !l2.allow("10.0.0.5", now) {
		t.Fatal("a small /all must not lock out a normal caller")
	}
}

// TestRateLimiterIsPerIP: one noisy client does not throttle another.
func TestRateLimiterIsPerIP(t *testing.T) {
	l := newIPRateLimiter(1, 2)
	defer l.close()
	now := time.Unix(2_000_000, 0)
	l.allow("10.0.0.1", now)
	l.allow("10.0.0.1", now)
	if l.allow("10.0.0.1", now) {
		t.Fatal("the noisy IP should be throttled")
	}
	if !l.allow("10.0.0.2", now) {
		t.Fatal("a different IP must have its own bucket")
	}
}

// TestRateLimitMiddleware429: the HTTP middleware returns 429 once the bucket is spent.
func TestRateLimitMiddleware429(t *testing.T) {
	l := newIPRateLimiter(0.0001, 2) // effectively no refill during the test
	defer l.close()
	h := l.limit(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))

	codes := make([]int, 0, 3)
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodGet, "/lookup", nil)
		req.RemoteAddr = "203.0.113.7:5555"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		codes = append(codes, rec.Code)
	}
	// burst 2 → first two 200, third 429.
	if codes[0] != http.StatusOK || codes[1] != http.StatusOK {
		t.Fatalf("first two requests should pass: got %v", codes)
	}
	if codes[2] != http.StatusTooManyRequests {
		t.Fatalf("third request should be rate-limited (429): got %d", codes[2])
	}
}
