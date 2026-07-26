// Package middleware holds the chi/net-http middleware stack: request id,
// panic recovery, structured logging, CORS, plus auth and rate limiting in
// sibling files.
package middleware

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"slices"
	"time"

	"github.com/innzout/ootybites/pkg/response"
)

type ctxKey string

const (
	ctxRequestID ctxKey = "request_id"
	ctxSubject   ctxKey = "subject"  // authenticated entity id
	ctxAudience  ctxKey = "audience" // "customer" | "admin"
)

// RequestID attaches a short random id to each request for log correlation.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if id == "" {
			b := make([]byte, 8)
			_, _ = rand.Read(b)
			id = hex.EncodeToString(b)
		}
		w.Header().Set("X-Request-ID", id)
		ctx := context.WithValue(r.Context(), ctxRequestID, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// Recover turns panics into a 500 envelope instead of crashing the server.
func Recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				slog.Error("panic recovered",
					"request_id", RequestIDFrom(r.Context()),
					"method", r.Method, "path", r.URL.Path, "error", err)
				response.Fail(w, http.StatusInternalServerError, response.CodeInternal,
					"An unexpected error occurred. Please try again.")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// statusRecorder captures the response code for logging.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// Logger emits one structured line per request after it completes.
func Logger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		slog.Info("request",
			"request_id", RequestIDFrom(r.Context()),
			"method", r.Method, "path", r.URL.Path,
			"status", rec.status, "duration_ms", time.Since(start).Milliseconds())
	})
}

// CORS allows the configured frontend origins and the usual verbs/headers.
func CORS(allowed []string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin != "" && slices.Contains(allowed, origin) {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Vary", "Origin")
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Request-ID")
				w.Header().Set("Access-Control-Max-Age", "300")
			}
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequestIDFrom returns the request id stored on the context, or "".
func RequestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(ctxRequestID).(string)
	return id
}

// SubjectFrom returns the authenticated entity id set by an auth middleware.
func SubjectFrom(ctx context.Context) string {
	s, _ := ctx.Value(ctxSubject).(string)
	return s
}

// AudienceFrom returns the token audience ("customer"|"admin") of the caller.
func AudienceFrom(ctx context.Context) string {
	a, _ := ctx.Value(ctxAudience).(string)
	return a
}
