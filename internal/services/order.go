package services

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/innzout/ootybites/internal/db"
	"github.com/innzout/ootybites/internal/models"
	"github.com/jackc/pgx/v5"
)

// CartError is a user-facing cart problem (unavailable item, out of stock),
// distinct from an internal failure so handlers can return 409 vs 500.
type CartError struct{ Message string }

func (e *CartError) Error() string { return e.Message }

// ErrSessionInvalid means the authenticated subject no longer exists (e.g. the
// account was deleted / data was reset). Handlers map this to 401 so the client
// re-authenticates instead of seeing a raw 500.
var ErrSessionInvalid = errors.New("session invalid")

// CartItemInput is a requested cart line.
type CartItemInput struct {
	VariantID string `json:"variant_id"`
	Qty       int    `json:"qty"`
}

// ShippingInput is the delivery address captured at checkout (snapshotted).
type ShippingInput struct {
	Name    string  `json:"name"`
	Phone   string  `json:"phone"`
	Line1   string  `json:"line1"`
	Line2   *string `json:"line2"`
	City    string  `json:"city"`
	State   string  `json:"state"`
	Pincode string  `json:"pincode"`
}

// validatedLine is a cart line re-priced from the live DB.
type validatedLine struct {
	VariantID    string
	ProductID    string
	ProductName  string
	VariantLabel string
	Unit         models.Unit
	UnitValue    float64
	Price        float64
	Qty          int
	LineTotal    float64
}

// Orders is the order service: cart validation, coupon preview, placement.
type Orders struct {
	db      *db.Pool
	coupons *Coupons
}

// NewOrders builds the order service.
func NewOrders(pool *db.Pool, coupons *Coupons) *Orders {
	return &Orders{db: pool, coupons: coupons}
}

// validateCart re-prices each item against the live DB. Money is never trusted
// from the client — prices and stock come from here (non-negotiable rule 3).
func (s *Orders) validateCart(ctx context.Context, items []CartItemInput) ([]validatedLine, float64, error) {
	if len(items) == 0 {
		return nil, 0, &CartError{Message: "Your cart is empty"}
	}
	var lines []validatedLine
	var subtotal float64
	for _, it := range items {
		if it.Qty < 1 {
			return nil, 0, &CartError{Message: "Item quantity must be at least 1"}
		}
		var l validatedLine
		var vActive, pActive bool
		var stock int
		err := s.db.QueryRow(ctx,
			`SELECT v.id, v.product_id, p.name, v.label, v.unit, v.unit_value, v.price,
			        v.stock_qty, v.is_active, p.is_active
			 FROM product_variants v JOIN products p ON p.id = v.product_id
			 WHERE v.id = $1`, it.VariantID).
			Scan(&l.VariantID, &l.ProductID, &l.ProductName, &l.VariantLabel, &l.Unit,
				&l.UnitValue, &l.Price, &stock, &vActive, &pActive)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, 0, &CartError{Message: "An item in your cart is no longer available"}
		}
		if err != nil {
			return nil, 0, fmt.Errorf("validate cart item: %w", err)
		}
		if !vActive || !pActive {
			return nil, 0, &CartError{Message: fmt.Sprintf("%s (%s) is no longer available", l.ProductName, l.VariantLabel)}
		}
		if stock < it.Qty {
			return nil, 0, &CartError{Message: fmt.Sprintf("Only %d left of %s (%s)", stock, l.ProductName, l.VariantLabel)}
		}
		l.Qty = it.Qty
		l.LineTotal = math.Round(l.Price*float64(it.Qty)*100) / 100
		subtotal += l.LineTotal
		lines = append(lines, l)
	}
	return lines, math.Round(subtotal*100) / 100, nil
}

// PreviewCoupon re-validates the cart and evaluates a coupon without placing an
// order. Used by POST /api/coupons/validate.
func (s *Orders) PreviewCoupon(ctx context.Context, customerID string, items []CartItemInput, code string) (CouponResult, float64, error) {
	lines, subtotal, err := s.validateCart(ctx, items)
	if err != nil {
		return CouponResult{}, 0, err
	}
	coupon, err := s.coupons.GetByCode(ctx, code)
	if errors.Is(err, ErrNotFound) {
		return CouponResult{Reason: "Invalid coupon code"}, subtotal, nil
	}
	if err != nil {
		return CouponResult{}, 0, err
	}
	stats, err := s.coupons.CustomerStatsFor(ctx, customerID, coupon.ID)
	if err != nil {
		return CouponResult{}, 0, err
	}
	return EvaluateCoupon(toCartLines(lines), subtotal, coupon, stats, time.Now()), subtotal, nil
}

// PlaceOrder re-validates the cart, recomputes money authoritatively, decrements
// stock (guarding oversell), and writes the order + items + first history row
// (+ coupon redemption) in a single transaction (ARCHITECTURE §6).
func (s *Orders) PlaceOrder(ctx context.Context, customerID string, items []CartItemInput, code string, ship ShippingInput) (*models.Order, error) {
	// The customer id comes from a verified JWT, but the account could have been
	// removed (e.g. data reset). Fail cleanly with a re-login signal, not a 500.
	var exists bool
	if err := s.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM customers WHERE id=$1)`, customerID).Scan(&exists); err != nil {
		return nil, fmt.Errorf("verify customer: %w", err)
	}
	if !exists {
		return nil, ErrSessionInvalid
	}

	lines, subtotal, err := s.validateCart(ctx, items)
	if err != nil {
		return nil, err
	}

	// Re-run the coupon engine authoritatively (never trust a client total).
	var discount float64
	var coupon *models.Coupon
	if code != "" {
		coupon, err = s.coupons.GetByCode(ctx, code)
		if errors.Is(err, ErrNotFound) {
			return nil, &CartError{Message: "Invalid coupon code"}
		}
		if err != nil {
			return nil, err
		}
		stats, err := s.coupons.CustomerStatsFor(ctx, customerID, coupon.ID)
		if err != nil {
			return nil, err
		}
		res := EvaluateCoupon(toCartLines(lines), subtotal, coupon, stats, time.Now())
		if !res.Valid {
			return nil, &CartError{Message: res.Reason}
		}
		discount = res.Discount
	}
	total := math.Round((subtotal-discount)*100) / 100

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	// Decrement stock with an oversell guard.
	for _, l := range lines {
		tag, err := tx.Exec(ctx,
			`UPDATE product_variants SET stock_qty = stock_qty - $2
			 WHERE id = $1 AND stock_qty >= $2`, l.VariantID, l.Qty)
		if err != nil {
			return nil, fmt.Errorf("decrement stock: %w", err)
		}
		if tag.RowsAffected() != 1 {
			return nil, &CartError{Message: fmt.Sprintf("%s (%s) just went out of stock", l.ProductName, l.VariantLabel)}
		}
	}

	orderNumber, err := nextOrderNumber(ctx, tx)
	if err != nil {
		return nil, err
	}

	var couponID *string
	var couponCode *string
	if coupon != nil {
		couponID = &coupon.ID
		couponCode = &coupon.Code
	}

	var o models.Order
	err = tx.QueryRow(ctx,
		`INSERT INTO orders
		   (order_number, customer_id, status, subtotal, discount_amount, coupon_id, coupon_code,
		    total, payment_method, ship_name, ship_phone, ship_line1, ship_line2, ship_city, ship_state, ship_pincode)
		 VALUES ($1,$2,'placed',$3,$4,$5,$6,$7,'cod',$8,$9,$10,$11,$12,$13,$14)
		 RETURNING id, order_number, customer_id, status, subtotal, discount_amount, coupon_code, total,
		           payment_method, ship_name, ship_phone, ship_line1, ship_line2, ship_city, ship_state, ship_pincode, placed_at`,
		orderNumber, customerID, subtotal, discount, couponID, couponCode, total,
		ship.Name, ship.Phone, ship.Line1, ship.Line2, ship.City, ship.State, ship.Pincode).
		Scan(&o.ID, &o.OrderNumber, &o.CustomerID, &o.Status, &o.Subtotal, &o.DiscountAmount, &o.CouponCode, &o.Total,
			&o.PaymentMethod, &o.ShipName, &o.ShipPhone, &o.ShipLine1, &o.ShipLine2, &o.ShipCity, &o.ShipState, &o.ShipPincode, &o.PlacedAt)
	if err != nil {
		return nil, fmt.Errorf("insert order: %w", err)
	}

	// Snapshot every line item.
	for _, l := range lines {
		var item models.OrderItem
		vid := l.VariantID
		err := tx.QueryRow(ctx,
			`INSERT INTO order_items
			   (order_id, variant_id, product_name, variant_label, unit, unit_value, price, qty, line_total)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
			 RETURNING id, variant_id, product_name, variant_label, unit, unit_value, price, qty, line_total`,
			o.ID, vid, l.ProductName, l.VariantLabel, l.Unit, l.UnitValue, l.Price, l.Qty, l.LineTotal).
			Scan(&item.ID, &item.VariantID, &item.ProductName, &item.VariantLabel, &item.Unit,
				&item.UnitValue, &item.Price, &item.Qty, &item.LineTotal)
		if err != nil {
			return nil, fmt.Errorf("insert order item: %w", err)
		}
		o.Items = append(o.Items, item)
	}

	// First status-history row.
	if _, err := tx.Exec(ctx,
		`INSERT INTO order_status_history (order_id, status, note) VALUES ($1,'placed','Order placed')`,
		o.ID); err != nil {
		return nil, fmt.Errorf("insert history: %w", err)
	}

	// Auto-assign a dealer by delivery pincode (area → dealer mapping).
	dealerID, err := dealerForPincode(ctx, tx, ship.Pincode)
	if err != nil {
		return nil, fmt.Errorf("area lookup: %w", err)
	}
	if dealerID != nil {
		if _, err := tx.Exec(ctx, `UPDATE orders SET dealer_id=$2 WHERE id=$1`, o.ID, *dealerID); err != nil {
			return nil, fmt.Errorf("auto-assign dealer: %w", err)
		}
		o.DealerID = dealerID
		if _, err := tx.Exec(ctx,
			`INSERT INTO order_status_history (order_id, status, note) VALUES ($1,'placed','Auto-assigned to dealer by area')`,
			o.ID); err != nil {
			return nil, fmt.Errorf("insert history: %w", err)
		}
	}

	// Record coupon redemption + bump used_count. The increment is guarded so the
	// total-usage limit is enforced atomically at write time: EvaluateCoupon's
	// earlier check is a read, and concurrent orders could both pass it. The
	// conditional UPDATE (row-locked) lets only one cross the limit boundary.
	if coupon != nil {
		tag, err := tx.Exec(ctx,
			`UPDATE coupons SET used_count = used_count + 1
			 WHERE id = $1 AND (usage_limit_total IS NULL OR used_count < usage_limit_total)`,
			coupon.ID)
		if err != nil {
			return nil, fmt.Errorf("bump used_count: %w", err)
		}
		if tag.RowsAffected() != 1 {
			return nil, &CartError{Message: "This coupon has reached its usage limit"}
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO coupon_redemptions (coupon_id, customer_id, order_id) VALUES ($1,$2,$3)`,
			coupon.ID, customerID, o.ID); err != nil {
			return nil, fmt.Errorf("insert redemption: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return &o, nil
}

// ListForCustomer returns a customer's orders (newest first), without items.
func (s *Orders) ListForCustomer(ctx context.Context, customerID string) ([]models.Order, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id, order_number, customer_id, status, subtotal, discount_amount, coupon_code, total,
		        payment_method, ship_name, ship_phone, ship_line1, ship_line2, ship_city, ship_state, ship_pincode, placed_at
		 FROM orders WHERE customer_id=$1 ORDER BY placed_at DESC`, customerID)
	if err != nil {
		return nil, fmt.Errorf("list orders: %w", err)
	}
	defer rows.Close()
	out := []models.Order{}
	for rows.Next() {
		var o models.Order
		if err := rows.Scan(&o.ID, &o.OrderNumber, &o.CustomerID, &o.Status, &o.Subtotal, &o.DiscountAmount,
			&o.CouponCode, &o.Total, &o.PaymentMethod, &o.ShipName, &o.ShipPhone, &o.ShipLine1, &o.ShipLine2,
			&o.ShipCity, &o.ShipState, &o.ShipPincode, &o.PlacedAt); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// GetForCustomer returns one of the customer's orders with its items.
func (s *Orders) GetForCustomer(ctx context.Context, customerID, orderID string) (*models.Order, error) {
	var o models.Order
	err := s.db.QueryRow(ctx,
		`SELECT id, order_number, customer_id, status, subtotal, discount_amount, coupon_code, total,
		        payment_method, ship_name, ship_phone, ship_line1, ship_line2, ship_city, ship_state, ship_pincode, placed_at
		 FROM orders WHERE id=$1 AND customer_id=$2`, orderID, customerID).
		Scan(&o.ID, &o.OrderNumber, &o.CustomerID, &o.Status, &o.Subtotal, &o.DiscountAmount, &o.CouponCode, &o.Total,
			&o.PaymentMethod, &o.ShipName, &o.ShipPhone, &o.ShipLine1, &o.ShipLine2, &o.ShipCity, &o.ShipState, &o.ShipPincode, &o.PlacedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get order: %w", err)
	}
	rows, err := s.db.Query(ctx,
		`SELECT id, variant_id, product_name, variant_label, unit, unit_value, price, qty, line_total
		 FROM order_items WHERE order_id=$1`, o.ID)
	if err != nil {
		return nil, fmt.Errorf("get order items: %w", err)
	}
	for rows.Next() {
		var it models.OrderItem
		if err := rows.Scan(&it.ID, &it.VariantID, &it.ProductName, &it.VariantLabel, &it.Unit,
			&it.UnitValue, &it.Price, &it.Qty, &it.LineTotal); err != nil {
			rows.Close()
			return nil, err
		}
		o.Items = append(o.Items, it)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Status timeline so the customer can track progress.
	hRows, err := s.db.Query(ctx,
		`SELECT id, status, note, created_at FROM order_status_history
		 WHERE order_id=$1 ORDER BY created_at ASC`, o.ID)
	if err != nil {
		return nil, fmt.Errorf("get order history: %w", err)
	}
	defer hRows.Close()
	for hRows.Next() {
		var h models.OrderStatusHistory
		if err := hRows.Scan(&h.ID, &h.Status, &h.Note, &h.CreatedAt); err != nil {
			return nil, err
		}
		o.History = append(o.History, h)
	}
	return &o, hRows.Err()
}

func toCartLines(lines []validatedLine) []CartLine {
	out := make([]CartLine, len(lines))
	for i, l := range lines {
		out[i] = CartLine{ProductID: l.ProductID, LineTotal: l.LineTotal}
	}
	return out
}

// nextOrderNumber builds OB-YYYYMMDD-NNNN using a per-day running count.
// A transaction-scoped advisory lock (keyed on the date) serializes concurrent
// placements so two orders can't read the same count and collide on the unique
// order_number. The lock releases automatically at commit/rollback.
func nextOrderNumber(ctx context.Context, tx pgx.Tx) (string, error) {
	if _, err := tx.Exec(ctx,
		`SELECT pg_advisory_xact_lock(42, hashtext(current_date::text))`); err != nil {
		return "", fmt.Errorf("lock order seq: %w", err)
	}
	today := time.Now().Format("20060102")
	var seq int
	if err := tx.QueryRow(ctx,
		`SELECT count(*)+1 FROM orders WHERE placed_at::date = current_date`).Scan(&seq); err != nil {
		return "", fmt.Errorf("order number seq: %w", err)
	}
	return fmt.Sprintf("OB-%s-%04d", today, seq), nil
}
