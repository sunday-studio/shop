package hostedapi

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"shop/internal/hosted/hclock"
)

// rateLimiter is a sliding-window counter in memory (one process). Keys are "scope:subject".
type rateLimiter struct {
	clock hclock.Clock
	mu    sync.Mutex
	hits  map[string][]time.Time
	gc    time.Time
}

func newRateLimiter(c hclock.Clock) *rateLimiter {
	return &rateLimiter{clock: c, hits: map[string][]time.Time{}}
}

// allow records one hit for key unless max hits already happened in the window; it returns how
// long to wait when it refuses. A refused hit is not recorded.
func (l *rateLimiter) allow(key string, max int, window time.Duration) (bool, time.Duration) {
	if max <= 0 {
		return true, 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.clock.Now()
	l.sweepLocked(now)
	h := prune(l.hits[key], now.Add(-window))
	if len(h) >= max {
		l.hits[key] = h
		return false, h[0].Add(window).Sub(now)
	}
	l.hits[key] = append(h, now)
	return true, 0
}

// add records an extra hit (a failed sign-in counts double).
func (l *rateLimiter) add(key string, window time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.clock.Now()
	l.hits[key] = append(prune(l.hits[key], now.Add(-window)), now)
}

func prune(h []time.Time, since time.Time) []time.Time {
	i := 0
	for i < len(h) && !h[i].After(since) {
		i++
	}
	return h[i:]
}

// sweepLocked forgets idle keys now and then so the map stays small.
func (l *rateLimiter) sweepLocked(now time.Time) {
	if now.Sub(l.gc) < time.Minute {
		return
	}
	l.gc = now
	for k, h := range l.hits {
		if len(h) == 0 || now.Sub(h[len(h)-1]) > 2*time.Hour {
			delete(l.hits, k)
		}
	}
}

// limit checks one bucket and returns the error to answer with.
func (a *API) limit(key string, max int, window time.Duration) error {
	if a.Cfg.Rates.Disabled {
		return nil
	}
	if ok, wait := a.rl.allow(key, max, window); !ok {
		return rateLimited(wait)
	}
	return nil
}

// clientIP is the caller's address (the last X-Forwarded-For entry when behind our own proxy).
func (a *API) clientIP(r *http.Request) string {
	if a.Cfg.TrustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			if ip := strings.TrimSpace(parts[len(parts)-1]); ip != "" {
				return ip
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
