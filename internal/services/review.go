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

// ErrNotPurchased is returned when a customer tries to review a product they
// have no delivered order for.
var ErrNotPurchased = errors.New("no delivered order for this product")

// maxReviewBody caps a review body. Mirrored in lib/validators on the client.
const maxReviewBody = 1000

// Reviews is the product review service.
//
// Two rules live here and nowhere else:
//
//  1. Only a customer with a *delivered* order containing the product may
//     write a review. order_items snapshots the product name but links to the
//     catalogue only through variant_id, so eligibility joins
//     orders -> order_items -> product_variants to reach product_id.
//  2. Reading is for signed-in customers and admins. The handlers enforce that
//     by sitting behind auth; there is no anonymous read path.
type Reviews struct {
	db *db.Pool
}

// NewReviews builds the review service.
func NewReviews(pool *db.Pool) *Reviews { return &Reviews{db: pool} }

// deliveredOrderFor returns the id of a delivered order from this customer that
// contained the product, or "" when there is none.
//
// A variant deleted since purchase leaves order_items.variant_id null, which
// breaks the link to the product - such an order cannot establish eligibility.
func (s *Reviews) deliveredOrderFor(ctx context.Context, customerID, productID string) (string, error) {
	var orderID string
	err := s.db.QueryRow(ctx, `
		SELECT o.id
		FROM orders o
		JOIN order_items oi ON oi.order_id = o.id
		JOIN product_variants v ON v.id = oi.variant_id
		WHERE o.customer_id = $1 AND v.product_id = $2 AND o.status = 'delivered'
		ORDER BY o.placed_at DESC
		LIMIT 1`, customerID, productID).Scan(&orderID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("check delivered order: %w", err)
	}
	return orderID, nil
}

// CanReview reports whether this customer is allowed to review this product.
func (s *Reviews) CanReview(ctx context.Context, customerID, productID string) (bool, error) {
	orderID, err := s.deliveredOrderFor(ctx, customerID, productID)
	return orderID != "", err
}

// Upsert creates or replaces this customer's review of a product. Re-reviewing
// updates the existing row rather than adding a second one, which is what the
// (product_id, customer_id) unique index enforces.
func (s *Reviews) Upsert(ctx context.Context, customerID, productID string, rating int, body string) (*models.Review, error) {
	if rating < 1 || rating > 5 {
		return nil, fmt.Errorf("rating must be between 1 and 5")
	}
	body = strings.TrimSpace(body)
	if len(body) > maxReviewBody {
		body = body[:maxReviewBody]
	}

	orderID, err := s.deliveredOrderFor(ctx, customerID, productID)
	if err != nil {
		return nil, err
	}
	if orderID == "" {
		return nil, ErrNotPurchased
	}

	var r models.Review
	err = s.db.QueryRow(ctx, `
		INSERT INTO product_reviews (product_id, customer_id, order_id, rating, body)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (product_id, customer_id) DO UPDATE
		  SET rating = excluded.rating, body = excluded.body, updated_at = now()
		RETURNING id, product_id, customer_id, rating, body, created_at, updated_at`,
		productID, customerID, orderID, rating, body).
		Scan(&r.ID, &r.ProductID, &r.CustomerID, &r.Rating, &r.Body, &r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("save review: %w", err)
	}
	return &r, nil
}

// ListForProduct returns a product's reviews, newest first, with the reviewer's
// display name. Signed-in reads only - the handler sits behind customer auth.
func (s *Reviews) ListForProduct(ctx context.Context, productID string, limit int) ([]models.Review, float64, int, error) {
	if limit < 1 || limit > 100 {
		limit = 50
	}
	rows, err := s.db.Query(ctx, `
		SELECT r.id, r.product_id, r.customer_id, r.rating, r.body, r.created_at, r.updated_at,
		       coalesce(c.name, '')
		FROM product_reviews r
		JOIN customers c ON c.id = r.customer_id
		WHERE r.product_id = $1
		ORDER BY r.created_at DESC
		LIMIT $2`, productID, limit)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("list reviews: %w", err)
	}
	defer rows.Close()

	out := []models.Review{}
	for rows.Next() {
		var r models.Review
		if err := rows.Scan(&r.ID, &r.ProductID, &r.CustomerID, &r.Rating, &r.Body,
			&r.CreatedAt, &r.UpdatedAt, &r.CustomerName); err != nil {
			return nil, 0, 0, fmt.Errorf("scan review: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, 0, fmt.Errorf("iterate reviews: %w", err)
	}

	// Average over every review, not just this page.
	var avg float64
	var count int
	if err := s.db.QueryRow(ctx,
		`SELECT coalesce(avg(rating), 0), count(*) FROM product_reviews WHERE product_id = $1`,
		productID).Scan(&avg, &count); err != nil {
		return nil, 0, 0, fmt.Errorf("review summary: %w", err)
	}
	return out, avg, count, nil
}

// MineFor returns this customer's own review of a product, or nil.
func (s *Reviews) MineFor(ctx context.Context, customerID, productID string) (*models.Review, error) {
	var r models.Review
	err := s.db.QueryRow(ctx, `
		SELECT id, product_id, customer_id, rating, body, created_at, updated_at
		FROM product_reviews WHERE product_id = $1 AND customer_id = $2`,
		productID, customerID).
		Scan(&r.ID, &r.ProductID, &r.CustomerID, &r.Rating, &r.Body, &r.CreatedAt, &r.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load own review: %w", err)
	}
	return &r, nil
}

// DeleteOwn removes a review, but only if it belongs to this customer. The
// ownership check is part of the DELETE so another customer's id cannot be
// passed in to remove their review.
func (s *Reviews) DeleteOwn(ctx context.Context, customerID, reviewID string) error {
	tag, err := s.db.Exec(ctx,
		`DELETE FROM product_reviews WHERE id = $1 AND customer_id = $2`, reviewID, customerID)
	if err != nil {
		return fmt.Errorf("delete review: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ---------------- Admin ----------------

// ListAll returns every review with its product and customer, newest first, for
// the admin moderation screen.
func (s *Reviews) ListAll(ctx context.Context, limit int) ([]models.Review, error) {
	if limit < 1 || limit > 200 {
		limit = 100
	}
	rows, err := s.db.Query(ctx, `
		SELECT r.id, r.product_id, r.customer_id, r.rating, r.body, r.created_at, r.updated_at,
		       coalesce(c.name, ''), coalesce(c.phone, ''), p.name, p.slug
		FROM product_reviews r
		JOIN customers c ON c.id = r.customer_id
		JOIN products  p ON p.id = r.product_id
		ORDER BY r.created_at DESC
		LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("list all reviews: %w", err)
	}
	defer rows.Close()

	out := []models.Review{}
	for rows.Next() {
		var r models.Review
		if err := rows.Scan(&r.ID, &r.ProductID, &r.CustomerID, &r.Rating, &r.Body,
			&r.CreatedAt, &r.UpdatedAt, &r.CustomerName, &r.CustomerPhone,
			&r.ProductName, &r.ProductSlug); err != nil {
			return nil, fmt.Errorf("scan review: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// AdminDelete removes any review (moderation).
func (s *Reviews) AdminDelete(ctx context.Context, reviewID string) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM product_reviews WHERE id = $1`, reviewID)
	if err != nil {
		return fmt.Errorf("delete review: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
