package middleware

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/innzout/ootybites/pkg/response"
)

// Limiter is the fixed-window rate-limit backend. Both the Redis client
// (internal/redis) and the in-memory fallback below satisfy it. AllowN returns
// (allowed, err); callers treat a non-nil error as "allow" (fail open).
type Limiter interface {
	AllowN(ctx context.Context, key string, max int, window time.Duration) (bool, error)
}

// RateLimit returns middleware enforcing a fixed-window limit of `max` requests
// per `window` for the named route, keyed by customer id when authenticated,
// otherwise by client IP. Applied to sensitive routes only: OTP request/verify,
// admin login, order place, coupon validate.
func RateLimit(l Limiter, route string, max int, window time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := "rl:" + route + ":" + clientKey(r)
			ok, err := l.AllowN(r.Context(), key, max, window)
			if err == nil && !ok {
				// Security event: someone is hammering a sensitive route.
				slog.Warn("rate limit exceeded", "route", route, "ip", ClientIP(r))
				response.Fail(w, http.StatusTooManyRequests, response.CodeRateLimited,
					"Too many requests. Please slow down and try again shortly.")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// trustedProxyHops is how many reverse proxies sit between the internet and
// this process. Set once at boot via SetTrustedProxyHops.
var trustedProxyHops int

// SetTrustedProxyHops records how many proxies in front of the API may be
// trusted to have appended to X-Forwarded-For (Railway's edge is one). Zero
// means the API is reached directly and the header is ignored entirely.
func SetTrustedProxyHops(n int) {
	if n < 0 {
		n = 0
	}
	trustedProxyHops = n
}

// ClientIP returns the address rate limits are keyed on.
//
// X-Forwarded-For is client-controlled: anyone can send
// "X-Forwarded-For: <anything>". The limiter used to key on that header
// verbatim, so rotating its value gave every request a fresh bucket and every
// unauthenticated limit - OTP request, OTP verify, admin and dealer login -
// could be bypassed at will.
//
// Only the entries appended by our own proxies are trustworthy, and a proxy
// appends on the right. With N trusted hops the real client is therefore the
// Nth entry from the right; anything further left was supplied by the client
// and is ignored. With zero trusted hops the header is not consulted at all.
func ClientIP(r *http.Request) string {
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	if trustedProxyHops == 0 {
		return ip
	}
	parts := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	idx := len(parts) - trustedProxyHops
	if idx < 0 || idx >= len(parts) {
		// Fewer entries than trusted hops: the request did not come through
		// the proxies we expect, so fall back to the socket address.
		return ip
	}
	if fwd := strings.TrimSpace(parts[idx]); fwd != "" {
		return fwd
	}
	return ip
}

// clientKey prefers the authenticated subject; falls back to the client IP.
func clientKey(r *http.Request) string {
	if sub := SubjectFrom(r.Context()); sub != "" {
		return "cust:" + sub
	}
	return "ip:" + ClientIP(r)
}

// MemoryLimiter is a process-local fixed-window limiter used when Redis is not
// configured (local dev). It is not shared across instances — fine for a single
// local process, not for production behind multiple replicas.
type MemoryLimiter struct {
	mu      sync.Mutex
	windows map[string]*window
}

type window struct {
	count   int
	resetAt time.Time
}

// NewMemoryLimiter builds an in-memory limiter and starts a background sweep
// that evicts expired windows.
func NewMemoryLimiter() *MemoryLimiter {
	m := &MemoryLimiter{windows: make(map[string]*window)}
	go m.sweep()
	return m
}

// AllowN implements Limiter.
func (m *MemoryLimiter) AllowN(_ context.Context, key string, max int, dur time.Duration) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	w, ok := m.windows[key]
	if !ok || now.After(w.resetAt) {
		m.windows[key] = &window{count: 1, resetAt: now.Add(dur)}
		return true, nil
	}
	w.count++
	return w.count <= max, nil
}

func (m *MemoryLimiter) sweep() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		now := time.Now()
		m.mu.Lock()
		for k, w := range m.windows {
			if now.After(w.resetAt) {
				delete(m.windows, k)
			}
		}
		m.mu.Unlock()
	}
}
