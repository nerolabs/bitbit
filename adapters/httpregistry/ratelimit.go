package httpregistry

import (
	"net"
	"net/http"
	"sync"
	"time"
)

// Read-cost bounding for a public registry (#48 — keep registries cheap to run). A
// registry is a costless public good only if a single caller can't drive unbounded
// lookup / serialize cost; without a bound, `GET /all` and a flood of lookups are a
// free way to make a volunteer's registry expensive. These defaults are generous for
// normal clients (a lookup is the hot path and cheap) — bursts are absorbed, sustained
// floods from one source get 429 — and are a per-network posture, not a hard promise.
const (
	defaultRatePerSec = 20.0 // sustained requests/second per client IP
	defaultBurst      = 40.0 // absorbed instantaneous burst per client IP

	// Work-proportional pricing for GET /all (red-team blind-2026-08-08 F-3): a
	// per-request token bucket meters the wrong quantity when one endpoint does
	// O(N) work — /all serializes the whole registry for the same one token as a
	// 183-byte /lookup (measured ~20,000× byte amplification at N=20k). So /all is
	// additionally charged one token per allEntriesPerToken entries it serves,
	// letting the bucket go into bounded debt — the amplification is now PRICED
	// per source. (This bounds per-SOURCE cost; a distributed /all flood and full
	// cursor pagination remain — see the #48 CHANGELOG note.)
	allEntriesPerToken = 64.0
	maxDebtSeconds     = 60.0 // cap how long one heavy response can lock a source out
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

// allow charges one request to ip's bucket at time now, refilling first, and reports
// whether it is under the limit.
func (l *ipRateLimiter) allow(ip string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.buckets[ip]
	if b == nil {
		b = &tokenBucket{tokens: l.burst, last: now}
		l.buckets[ip] = b
	}
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

// charge deducts cost tokens from ip's bucket (refilling first), letting the balance
// go into bounded debt so an expensive O(N) response — a big GET /all — imposes a
// real, work-proportional cooldown on that source instead of costing one flat token.
// The debt is floored so a single huge response can't lock a caller out for more than
// ~maxDebtSeconds. This is the fix for metering request COUNT when per-request work is
// unbounded (Crosby–Wallach algorithmic-complexity DoS).
func (l *ipRateLimiter) charge(ip string, cost float64, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.buckets[ip]
	if b == nil {
		b = &tokenBucket{tokens: l.burst, last: now}
		l.buckets[ip] = b
	}
	b.tokens += now.Sub(b.last).Seconds() * l.rate
	if b.tokens > l.burst {
		b.tokens = l.burst
	}
	b.last = now
	b.tokens -= cost
	if floor := -(l.burst + l.rate*maxDebtSeconds); b.tokens < floor {
		b.tokens = floor
	}
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
