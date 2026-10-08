package services

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"sync"
	"time"

	"github.com/innzout/ootybites/internal/config"
	"github.com/innzout/ootybites/internal/db"
	"github.com/innzout/ootybites/internal/models"
	"github.com/innzout/ootybites/internal/password"
	"github.com/innzout/ootybites/internal/token"
	"github.com/jackc/pgx/v5"
)

// Common auth errors surfaced to handlers.
var (
	ErrInvalidOTP         = errors.New("invalid or expired code")
	// ErrOTPAttemptsExceeded means the attempt cap burned the code. Reported
	// distinctly from a plain wrong code: otherwise the customer keeps retyping
	// a code that can no longer ever work, with nothing telling them to request
	// a fresh one.
	ErrOTPAttemptsExceeded = errors.New("too many incorrect attempts")
	ErrInvalidCredentials = errors.New("invalid credentials")
)

const otpTTL = 5 * time.Minute

// Auth handles customer OTP login and admin username/password login.
type Auth struct {
	cfg *config.Config
	db  *db.Pool
	otp OTPStore
	sms SMSSender // nil when MSG91 is unconfigured (dev)

	// Wrong-code attempts per phone for the life of the current code. A 6-digit
	// OTP is only ~20 bits, so without a cap it is brute-forceable within the
	// 5-minute window; route rate-limiting alone throttles but does not stop it.
	attemptsMu sync.Mutex
	attempts   map[string]int
}

// maxOTPAttempts is how many wrong codes a phone may submit before the issued
// code is burned and a new one must be requested.
const maxOTPAttempts = 5

// NewAuth builds the auth service. sms may be nil; live sends then fail loudly
// rather than reporting a success that never reached the customer.
func NewAuth(cfg *config.Config, pool *db.Pool, otp OTPStore, sms SMSSender) *Auth {
	return &Auth{cfg: cfg, db: pool, otp: otp, sms: sms, attempts: map[string]int{}}
}

// RequestOTP generates a code for the phone and "sends" it. In dev mode MSG91 is
// skipped: the code is logged and returned to the caller for convenience. The
// returned string is non-empty only in dev mode.
func (a *Auth) RequestOTP(ctx context.Context, phone string) (devCode string, err error) {
	code := genOTP()

	if a.cfg.OTPDevMode {
		a.otp.Set(phone, code, otpTTL)
		a.resetAttempts(phone)
		slog.Info("dev OTP issued", "phone", phone, "code", code)
		return code, nil
	}

	if a.sms == nil {
		return "", ErrSMSNotConfigured
	}
	// Send BEFORE storing. If delivery fails we must not leave a live code the
	// customer never received — and any previously issued code stays valid.
	if err := a.sms.SendOTP(ctx, phone, code); err != nil {
		// The code must never reach the log in production.
		slog.Error("otp send failed", "phone", phone, "err", err)
		return "", err
	}
	a.otp.Set(phone, code, otpTTL)
	a.resetAttempts(phone)
	slog.Info("otp sent", "phone", phone)
	return "", nil
}

func (a *Auth) resetAttempts(phone string) {
	a.attemptsMu.Lock()
	delete(a.attempts, phone)
	a.attemptsMu.Unlock()
}

// registerFailure counts a wrong code and reports whether the cap is now hit.
func (a *Auth) registerFailure(phone string) bool {
	a.attemptsMu.Lock()
	defer a.attemptsMu.Unlock()
	a.attempts[phone]++
	return a.attempts[phone] >= maxOTPAttempts
}

// VerifyOTP checks the code, finds-or-creates the customer, and issues a
// customer-audience JWT.
func (a *Auth) VerifyOTP(ctx context.Context, phone, code string) (string, *models.Customer, error) {
	stored, ok := a.otp.Get(phone)
	// subtle.ConstantTimeCompare keeps the check free of a timing side channel
	// that would otherwise let an attacker learn the code digit by digit.
	if !ok || subtle.ConstantTimeCompare([]byte(stored), []byte(code)) != 1 {
		if ok && a.registerFailure(phone) {
			// Cap reached — burn the code so guessing must start over from a
			// fresh (rate-limited) request.
			a.otp.Delete(phone)
			a.resetAttempts(phone)
			return "", nil, ErrOTPAttemptsExceeded
		}
		return "", nil, ErrInvalidOTP
	}
	a.otp.Delete(phone)
	a.resetAttempts(phone)

	cust, err := a.findOrCreateCustomer(ctx, phone)
	if err != nil {
		return "", nil, err
	}
	tok, err := token.IssueCustomer(a.cfg.JWTSecret, cust.ID, cust.Phone)
	if err != nil {
		return "", nil, err
	}
	return tok, cust, nil
}

func (a *Auth) findOrCreateCustomer(ctx context.Context, phone string) (*models.Customer, error) {
	var c models.Customer
	err := a.db.QueryRow(ctx,
		`SELECT id, phone, name, created_at FROM customers WHERE phone = $1`, phone).
		Scan(&c.ID, &c.Phone, &c.Name, &c.CreatedAt)
	if err == nil {
		return &c, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("lookup customer: %w", err)
	}
	// Create.
	err = a.db.QueryRow(ctx,
		`INSERT INTO customers (phone) VALUES ($1) RETURNING id, phone, name, created_at`, phone).
		Scan(&c.ID, &c.Phone, &c.Name, &c.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("create customer: %w", err)
	}
	return &c, nil
}

// GetCustomer loads a customer by id (for GET /api/me).
func (a *Auth) GetCustomer(ctx context.Context, id string) (*models.Customer, error) {
	var c models.Customer
	err := a.db.QueryRow(ctx,
		`SELECT id, phone, name, created_at FROM customers WHERE id = $1`, id).
		Scan(&c.ID, &c.Phone, &c.Name, &c.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get customer: %w", err)
	}
	return &c, nil
}

// CustomerExists reports whether a customer id is still present. Used by the
// auth middleware to reject tokens for deleted accounts (→ 401, not 500).
func (a *Auth) CustomerExists(ctx context.Context, id string) (bool, error) {
	var ok bool
	err := a.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM customers WHERE id=$1)`, id).Scan(&ok)
	return ok, err
}

// UpdateCustomerName sets the customer's display name (for PUT /api/me).
func (a *Auth) UpdateCustomerName(ctx context.Context, id, name string) (*models.Customer, error) {
	var c models.Customer
	err := a.db.QueryRow(ctx,
		`UPDATE customers SET name=$2 WHERE id=$1 RETURNING id, phone, name, created_at`, id, name).
		Scan(&c.ID, &c.Phone, &c.Name, &c.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("update customer: %w", err)
	}
	return &c, nil
}

// AdminLogin verifies credentials and issues an admin-audience JWT carrying the
// admin's role.
func (a *Auth) AdminLogin(ctx context.Context, username, plain string) (string, error) {
	var id, hash, role string
	err := a.db.QueryRow(ctx,
		`SELECT id, password_hash, role FROM admins WHERE username = $1`, username).
		Scan(&id, &hash, &role)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrInvalidCredentials
	}
	if err != nil {
		return "", fmt.Errorf("lookup admin: %w", err)
	}
	if !password.Verify(hash, plain) {
		return "", ErrInvalidCredentials
	}
	return token.IssueAdmin(a.cfg.JWTSecret, id, username, role)
}

// AdminExists reports whether an admin id is still present (auth middleware).
func (a *Auth) AdminExists(ctx context.Context, id string) (bool, error) {
	var ok bool
	err := a.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM admins WHERE id=$1)`, id).Scan(&ok)
	return ok, err
}

// GetAdmin loads one admin by id (for GET /admin/me).
func (a *Auth) GetAdmin(ctx context.Context, id string) (*models.Admin, error) {
	var m models.Admin
	err := a.db.QueryRow(ctx,
		`SELECT id, username, name, role, created_at FROM admins WHERE id=$1`, id).
		Scan(&m.ID, &m.Username, &m.Name, &m.Role, &m.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get admin: %w", err)
	}
	return &m, nil
}

// SeedAdmin upserts the bootstrap admin (as super_admin) so the admin panel is
// reachable on a fresh database. Called once at boot. It never downgrades an
// existing admin's role.
func (a *Auth) SeedAdmin(ctx context.Context, username, plain string) error {
	hash, err := password.Hash(plain)
	if err != nil {
		return err
	}
	_, err = a.db.Exec(ctx,
		`INSERT INTO admins (username, password_hash, role) VALUES ($1, $2, 'super_admin')
		 ON CONFLICT (username) DO UPDATE SET password_hash = EXCLUDED.password_hash`,
		username, hash)
	return err
}

// genOTP returns a cryptographically-random 6-digit code.
func genOTP() string {
	n, _ := rand.Int(rand.Reader, big.NewInt(1_000_000))
	return fmt.Sprintf("%06d", n.Int64())
}
