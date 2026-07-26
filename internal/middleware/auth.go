package middleware

import (
	"context"
	"net/http"
	"strings"

	"github.com/innzout/ootybites/internal/token"
	"github.com/innzout/ootybites/pkg/response"
)

// RequireCustomer accepts only valid customer-audience tokens.
func RequireCustomer(secret string) func(http.Handler) http.Handler {
	return requireAudience(secret, token.AudienceCustomer)
}

// RequireAdmin accepts only valid admin-audience tokens. A customer token can
// never satisfy this (enforced by the audience check), per the two-audience rule.
func RequireAdmin(secret string) func(http.Handler) http.Handler {
	return requireAudience(secret, token.AudienceAdmin)
}

// RequireDealer accepts only valid dealer-audience tokens.
func RequireDealer(secret string) func(http.Handler) http.Handler {
	return requireAudience(secret, token.AudienceDealer)
}

func requireAudience(secret, audience string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw := bearerToken(r)
			if raw == "" {
				response.Fail(w, http.StatusUnauthorized, response.CodeUnauthorized, "Authorization required")
				return
			}
			claims, err := token.Parse(secret, raw, audience)
			if err != nil {
				response.Fail(w, http.StatusUnauthorized, response.CodeUnauthorized, "Invalid or expired session")
				return
			}
			ctx := context.WithValue(r.Context(), ctxSubject, claims.Subject)
			ctx = context.WithValue(ctx, ctxAudience, audience)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	parts := strings.SplitN(h, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return strings.TrimSpace(parts[1])
}
