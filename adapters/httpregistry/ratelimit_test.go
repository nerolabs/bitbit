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

// TestBucketMapIsBounded is the F-3 hardening regression: the per-IP bucket map is a
// bounded resource — a flood that cycles source IPs cannot grow it without bound (it
// would otherwise be its OWN cost vector). It never exceeds maxBuckets, and a
// recently-active IP survives the sampled-LRU eviction that a flood triggers.
func TestBucketMapIsBounded(t *testing.T) {
	l := newIPRateLimiter(10, 5)
	defer l.close()
	base := time.Unix(4_000_000, 0)

	// Pre-seed one IP and keep it hot (most-recent `last`), so the sampled-LRU eviction
	// prefers older flood entries over it.
	const hot = "203.0.113.1"
	l.allow(hot, base.Add(time.Duration(maxBuckets+50)*time.Second))

	// Flood far more distinct IPs than the cap, each older than `hot`.
	for i := 0; i < maxBuckets+5000; i++ {
		l.allow("10."+itoa(i/65536%256)+"."+itoa(i/256%256)+"."+itoa(i%256), base.Add(time.Duration(i)*time.Second))
	}
	l.mu.Lock()
	n := len(l.buckets)
	_, hotAlive := l.buckets[hot]
	l.mu.Unlock()
	if n > maxBuckets {
		t.Fatalf("F-3: the bucket map must stay ≤ maxBuckets (%d); got %d", maxBuckets, n)
	}
	if !hotAlive {
		t.Fatal("F-3: a recently-active IP must survive the sampled-LRU eviction under a distinct-IP flood")
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
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
