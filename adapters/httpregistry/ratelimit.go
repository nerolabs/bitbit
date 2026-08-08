package httpregistry

import (
	"net"
	"net/http"
	"sync"
	"time"
)

// Read-cost bounding for a public registry (#48 — keep registries cheap to run). A
// registry is a costless public good only if a single caller can't drive unbounded
// lookup cost; without a bound, a flood of lookups is a free way to make a volunteer's
// registry expensive. (The one truly unbounded endpoint, the whole-registry `GET /all`
// dump, is no longer served on the public mux — F-3.) These defaults are generous for
// normal clients (a lookup is the hot path and cheap) — bursts are absorbed, sustained
// floods from one source get 429 — and are a per-network posture, not a hard promise.
const (
	defaultRatePerSec = 20.0 // sustained requests/second per client IP
	defaultBurst      = 40.0 // absorbed instantaneous burst per client IP

	// maxBuckets hard-caps the per-IP bucket map so it stays a BOUNDED resource even
	// under a flood that cycles source IPs faster than the idle-prune reclaims them —
	// otherwise the map is its own DoS vector (red-team F-3 hardening §5.3). At the cap
	// a sampled-LRU eviction drops the oldest of a small random sample per new IP,
	// bounding memory without an O(n) scan per request.
	maxBuckets = 100_000
)

type tokenBucket struct {
	tokens float64
	last   time.Time
}

// ipRateLimiter is a per-remote-IP token-bucket limiter. Idle buckets are pruned on a
// timer so a caller cycling source IPs can't grow the map without bound — the bucket
// map would otherwise be its OWN cost vector.
type ipRateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*tokenBucket
	rate    float64
	burst   float64
	stop    chan struct{}
}

func newIPRateLimiter(rate, burst float64) *ipRateLimiter {
	l := &ipRateLimiter{buckets: map[string]*tokenBucket{}, rate: rate, burst: burst, stop: make(chan struct{})}
	go l.cleanupLoop()
	return l
}

// bucketFor returns ip's bucket (creating it if new), hard-capping the map at
// maxBuckets: when full, it evicts the least-recently-seen of a small random sample
// (sampled LRU, Redis-style) so the map can't grow without bound under a distinct-IP
// flood. Caller holds l.mu.
func (l *ipRateLimiter) bucketFor(ip string, now time.Time) *tokenBucket {
	if b := l.buckets[ip]; b != nil {
		return b
	}
	if len(l.buckets) >= maxBuckets {
		var victim string
		var oldest time.Time
		i := 0
		for k, b := range l.buckets { // Go map iteration is randomized → this samples
			if i == 0 || b.last.Before(oldest) {
				victim, oldest = k, b.last
			}
			if i++; i >= 8 {
				break
			}
		}
		delete(l.buckets, victim)
	}
	b := &tokenBucket{tokens: l.burst, last: now}
	l.buckets[ip] = b
	return b
}

// allow charges one request to ip's bucket at time now, refilling first, and reports
// whether it is under the limit.
func (l *ipRateLimiter) allow(ip string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.bucketFor(ip, now)
	b.tokens += now.Sub(b.last).Seconds() * l.rate
	if b.tokens > l.burst {
		b.tokens = l.burst
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// cleanupLoop drops buckets idle long enough to have refilled fully anyway — so a
// returning IP gets a fresh full bucket, exactly as if it had never been pruned (no
// free requests gained), while the map stays bounded to recently-active callers.
func (l *ipRateLimiter) cleanupLoop() {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-l.stop:
			return
		case now := <-t.C:
			l.mu.Lock()
			for ip, b := range l.buckets {
				if now.Sub(b.last) > 2*time.Minute {
					delete(l.buckets, ip)
				}
			}
			l.mu.Unlock()
		}
	}
}

func (l *ipRateLimiter) close() { close(l.stop) }

// clientIP is the remote IP (without port) a request is bucketed under.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// limit wraps h, returning 429 when the caller's IP is over its token bucket.
func (l *ipRateLimiter) limit(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !l.allow(clientIP(r), time.Now()) {
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		h.ServeHTTP(w, r)
	})
}
