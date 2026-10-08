package services

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/innzout/ootybites/internal/db"
	"github.com/innzout/ootybites/internal/models"
	"github.com/jackc/pgx/v5"
)

// CartLine is one validated cart line the coupon engine reasons over.
type CartLine struct {
	ProductID string
	LineTotal float64
}

// CustomerStats gates account-based coupon conditions.
type CustomerStats struct {
	LifetimeValue  float64 // sum(total) of the customer's delivered orders
	CouponRedeemed int     // this coupon's redemptions by this customer
}

// CouponResult is the engine's verdict.
type CouponResult struct {
	Valid    bool    `json:"valid"`
	Discount float64 `json:"discount"`
	Reason   string  `json:"reason,omitempty"`
}

// EvaluateCoupon is a pure function: given a validated cart, a coupon and the
// customer's stats, it returns whether the coupon applies and the discount.
// Rules run in order and fail fast with a specific reason (ARCHITECTURE §5).
func EvaluateCoupon(lines []CartLine, subtotal float64, c *models.Coupon, stats CustomerStats, now time.Time) CouponResult {
	// 1. active + within validity window
	if !c.IsActive {
		return CouponResult{Reason: "This coupon is not active"}
	}
	if c.ValidFrom != nil && now.Before(*c.ValidFrom) {
		return CouponResult{Reason: "This coupon is not yet valid"}
	}
	if c.ValidTo != nil && now.After(*c.ValidTo) {
		return CouponResult{Reason: "This coupon has expired"}
	}

	// 2. usage limits (total + per-user)
	if c.UsageLimitTotal != nil && c.UsedCount >= *c.UsageLimitTotal {
		return CouponResult{Reason: "This coupon has reached its usage limit"}
	}
	if c.UsageLimitPerUser != nil && stats.CouponRedeemed >= *c.UsageLimitPerUser {
		return CouponResult{Reason: "You have already used this coupon"}
	}

	// 3. scope: compute the applicable subtotal
	applicable := subtotal
	if c.ApplicableScope == "specific_products" {
		set := make(map[string]bool, len(c.ProductIDs))
		for _, id := range c.ProductIDs {
			set[id] = true
		}
		applicable = 0
		for _, l := range lines {
			if set[l.ProductID] {
				applicable += l.LineTotal
			}
		}
		if applicable <= 0 {
			return CouponResult{Reason: "Coupon not applicable to items in your cart"}
		}
	}

	// 4. minimum order value (on the whole cart)
	if subtotal < c.MinOrderValue {
		return CouponResult{Reason: fmt.Sprintf("Add items worth ₹%.0f more to use this coupon", c.MinOrderValue-subtotal)}
	}

	// 5. minimum customer lifetime value
	if stats.LifetimeValue < c.MinCustomerLifetimeValue {
		return CouponResult{Reason: "This coupon is not available for your account yet"}
	}

	// 6. percentage discount, capped
	discount := applicable * c.DiscountValue / 100
	if c.MaxDiscountCap != nil && discount > *c.MaxDiscountCap {
		discount = *c.MaxDiscountCap
	}
	discount = math.Round(discount*100) / 100
	return CouponResult{Valid: true, Discount: discount}
}

// Coupons is the coupon service — loads coupons and customer stats, and runs
// the engine for the preview endpoint.
type Coupons struct {
	db *db.Pool
}

// NewCoupons builds the coupon service.
func NewCoupons(pool *db.Pool) *Coupons { return &Coupons{db: pool} }

// GetByCode loads an active/inactive coupon by code, including its product ids.
func (s *Coupons) GetByCode(ctx context.Context, code string) (*models.Coupon, error) {
	var c models.Coupon
	err := s.db.QueryRow(ctx,
		`SELECT id, code, is_active, discount_type, discount_value, max_discount_cap,
		        applicable_scope, min_order_value, min_customer_lifetime_value,
		        usage_limit_total, usage_limit_per_user, used_count, valid_from, valid_to
		 FROM coupons WHERE upper(code) = upper($1)`, strings.TrimSpace(code)).
		Scan(&c.ID, &c.Code, &c.IsActive, &c.DiscountType, &c.DiscountValue, &c.MaxDiscountCap,
			&c.ApplicableScope, &c.MinOrderValue, &c.MinCustomerLifetimeValue,
			&c.UsageLimitTotal, &c.UsageLimitPerUser, &c.UsedCount, &c.ValidFrom, &c.ValidTo)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load coupon: %w", err)
	}
	if c.ApplicableScope == "specific_products" {
		rows, err := s.db.Query(ctx, `SELECT product_id FROM coupon_products WHERE coupon_id=$1`, c.ID)
		if err != nil {
			return nil, fmt.Errorf("load coupon products: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return nil, err
			}
			c.ProductIDs = append(c.ProductIDs, id)
		}
	}
	return &c, nil
}

// CouponInput carries coupon fields for admin create/update.
type CouponInput struct {
	Code                     string
	IsActive                 bool
	DiscountValue            float64
	MaxDiscountCap           *float64
	ApplicableScope          string
	MinOrderValue            float64
	MinCustomerLifetimeValue float64
	UsageLimitTotal          *int
	UsageLimitPerUser        *int
	ValidFrom                *time.Time
	ValidTo                  *time.Time
	ProductIDs               []string // for specific_products scope
}

// List returns all coupons (newest first) with their product ids.
func (s *Coupons) List(ctx context.Context) ([]models.Coupon, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id, code, is_active, discount_type, discount_value, max_discount_cap,
		        applicable_scope, min_order_value, min_customer_lifetime_value,
		        usage_limit_total, usage_limit_per_user, used_count, valid_from, valid_to
		 FROM coupons ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list coupons: %w", err)
	}
	defer rows.Close()
	out := []models.Coupon{}
	var ids []string
	idx := map[string]int{}
	for rows.Next() {
		var c models.Coupon
		if err := rows.Scan(&c.ID, &c.Code, &c.IsActive, &c.DiscountType, &c.DiscountValue, &c.MaxDiscountCap,
			&c.ApplicableScope, &c.MinOrderValue, &c.MinCustomerLifetimeValue,
			&c.UsageLimitTotal, &c.UsageLimitPerUser, &c.UsedCount, &c.ValidFrom, &c.ValidTo); err != nil {
			return nil, err
		}
		idx[c.ID] = len(out)
		out = append(out, c)
		ids = append(ids, c.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Attach product ids in one query.
	if len(ids) > 0 {
		pr, err := s.db.Query(ctx, `SELECT coupon_id, product_id FROM coupon_products WHERE coupon_id = ANY($1)`, ids)
		if err != nil {
			return nil, fmt.Errorf("load coupon products: %w", err)
		}
		defer pr.Close()
		for pr.Next() {
			var cid, pid string
			if err := pr.Scan(&cid, &pid); err != nil {
				return nil, err
			}
			out[idx[cid]].ProductIDs = append(out[idx[cid]].ProductIDs, pid)
		}
	}
	return out, nil
}

// Create inserts a coupon (+ product links for specific scope).
func (s *Coupons) Create(ctx context.Context, in CouponInput) (*models.Coupon, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var id string
	err = tx.QueryRow(ctx,
		`INSERT INTO coupons
		  (code, is_active, discount_value, max_discount_cap, applicable_scope,
		   min_order_value, min_customer_lifetime_value, usage_limit_total, usage_limit_per_user, valid_from, valid_to)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING id`,
		strings.ToUpper(strings.TrimSpace(in.Code)), in.IsActive, in.DiscountValue, in.MaxDiscountCap, in.ApplicableScope,
		in.MinOrderValue, in.MinCustomerLifetimeValue, in.UsageLimitTotal, in.UsageLimitPerUser, in.ValidFrom, in.ValidTo).
		Scan(&id)
	if err != nil {
		return nil, fmt.Errorf("insert coupon: %w", err)
	}
	if in.ApplicableScope == "specific_products" {
		if err := linkCouponProducts(ctx, tx, id, in.ProductIDs); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.getByID(ctx, id)
}

// Update mutates a coupon and re-links products.
func (s *Coupons) Update(ctx context.Context, id string, in CouponInput) (*models.Coupon, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	tag, err := tx.Exec(ctx,
		`UPDATE coupons SET code=$2, is_active=$3, discount_value=$4, max_discount_cap=$5,
		   applicable_scope=$6, min_order_value=$7, min_customer_lifetime_value=$8,
		   usage_limit_total=$9, usage_limit_per_user=$10, valid_from=$11, valid_to=$12
		 WHERE id=$1`,
		id, strings.ToUpper(strings.TrimSpace(in.Code)), in.IsActive, in.DiscountValue, in.MaxDiscountCap,
		in.ApplicableScope, in.MinOrderValue, in.MinCustomerLifetimeValue,
		in.UsageLimitTotal, in.UsageLimitPerUser, in.ValidFrom, in.ValidTo)
	if err != nil {
		return nil, fmt.Errorf("update coupon: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrNotFound
	}
	if _, err := tx.Exec(ctx, `DELETE FROM coupon_products WHERE coupon_id=$1`, id); err != nil {
		return nil, err
	}
	if in.ApplicableScope == "specific_products" {
		if err := linkCouponProducts(ctx, tx, id, in.ProductIDs); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.getByID(ctx, id)
}

// Delete removes a coupon.
func (s *Coupons) Delete(ctx context.Context, id string) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM coupons WHERE id=$1`, id)
	if err != nil {
		return fmt.Errorf("delete coupon: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Get returns one coupon by id (ErrNotFound if absent).
func (s *Coupons) Get(ctx context.Context, id string) (*models.Coupon, error) {
	return s.getByID(ctx, id)
}

func (s *Coupons) getByID(ctx context.Context, id string) (*models.Coupon, error) {
	var c models.Coupon
	err := s.db.QueryRow(ctx,
		`SELECT id, code, is_active, discount_type, discount_value, max_discount_cap,
		        applicable_scope, min_order_value, min_customer_lifetime_value,
		        usage_limit_total, usage_limit_per_user, used_count, valid_from, valid_to
		 FROM coupons WHERE id=$1`, id).
		Scan(&c.ID, &c.Code, &c.IsActive, &c.DiscountType, &c.DiscountValue, &c.MaxDiscountCap,
			&c.ApplicableScope, &c.MinOrderValue, &c.MinCustomerLifetimeValue,
			&c.UsageLimitTotal, &c.UsageLimitPerUser, &c.UsedCount, &c.ValidFrom, &c.ValidTo)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Query(ctx, `SELECT product_id FROM coupon_products WHERE coupon_id=$1`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var pid string
		if err := rows.Scan(&pid); err != nil {
			return nil, err
		}
		c.ProductIDs = append(c.ProductIDs, pid)
	}
	return &c, nil
}

func linkCouponProducts(ctx context.Context, tx pgx.Tx, couponID string, productIDs []string) error {
	for _, pid := range productIDs {
		if _, err := tx.Exec(ctx,
			`INSERT INTO coupon_products (coupon_id, product_id) VALUES ($1,$2)
			 ON CONFLICT DO NOTHING`, couponID, pid); err != nil {
			return fmt.Errorf("link coupon product: %w", err)
		}
	}
	return nil
}

// CustomerStatsFor computes the gating stats for a customer + coupon.
func (s *Coupons) CustomerStatsFor(ctx context.Context, customerID, couponID string) (CustomerStats, error) {
	var st CustomerStats
	if err := s.db.QueryRow(ctx,
		`SELECT coalesce(sum(total),0) FROM orders WHERE customer_id=$1 AND status='delivered'`,
		customerID).Scan(&st.LifetimeValue); err != nil {
		return st, fmt.Errorf("lifetime value: %w", err)
	}
	if err := s.db.QueryRow(ctx,
		`SELECT count(*) FROM coupon_redemptions WHERE customer_id=$1 AND coupon_id=$2`,
		customerID, couponID).Scan(&st.CouponRedeemed); err != nil {
		return st, fmt.Errorf("redemptions: %w", err)
	}
	return st, nil
}
