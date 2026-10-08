package services

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/innzout/ootybites/internal/db"
	"github.com/innzout/ootybites/internal/models"
	"github.com/innzout/ootybites/internal/password"
	"github.com/jackc/pgx/v5"
)

// ErrInvalidLogin is returned for a bad dealer username/password.
var ErrInvalidLogin = errors.New("invalid credentials")

// Dealers is the dealer service: CRUD, auth, and dealer-scoped order handling.
type Dealers struct {
	db *db.Pool
}

// NewDealers builds the dealer service.
func NewDealers(pool *db.Pool) *Dealers { return &Dealers{db: pool} }

// DealerInput carries admin create/update fields.
type DealerInput struct {
	Name     string
	Mobile   string
	Address  *string
	Username string
	Password string // optional on update (blank = keep existing)
	IsActive bool
}

// List returns all dealers (newest first).
func (s *Dealers) List(ctx context.Context) ([]models.Dealer, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id, name, mobile, address, username, is_active, created_at
		 FROM dealers ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list dealers: %w", err)
	}
	defer rows.Close()
	out := []models.Dealer{}
	for rows.Next() {
		var d models.Dealer
		if err := rows.Scan(&d.ID, &d.Name, &d.Mobile, &d.Address, &d.Username, &d.IsActive, &d.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// Create inserts a dealer with a hashed password.
func (s *Dealers) Create(ctx context.Context, in DealerInput) (*models.Dealer, error) {
	hash, err := password.Hash(in.Password)
	if err != nil {
		return nil, err
	}
	var id string
	err = s.db.QueryRow(ctx,
		`INSERT INTO dealers (name, mobile, address, username, password_hash, is_active)
		 VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`,
		in.Name, in.Mobile, in.Address, strings.ToLower(strings.TrimSpace(in.Username)), hash, in.IsActive).Scan(&id)
	if err != nil {
		return nil, fmt.Errorf("insert dealer: %w", err)
	}
	return s.getByID(ctx, id)
}

// Update mutates a dealer; a blank password keeps the existing one.
func (s *Dealers) Update(ctx context.Context, id string, in DealerInput) (*models.Dealer, error) {
	if strings.TrimSpace(in.Password) != "" {
		hash, err := password.Hash(in.Password)
		if err != nil {
			return nil, err
		}
		if _, err := s.db.Exec(ctx, `UPDATE dealers SET password_hash=$2 WHERE id=$1`, id, hash); err != nil {
			return nil, err
		}
	}
	tag, err := s.db.Exec(ctx,
		`UPDATE dealers SET name=$2, mobile=$3, address=$4, username=$5, is_active=$6 WHERE id=$1`,
		id, in.Name, in.Mobile, in.Address, strings.ToLower(strings.TrimSpace(in.Username)), in.IsActive)
	if err != nil {
		return nil, fmt.Errorf("update dealer: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrNotFound
	}
	return s.getByID(ctx, id)
}

// Delete removes a dealer (orders keep their snapshot; dealer_id set null).
func (s *Dealers) Delete(ctx context.Context, id string) error {
	if _, err := s.db.Exec(ctx, `UPDATE orders SET dealer_id=NULL WHERE dealer_id=$1`, id); err != nil {
		return err
	}
	tag, err := s.db.Exec(ctx, `DELETE FROM dealers WHERE id=$1`, id)
	if err != nil {
		return fmt.Errorf("delete dealer: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Authenticate verifies a dealer login and returns the dealer on success.
func (s *Dealers) Authenticate(ctx context.Context, username, plain string) (*models.Dealer, error) {
	var id, hash string
	var active bool
	err := s.db.QueryRow(ctx,
		`SELECT id, password_hash, is_active FROM dealers WHERE username=$1`,
		strings.ToLower(strings.TrimSpace(username))).Scan(&id, &hash, &active)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrInvalidLogin
	}
	if err != nil {
		return nil, err
	}
	if !active || !password.Verify(hash, plain) {
		return nil, ErrInvalidLogin
	}
	return s.getByID(ctx, id)
}

// Exists reports whether a dealer id is still present (for the auth middleware).
func (s *Dealers) Exists(ctx context.Context, id string) (bool, error) {
	var ok bool
	err := s.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM dealers WHERE id=$1)`, id).Scan(&ok)
	return ok, err
}

// Get returns one dealer by id (ErrNotFound if absent).
func (s *Dealers) Get(ctx context.Context, id string) (*models.Dealer, error) {
	return s.getByID(ctx, id)
}

func (s *Dealers) getByID(ctx context.Context, id string) (*models.Dealer, error) {
	var d models.Dealer
	err := s.db.QueryRow(ctx,
		`SELECT id, name, mobile, address, username, is_active, created_at FROM dealers WHERE id=$1`, id).
		Scan(&d.ID, &d.Name, &d.Mobile, &d.Address, &d.Username, &d.IsActive, &d.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &d, nil
}
