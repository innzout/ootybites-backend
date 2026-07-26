package services

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
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
	ErrInvalidCredentials = errors.New("invalid credentials")
)

const otpTTL = 5 * time.Minute

// Auth handles customer OTP login and admin username/password login.
type Auth struct {
	cfg *config.Config
	db  *db.Pool
	otp OTPStore
}

// NewAuth builds the auth service.
func NewAuth(cfg *config.Config, pool *db.Pool, otp OTPStore) *Auth {
	return &Auth{cfg: cfg, db: pool, otp: otp}
}

// RequestOTP generates a code for the phone and "sends" it. In dev mode MSG91 is
// skipped: the code is logged and returned to the caller for convenience. The
// returned string is non-empty only in dev mode.
func (a *Auth) RequestOTP(ctx context.Context, phone string) (devCode string, err error) {
	code := genOTP()
	a.otp.Set(phone, code, otpTTL)

	if a.cfg.OTPDevMode {
		slog.Info("dev OTP issued", "phone", phone, "code", code)
		return code, nil
	}
	// TODO(build step: MSG91): send SMS via MSG91 here.
	slog.Info("OTP issued (send not wired)", "phone", phone)
	return "", nil
}

// VerifyOTP checks the code, finds-or-creates the customer, and issues a
// customer-audience JWT.
func (a *Auth) VerifyOTP(ctx context.Context, phone, code string) (string, *models.Customer, error) {
	stored, ok := a.otp.Get(phone)
	if !ok || stored != code {
		return "", nil, ErrInvalidOTP
	}
	a.otp.Delete(phone)

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

// AdminLogin verifies credentials and issues an admin-audience JWT.
func (a *Auth) AdminLogin(ctx context.Context, username, plain string) (string, error) {
	var id, hash string
	err := a.db.QueryRow(ctx,
		`SELECT id, password_hash FROM admins WHERE username = $1`, username).
		Scan(&id, &hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrInvalidCredentials
	}
	if err != nil {
		return "", fmt.Errorf("lookup admin: %w", err)
	}
	if !password.Verify(hash, plain) {
		return "", ErrInvalidCredentials
	}
	return token.IssueAdmin(a.cfg.JWTSecret, id, username)
}

// SeedAdmin upserts the bootstrap admin so the admin panel is reachable on a
// fresh database. Called once at boot.
func (a *Auth) SeedAdmin(ctx context.Context, username, plain string) error {
	hash, err := password.Hash(plain)
	if err != nil {
		return err
	}
	_, err = a.db.Exec(ctx,
		`INSERT INTO admins (username, password_hash) VALUES ($1, $2)
		 ON CONFLICT (username) DO UPDATE SET password_hash = EXCLUDED.password_hash`,
		username, hash)
	return err
}

// genOTP returns a cryptographically-random 6-digit code.
func genOTP() string {
	n, _ := rand.Int(rand.Reader, big.NewInt(1_000_000))
	return fmt.Sprintf("%06d", n.Int64())
}
