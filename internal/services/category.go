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

// Categories is the product-category service.
type Categories struct {
	db *db.Pool
}

// NewCategories builds the category service.
func NewCategories(pool *db.Pool) *Categories { return &Categories{db: pool} }

// CategoryInput carries admin create/update fields.
type CategoryInput struct {
	Name      string
	Slug      string
	SortOrder int
	IsActive  bool
}

func (in CategoryInput) validate() map[string]string {
	f := map[string]string{}
	if strings.TrimSpace(in.Name) == "" {
		f["name"] = "Name is required"
	}
	if !slugRe.MatchString(strings.TrimSpace(in.Slug)) {
		f["slug"] = "Slug must be lowercase words separated by hyphens"
	}
	if len(f) == 0 {
		return nil
	}
	return f
}

// List returns all categories (admin), ordered for display.
func (s *Categories) List(ctx context.Context) ([]models.Category, error) {
	return s.query(ctx, `SELECT id, name, slug, sort_order, is_active, created_at FROM categories ORDER BY sort_order, name`)
}

// ListActive returns active categories for the storefront.
func (s *Categories) ListActive(ctx context.Context) ([]models.Category, error) {
	return s.query(ctx, `SELECT id, name, slug, sort_order, is_active, created_at FROM categories WHERE is_active = true ORDER BY sort_order, name`)
}

func (s *Categories) query(ctx context.Context, sql string) ([]models.Category, error) {
	rows, err := s.db.Query(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("list categories: %w", err)
	}
	defer rows.Close()
	out := []models.Category{}
	for rows.Next() {
		var c models.Category
		if err := rows.Scan(&c.ID, &c.Name, &c.Slug, &c.SortOrder, &c.IsActive, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Get returns one category by id.
func (s *Categories) Get(ctx context.Context, id string) (*models.Category, error) {
	var c models.Category
	err := s.db.QueryRow(ctx,
		`SELECT id, name, slug, sort_order, is_active, created_at FROM categories WHERE id=$1`, id).
		Scan(&c.ID, &c.Name, &c.Slug, &c.SortOrder, &c.IsActive, &c.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// Create inserts a category.
func (s *Categories) Create(ctx context.Context, in CategoryInput) (*models.Category, map[string]string, error) {
	if fields := in.validate(); fields != nil {
		return nil, fields, nil
	}
	var id string
	err := s.db.QueryRow(ctx,
		`INSERT INTO categories (name, slug, sort_order, is_active) VALUES ($1,$2,$3,$4) RETURNING id`,
		strings.TrimSpace(in.Name), strings.ToLower(strings.TrimSpace(in.Slug)), in.SortOrder, in.IsActive).Scan(&id)
	if err != nil {
		return nil, nil, &CartError{Message: "Could not create category (slug may be taken)"}
	}
	c, err := s.Get(ctx, id)
	return c, nil, err
}

// Update mutates a category.
func (s *Categories) Update(ctx context.Context, id string, in CategoryInput) (*models.Category, map[string]string, error) {
	if fields := in.validate(); fields != nil {
		return nil, fields, nil
	}
	tag, err := s.db.Exec(ctx,
		`UPDATE categories SET name=$2, slug=$3, sort_order=$4, is_active=$5 WHERE id=$1`,
		id, strings.TrimSpace(in.Name), strings.ToLower(strings.TrimSpace(in.Slug)), in.SortOrder, in.IsActive)
	if err != nil {
		return nil, nil, &CartError{Message: "Could not update category (slug may be taken)"}
	}
	if tag.RowsAffected() == 0 {
		return nil, nil, ErrNotFound
	}
	c, err := s.Get(ctx, id)
	return c, nil, err
}

// Delete removes a category (products are unassigned via ON DELETE SET NULL).
func (s *Categories) Delete(ctx context.Context, id string) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM categories WHERE id=$1`, id)
	if err != nil {
		return fmt.Errorf("delete category: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
