package services

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/innzout/ootybites/internal/db"
	"github.com/innzout/ootybites/internal/models"
	"github.com/jackc/pgx/v5"
)

// Areas is the delivery-area service (pincode → dealer mapping).
type Areas struct {
	db *db.Pool
}

// NewAreas builds the area service.
func NewAreas(pool *db.Pool) *Areas { return &Areas{db: pool} }

// AreaInput carries admin create/update fields.
type AreaInput struct {
	Code     string
	Name     string
	City     string
	Pincode  string
	DealerID *string
	IsActive bool
}

// List returns all areas with their dealer name.
func (s *Areas) List(ctx context.Context) ([]models.Area, error) {
	rows, err := s.db.Query(ctx,
		`SELECT a.id, a.code, a.name, a.city, a.pincode, a.dealer_id, d.name, a.is_active, a.created_at
		 FROM areas a LEFT JOIN dealers d ON d.id = a.dealer_id
		 ORDER BY a.city, a.name`)
	if err != nil {
		return nil, fmt.Errorf("list areas: %w", err)
	}
	defer rows.Close()
	out := []models.Area{}
	for rows.Next() {
		var a models.Area
		if err := rows.Scan(&a.ID, &a.Code, &a.Name, &a.City, &a.Pincode, &a.DealerID, &a.DealerName, &a.IsActive, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// Create inserts an area.
func (s *Areas) Create(ctx context.Context, in AreaInput) (*models.Area, error) {
	var id string
	err := s.db.QueryRow(ctx,
		`INSERT INTO areas (code, name, city, pincode, dealer_id, is_active)
		 VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`,
		strings.ToUpper(strings.TrimSpace(in.Code)), in.Name, in.City, strings.TrimSpace(in.Pincode), in.DealerID, in.IsActive).Scan(&id)
	if err != nil {
		return nil, fmt.Errorf("insert area: %w", err)
	}
	return s.getByID(ctx, id)
}

// Update mutates an area.
func (s *Areas) Update(ctx context.Context, id string, in AreaInput) (*models.Area, error) {
	tag, err := s.db.Exec(ctx,
		`UPDATE areas SET code=$2, name=$3, city=$4, pincode=$5, dealer_id=$6, is_active=$7 WHERE id=$1`,
		id, strings.ToUpper(strings.TrimSpace(in.Code)), in.Name, in.City, strings.TrimSpace(in.Pincode), in.DealerID, in.IsActive)
	if err != nil {
		return nil, fmt.Errorf("update area: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrNotFound
	}
	return s.getByID(ctx, id)
}

// Delete removes an area.
func (s *Areas) Delete(ctx context.Context, id string) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM areas WHERE id=$1`, id)
	if err != nil {
		return fmt.Errorf("delete area: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Areas) getByID(ctx context.Context, id string) (*models.Area, error) {
	var a models.Area
	err := s.db.QueryRow(ctx,
		`SELECT a.id, a.code, a.name, a.city, a.pincode, a.dealer_id, d.name, a.is_active, a.created_at
		 FROM areas a LEFT JOIN dealers d ON d.id = a.dealer_id WHERE a.id=$1`, id).
		Scan(&a.ID, &a.Code, &a.Name, &a.City, &a.Pincode, &a.DealerID, &a.DealerName, &a.IsActive, &a.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// dealerForPincode returns the active dealer id serving a pincode, or nil. Used
// by order placement to auto-assign. Runs inside the placement transaction.
func dealerForPincode(ctx context.Context, tx pgx.Tx, pincode string) (*string, error) {
	var dealerID *string
	err := tx.QueryRow(ctx,
		`SELECT a.dealer_id FROM areas a JOIN dealers d ON d.id = a.dealer_id
		 WHERE a.pincode = $1 AND a.is_active AND d.is_active
		 ORDER BY a.created_at LIMIT 1`, strings.TrimSpace(pincode)).Scan(&dealerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return dealerID, nil
}
