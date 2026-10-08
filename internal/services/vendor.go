package services

import (
	"context"
	"errors"
	"fmt"

	"github.com/innzout/ootybites/internal/db"
	"github.com/innzout/ootybites/internal/models"
	"github.com/jackc/pgx/v5"
)

// Vendors is the supplier service (who we buy stock from).
type Vendors struct {
	db *db.Pool
}

// NewVendors builds the vendor service.
func NewVendors(pool *db.Pool) *Vendors { return &Vendors{db: pool} }

// VendorInput carries admin create/update fields.
type VendorInput struct {
	Name     string
	Phone    *string
	Location string
	Notes    *string
	IsActive bool
}

func (s *Vendors) List(ctx context.Context) ([]models.Vendor, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id, name, phone, location, notes, is_active, created_at FROM vendors ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list vendors: %w", err)
	}
	defer rows.Close()
	out := []models.Vendor{}
	for rows.Next() {
		var v models.Vendor
		if err := rows.Scan(&v.ID, &v.Name, &v.Phone, &v.Location, &v.Notes, &v.IsActive, &v.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Vendors) Create(ctx context.Context, in VendorInput) (*models.Vendor, error) {
	loc := in.Location
	if loc == "" {
		loc = "Ooty"
	}
	var id string
	err := s.db.QueryRow(ctx,
		`INSERT INTO vendors (name, phone, location, notes, is_active) VALUES ($1,$2,$3,$4,$5) RETURNING id`,
		in.Name, in.Phone, loc, in.Notes, in.IsActive).Scan(&id)
	if err != nil {
		return nil, fmt.Errorf("insert vendor: %w", err)
	}
	return s.getByID(ctx, id)
}

func (s *Vendors) Update(ctx context.Context, id string, in VendorInput) (*models.Vendor, error) {
	tag, err := s.db.Exec(ctx,
		`UPDATE vendors SET name=$2, phone=$3, location=$4, notes=$5, is_active=$6 WHERE id=$1`,
		id, in.Name, in.Phone, in.Location, in.Notes, in.IsActive)
	if err != nil {
		return nil, fmt.Errorf("update vendor: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrNotFound
	}
	return s.getByID(ctx, id)
}

func (s *Vendors) Delete(ctx context.Context, id string) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM vendors WHERE id=$1`, id)
	if err != nil {
		return fmt.Errorf("delete vendor: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Get returns one vendor by id (ErrNotFound if absent).
func (s *Vendors) Get(ctx context.Context, id string) (*models.Vendor, error) {
	return s.getByID(ctx, id)
}

func (s *Vendors) getByID(ctx context.Context, id string) (*models.Vendor, error) {
	var v models.Vendor
	err := s.db.QueryRow(ctx,
		`SELECT id, name, phone, location, notes, is_active, created_at FROM vendors WHERE id=$1`, id).
		Scan(&v.ID, &v.Name, &v.Phone, &v.Location, &v.Notes, &v.IsActive, &v.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &v, nil
}
