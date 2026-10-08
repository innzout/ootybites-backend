package services

import (
	"context"
	"fmt"

	"github.com/innzout/ootybites/internal/db"
	"github.com/innzout/ootybites/internal/models"
)

// Addresses is the customer address-book service. Every operation is scoped to
// the owning customer so one customer can never touch another's addresses.
type Addresses struct {
	db *db.Pool
}

// NewAddresses builds the address service.
func NewAddresses(pool *db.Pool) *Addresses { return &Addresses{db: pool} }

// AddressInput carries create/update fields.
type AddressInput struct {
	Name      string
	Phone     string
	Line1     string
	Line2     *string
	City      string
	State     string
	Pincode   string
	Lat       *float64
	Lng       *float64
	IsDefault bool
}

// List returns a customer's saved addresses (default first, then newest).
func (s *Addresses) List(ctx context.Context, customerID string) ([]models.Address, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id, customer_id, name, phone, line1, line2, city, state, pincode, lat, lng, is_default, created_at
		 FROM addresses WHERE customer_id=$1 ORDER BY is_default DESC, created_at DESC`, customerID)
	if err != nil {
		return nil, fmt.Errorf("list addresses: %w", err)
	}
	defer rows.Close()
	out := []models.Address{}
	for rows.Next() {
		var a models.Address
		if err := rows.Scan(&a.ID, &a.CustomerID, &a.Name, &a.Phone, &a.Line1, &a.Line2,
			&a.City, &a.State, &a.Pincode, &a.Lat, &a.Lng, &a.IsDefault, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// Create adds an address. First address (or an explicit default) becomes default.
func (s *Addresses) Create(ctx context.Context, customerID string, in AddressInput) (*models.Address, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM addresses WHERE customer_id=$1`, customerID).Scan(&count); err != nil {
		return nil, err
	}
	makeDefault := in.IsDefault || count == 0
	if makeDefault {
		if _, err := tx.Exec(ctx, `UPDATE addresses SET is_default=false WHERE customer_id=$1`, customerID); err != nil {
			return nil, err
		}
	}
	var id string
	err = tx.QueryRow(ctx,
		`INSERT INTO addresses (customer_id, name, phone, line1, line2, city, state, pincode, lat, lng, is_default)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING id`,
		customerID, in.Name, in.Phone, in.Line1, in.Line2, in.City, in.State, in.Pincode, in.Lat, in.Lng, makeDefault).Scan(&id)
	if err != nil {
		return nil, fmt.Errorf("insert address: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.get(ctx, customerID, id)
}

// Update mutates a customer's address.
func (s *Addresses) Update(ctx context.Context, customerID, id string, in AddressInput) (*models.Address, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	if in.IsDefault {
		if _, err := tx.Exec(ctx, `UPDATE addresses SET is_default=false WHERE customer_id=$1`, customerID); err != nil {
			return nil, err
		}
	}
	tag, err := tx.Exec(ctx,
		`UPDATE addresses SET name=$3, phone=$4, line1=$5, line2=$6, city=$7, state=$8, pincode=$9, lat=$10, lng=$11, is_default=$12
		 WHERE id=$1 AND customer_id=$2`,
		id, customerID, in.Name, in.Phone, in.Line1, in.Line2, in.City, in.State, in.Pincode, in.Lat, in.Lng, in.IsDefault)
	if err != nil {
		return nil, fmt.Errorf("update address: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrNotFound
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.get(ctx, customerID, id)
}

// Delete removes a customer's address; if it was the default, promote another.
func (s *Addresses) Delete(ctx context.Context, customerID, id string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var wasDefault bool
	err = tx.QueryRow(ctx, `DELETE FROM addresses WHERE id=$1 AND customer_id=$2 RETURNING is_default`, id, customerID).Scan(&wasDefault)
	if err != nil {
		return ErrNotFound
	}
	if wasDefault {
		// Promote the most recent remaining address to default.
		_, _ = tx.Exec(ctx,
			`UPDATE addresses SET is_default=true WHERE id = (
			   SELECT id FROM addresses WHERE customer_id=$1 ORDER BY created_at DESC LIMIT 1)`, customerID)
	}
	return tx.Commit(ctx)
}

func (s *Addresses) get(ctx context.Context, customerID, id string) (*models.Address, error) {
	var a models.Address
	err := s.db.QueryRow(ctx,
		`SELECT id, customer_id, name, phone, line1, line2, city, state, pincode, lat, lng, is_default, created_at
		 FROM addresses WHERE id=$1 AND customer_id=$2`, id, customerID).
		Scan(&a.ID, &a.CustomerID, &a.Name, &a.Phone, &a.Line1, &a.Line2, &a.City, &a.State, &a.Pincode, &a.Lat, &a.Lng, &a.IsDefault, &a.CreatedAt)
	if err != nil {
		return nil, ErrNotFound
	}
	return &a, nil
}
