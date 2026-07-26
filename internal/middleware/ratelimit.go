package middleware

import (
	"context"
	"net"
	"net/http"
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
				response.Fail(w, http.StatusTooManyRequests, response.CodeRateLimited,
					"Too many requests. Please slow down and try again shortly.")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// clientKey prefers the authenticated subject; falls back to remote IP.
func clientKey(r *http.Request) string {
	if sub := SubjectFrom(r.Context()); sub != "" {
		return "cust:" + sub
	}
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		ip = fwd
	}
	return "ip:" + ip
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
