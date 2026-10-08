package services

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/innzout/ootybites/internal/db"
	"github.com/innzout/ootybites/internal/models"
	"github.com/jackc/pgx/v5"
)

var slugRe = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// Pages is the CMS content-page service.
type Pages struct {
	db *db.Pool
}

// NewPages builds the pages service.
func NewPages(pool *db.Pool) *Pages { return &Pages{db: pool} }

// PageInput carries admin create/update fields.
type PageInput struct {
	Slug        string
	Title       string
	Body        string
	IsPublished bool
}

func (in PageInput) validate() map[string]string {
	f := map[string]string{}
	if !slugRe.MatchString(strings.TrimSpace(in.Slug)) {
		f["slug"] = "Slug must be lowercase words separated by hyphens"
	}
	if strings.TrimSpace(in.Title) == "" {
		f["title"] = "Title is required"
	}
	if len(f) == 0 {
		return nil
	}
	return f
}

// List returns all pages (admin), newest first.
func (s *Pages) List(ctx context.Context) ([]models.Page, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id, slug, title, body, is_published, created_at, updated_at FROM pages ORDER BY created_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("list pages: %w", err)
	}
	defer rows.Close()
	out := []models.Page{}
	for rows.Next() {
		var p models.Page
		if err := rows.Scan(&p.ID, &p.Slug, &p.Title, &p.Body, &p.IsPublished, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetByID returns one page by id (admin edit).
func (s *Pages) GetByID(ctx context.Context, id string) (*models.Page, error) {
	return s.getOne(ctx, "id = $1", id)
}

// GetPublished returns a published page by slug (public storefront).
func (s *Pages) GetPublished(ctx context.Context, slug string) (*models.Page, error) {
	return s.getOne(ctx, "slug = $1 AND is_published = true", strings.ToLower(strings.TrimSpace(slug)))
}

func (s *Pages) getOne(ctx context.Context, cond, arg string) (*models.Page, error) {
	var p models.Page
	err := s.db.QueryRow(ctx,
		`SELECT id, slug, title, body, is_published, created_at, updated_at FROM pages WHERE `+cond, arg).
		Scan(&p.ID, &p.Slug, &p.Title, &p.Body, &p.IsPublished, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// Create inserts a new page.
func (s *Pages) Create(ctx context.Context, in PageInput) (*models.Page, map[string]string, error) {
	if fields := in.validate(); fields != nil {
		return nil, fields, nil
	}
	var id string
	err := s.db.QueryRow(ctx,
		`INSERT INTO pages (slug, title, body, is_published) VALUES ($1,$2,$3,$4) RETURNING id`,
		strings.ToLower(strings.TrimSpace(in.Slug)), strings.TrimSpace(in.Title), in.Body, in.IsPublished).Scan(&id)
	if err != nil {
		return nil, nil, &CartError{Message: "Could not create page (slug may be taken)"}
	}
	p, err := s.GetByID(ctx, id)
	return p, nil, err
}

// Update mutates a page. Slug stays editable but must remain unique.
func (s *Pages) Update(ctx context.Context, id string, in PageInput) (*models.Page, map[string]string, error) {
	if fields := in.validate(); fields != nil {
		return nil, fields, nil
	}
	tag, err := s.db.Exec(ctx,
		`UPDATE pages SET slug=$2, title=$3, body=$4, is_published=$5, updated_at=now() WHERE id=$1`,
		id, strings.ToLower(strings.TrimSpace(in.Slug)), strings.TrimSpace(in.Title), in.Body, in.IsPublished)
	if err != nil {
		return nil, nil, &CartError{Message: "Could not update page (slug may be taken)"}
	}
	if tag.RowsAffected() == 0 {
		return nil, nil, ErrNotFound
	}
	p, err := s.GetByID(ctx, id)
	return p, nil, err
}

// Delete removes a page.
func (s *Pages) Delete(ctx context.Context, id string) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM pages WHERE id=$1`, id)
	if err != nil {
		return fmt.Errorf("delete page: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
