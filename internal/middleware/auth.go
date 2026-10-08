package middleware

import (
	"context"
	"net/http"
	"strings"

	"github.com/innzout/ootybites/internal/token"
	"github.com/innzout/ootybites/pkg/response"
)

// ExistsFunc reports whether the authenticated entity (by id) still exists.
// Used to reject tokens whose account was deleted (→ 401, re-login) instead of
// letting downstream handlers fail with a confusing 500.
type ExistsFunc func(ctx context.Context, id string) (bool, error)

// RequireCustomer accepts only valid customer-audience tokens whose customer
// still exists.
func RequireCustomer(secret string, exists ExistsFunc) func(http.Handler) http.Handler {
	return requireAudience(secret, token.AudienceCustomer, exists)
}

// RequireAdmin accepts only valid admin-audience tokens whose admin still exists.
// A customer token can never satisfy this (enforced by the audience check), per
// the two-audience rule. It also stores the admin's role in context.
func RequireAdmin(secret string, exists ExistsFunc) func(http.Handler) http.Handler {
	return requireAudience(secret, token.AudienceAdmin, exists)
}

// RequireSuperAdmin gates a route to super_admin only. It must be chained AFTER
// RequireAdmin (which populates the role in context); a manager gets 403.
func RequireSuperAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if RoleFrom(r.Context()) != "super_admin" {
			response.Fail(w, http.StatusForbidden, response.CodeForbidden, "Requires a super-admin account")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireDealer accepts only valid dealer-audience tokens whose dealer exists.
func RequireDealer(secret string, exists ExistsFunc) func(http.Handler) http.Handler {
	return requireAudience(secret, token.AudienceDealer, exists)
}

func requireAudience(secret, audience string, exists ExistsFunc) func(http.Handler) http.Handler {
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
			// Reject a token whose account no longer exists (e.g. data reset).
			if exists != nil {
				ok, err := exists(r.Context(), claims.Subject)
				if err != nil {
					response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Auth check failed")
					return
				}
				if !ok {
					response.Fail(w, http.StatusUnauthorized, response.CodeUnauthorized, "Your session has expired. Please sign in again.")
					return
				}
			}
			ctx := context.WithValue(r.Context(), ctxSubject, claims.Subject)
			ctx = context.WithValue(ctx, ctxAudience, audience)
			ctx = context.WithValue(ctx, ctxRole, claims.Role)
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
