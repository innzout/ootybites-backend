// Package token issues and verifies the two separate JWT audiences the system
// uses: "customer" (OTP login) and "admin" (username/password). A token minted
// for one audience must never authenticate against the other — enforced here
// and again in the auth middleware.
package token

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Audience values. These populate the JWT `aud` claim and gate route access.
const (
	AudienceCustomer = "customer"
	AudienceAdmin    = "admin"
	AudienceDealer   = "dealer"
)

// Default lifetimes. Sessions are stateless for v1 (no refresh tokens yet).
const (
	CustomerTTL = 30 * 24 * time.Hour
	AdminTTL    = 12 * time.Hour
	DealerTTL   = 12 * time.Hour
)

// Claims is our JWT payload. `Phone` is set for customers, `Username`/`Role`
// for admins; `Subject` (from RegisteredClaims) always carries the entity id.
type Claims struct {
	Username string `json:"username,omitempty"`
	Phone    string `json:"phone,omitempty"`
	Role     string `json:"role,omitempty"` // admin role: super_admin | manager
	jwt.RegisteredClaims
}

var (
	ErrInvalid  = errors.New("invalid token")
	ErrAudience = errors.New("wrong token audience")
)

// IssueCustomer mints a customer-audience token for the given customer id.
func IssueCustomer(secret, customerID, phone string) (string, error) {
	return issue(secret, AudienceCustomer, customerID, CustomerTTL, Claims{Phone: phone})
}

// IssueAdmin mints an admin-audience token for the given admin id, carrying the
// admin's role so route middleware can enforce role-based access.
func IssueAdmin(secret, adminID, username, role string) (string, error) {
	return issue(secret, AudienceAdmin, adminID, AdminTTL, Claims{Username: username, Role: role})
}

// IssueDealer mints a dealer-audience token for the given dealer id.
func IssueDealer(secret, dealerID, username string) (string, error) {
	return issue(secret, AudienceDealer, dealerID, DealerTTL, Claims{Username: username})
}

func issue(secret, audience, subject string, ttl time.Duration, base Claims) (string, error) {
	now := time.Now()
	base.RegisteredClaims = jwt.RegisteredClaims{
		Subject:   subject,
		Audience:  jwt.ClaimStrings{audience},
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, base).SignedString([]byte(secret))
}

// Parse verifies the signature and expiry, and asserts the token carries the
// expected audience. Any mismatch returns ErrAudience.
func Parse(secret, tokenString, wantAudience string) (*Claims, error) {
	claims := &Claims{}
	tok, err := jwt.ParseWithClaims(tokenString, claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, ErrInvalid
		}
		return []byte(secret), nil
	})
	if err != nil || !tok.Valid {
		return nil, ErrInvalid
	}
	for _, aud := range claims.Audience {
		if aud == wantAudience {
			return claims, nil
		}
	}
	return nil, ErrAudience
}
