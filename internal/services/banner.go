package services

import (
	"context"
	"errors"
	"fmt"

	"github.com/innzout/ootybites/internal/db"
	"github.com/innzout/ootybites/internal/models"
	"github.com/jackc/pgx/v5"
)

// Banners is the promo-banner service.
type Banners struct {
	db *db.Pool
}

// NewBanners builds the banner service.
func NewBanners(pool *db.Pool) *Banners { return &Banners{db: pool} }

// BannerInput carries admin create/update fields.
type BannerInput struct {
	Title     *string
	ImageURL  string
	LinkURL   *string
	SortOrder int
	IsActive  bool
}

// ListActive returns the banners shown on the storefront, in display order.
func (s *Banners) ListActive(ctx context.Context) ([]models.Banner, error) {
	return s.query(ctx, `WHERE is_active = true`)
}

// ListAll returns every banner (admin), active first then by sort order.
func (s *Banners) ListAll(ctx context.Context) ([]models.Banner, error) {
	return s.query(ctx, ``)
}

func (s *Banners) query(ctx context.Context, where string) ([]models.Banner, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id, title, image_url, link_url, sort_order, is_active, created_at
		 FROM banners `+where+` ORDER BY sort_order ASC, created_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("list banners: %w", err)
	}
	defer rows.Close()
	out := []models.Banner{}
	for rows.Next() {
		var b models.Banner
		if err := rows.Scan(&b.ID, &b.Title, &b.ImageURL, &b.LinkURL, &b.SortOrder, &b.IsActive, &b.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// Create inserts a banner.
func (s *Banners) Create(ctx context.Context, in BannerInput) (*models.Banner, error) {
	var id string
	if err := s.db.QueryRow(ctx,
		`INSERT INTO banners (title, image_url, link_url, sort_order, is_active)
		 VALUES ($1,$2,$3,$4,$5) RETURNING id`,
		in.Title, in.ImageURL, in.LinkURL, in.SortOrder, in.IsActive).Scan(&id); err != nil {
		return nil, fmt.Errorf("insert banner: %w", err)
	}
	return s.getByID(ctx, id)
}

// Update mutates a banner.
func (s *Banners) Update(ctx context.Context, id string, in BannerInput) (*models.Banner, error) {
	tag, err := s.db.Exec(ctx,
		`UPDATE banners SET title=$2, image_url=$3, link_url=$4, sort_order=$5, is_active=$6 WHERE id=$1`,
		id, in.Title, in.ImageURL, in.LinkURL, in.SortOrder, in.IsActive)
	if err != nil {
		return nil, fmt.Errorf("update banner: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrNotFound
	}
	return s.getByID(ctx, id)
}

// Delete removes a banner.
func (s *Banners) Delete(ctx context.Context, id string) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM banners WHERE id=$1`, id)
	if err != nil {
		return fmt.Errorf("delete banner: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Get returns one banner by id (ErrNotFound if absent).
func (s *Banners) Get(ctx context.Context, id string) (*models.Banner, error) {
	return s.getByID(ctx, id)
}

func (s *Banners) getByID(ctx context.Context, id string) (*models.Banner, error) {
	var b models.Banner
	err := s.db.QueryRow(ctx,
		`SELECT id, title, image_url, link_url, sort_order, is_active, created_at
		 FROM banners WHERE id=$1`, id).
		Scan(&b.ID, &b.Title, &b.ImageURL, &b.LinkURL, &b.SortOrder, &b.IsActive, &b.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &b, nil
}
