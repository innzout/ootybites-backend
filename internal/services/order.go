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
	Name    string   `json:"name"`
	Phone   string   `json:"phone"`
	Line1   string   `json:"line1"`
	Line2   *string  `json:"line2"`
	City    string   `json:"city"`
	State   string   `json:"state"`
	Pincode string   `json:"pincode"`
	Lat     *float64 `json:"lat"`
	Lng     *float64 `json:"lng"`
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
		err := s.db.QueryRow(ctx,
			`SELECT v.id, v.product_id, p.name, v.label, v.unit, v.unit_value, v.price,
			        v.is_active, p.is_active
			 FROM product_variants v JOIN products p ON p.id = v.product_id
			 WHERE v.id = $1`, it.VariantID).
			Scan(&l.VariantID, &l.ProductID, &l.ProductName, &l.VariantLabel, &l.Unit,
				&l.UnitValue, &l.Price, &vActive, &pActive)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, 0, &CartError{Message: "An item in your cart is no longer available"}
		}
		if err != nil {
			return nil, 0, fmt.Errorf("validate cart item: %w", err)
		}
		if !vActive || !pActive {
			return nil, 0, &CartError{Message: fmt.Sprintf("%s (%s) is no longer available", l.ProductName, l.VariantLabel)}
		}
		// Stock availability (hub or central) is enforced by the guarded
		// decrements at placement time, so express orders aren't blocked by an
		// empty central warehouse when the local hub has stock.
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
func (s *Orders) PlaceOrder(ctx context.Context, customerID string, items []CartItemInput, code string, ship ShippingInput, deliveryNote string) (*models.Order, error) {
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

	// Fulfilment: try the local hub first (→ 24-hour express). If its stock can
	// cover the whole cart, it's decremented there; otherwise fall back to the
	// central warehouse with the oversell guard.
	hubID, dealerID, express, err := tryHubExpress(ctx, tx, ship.Pincode, lines)
	if err != nil {
		return nil, fmt.Errorf("hub express: %w", err)
	}
	if !express {
		for _, l := range lines {
			tag, err := tx.Exec(ctx,
				`UPDATE product_variants SET stock_qty = stock_qty - $2
				 WHERE id = $1 AND stock_qty >= $2`, l.VariantID, l.Qty)
			if err != nil {
				return nil, fmt.Errorf("decrement stock: %w", err)
			}
			if tag.RowsAffected() != 1 {
				return nil, &CartError{Message: fmt.Sprintf("%s (%s) is out of stock", l.ProductName, l.VariantLabel)}
			}
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

	var notePtr *string
	if n := strings.TrimSpace(deliveryNote); n != "" {
		notePtr = &n
	}

	var o models.Order
	err = tx.QueryRow(ctx,
		`INSERT INTO orders
		   (order_number, customer_id, status, subtotal, discount_amount, coupon_id, coupon_code,
		    total, payment_method, ship_name, ship_phone, ship_line1, ship_line2, ship_city, ship_state, ship_pincode,
		    dealer_id, hub_id, is_express, ship_lat, ship_lng, delivery_note)
		 VALUES ($1,$2,'placed',$3,$4,$5,$6,$7,'cod',$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20)
		 RETURNING id, order_number, customer_id, status, subtotal, discount_amount, coupon_code, total,
		           payment_method, ship_name, ship_phone, ship_line1, ship_line2, ship_city, ship_state, ship_pincode, ship_lat, ship_lng, placed_at, delivery_note`,
		orderNumber, customerID, subtotal, discount, couponID, couponCode, total,
		ship.Name, ship.Phone, ship.Line1, ship.Line2, ship.City, ship.State, ship.Pincode,
		dealerID, hubID, express, ship.Lat, ship.Lng, notePtr).
		Scan(&o.ID, &o.OrderNumber, &o.CustomerID, &o.Status, &o.Subtotal, &o.DiscountAmount, &o.CouponCode, &o.Total,
			&o.PaymentMethod, &o.ShipName, &o.ShipPhone, &o.ShipLine1, &o.ShipLine2, &o.ShipCity, &o.ShipState, &o.ShipPincode, &o.ShipLat, &o.ShipLng, &o.PlacedAt, &o.DeliveryNote)
	if err != nil {
		return nil, fmt.Errorf("insert order: %w", err)
	}
	o.DealerID, o.HubID, o.IsExpress = dealerID, hubID, express

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

	// Ledger: a 'sale' movement per line, tagged with the hub when fulfilled
	// locally (express) so hub and central stock are tracked separately.
	var moveHub *string
	if express {
		moveHub = hubID
	}
	for _, l := range lines {
		if _, err := tx.Exec(ctx,
			`INSERT INTO stock_movements (variant_id, delta, reason, order_id, hub_id) VALUES ($1,$2,'sale',$3,$4)`,
			l.VariantID, -l.Qty, o.ID, moveHub); err != nil {
			return nil, fmt.Errorf("log sale movement: %w", err)
		}
	}

	// Status history — note express/hub fulfilment.
	firstNote := "Order placed"
	if express {
		firstNote = "Order placed · 24-hour express from local hub"
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO order_status_history (order_id, status, note) VALUES ($1,'placed',$2)`,
		o.ID, firstNote); err != nil {
		return nil, fmt.Errorf("insert history: %w", err)
	}

	// Notifications (best-effort): confirm to the customer, alert the admin feed.
	custBody := fmt.Sprintf("Your order %s has been placed.", o.OrderNumber)
	if express {
		custBody = fmt.Sprintf("Your order %s is on its way — 24-hour delivery.", o.OrderNumber)
	}
	insertNotification(ctx, tx, &customerID, false, "Order placed 🎉", &custBody, &o.ID)
	adminBody := fmt.Sprintf("New order %s · ₹%.0f", o.OrderNumber, total)
	insertNotification(ctx, tx, nil, true, "New order received", &adminBody, &o.ID)

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

// restoreOrderStock returns a cancelled order's line quantities to wherever they
// were taken from: the fulfilling hub for an express order, otherwise central
// variant stock. It logs a matching (hub-tagged for express) 'cancel_restore'
// movement. Lines whose variant was since deleted (variant_id NULL) are skipped.
func restoreOrderStock(ctx context.Context, tx pgx.Tx, orderID string) error {
	var express bool
	var hubID *string
	if err := tx.QueryRow(ctx,
		`SELECT is_express, hub_id FROM orders WHERE id=$1`, orderID).Scan(&express, &hubID); err != nil {
		return fmt.Errorf("load order fulfilment: %w", err)
	}

	// Express: restore to the hub the order was fulfilled from.
	if express && hubID != nil {
		if _, err := tx.Exec(ctx,
			`INSERT INTO hub_stock (hub_id, variant_id, stock_qty)
			 SELECT $1, oi.variant_id, oi.qty FROM order_items oi
			 WHERE oi.order_id = $2 AND oi.variant_id IS NOT NULL
			 ON CONFLICT (hub_id, variant_id)
			   DO UPDATE SET stock_qty = hub_stock.stock_qty + EXCLUDED.stock_qty`,
			*hubID, orderID); err != nil {
			return fmt.Errorf("restore hub stock: %w", err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO stock_movements (variant_id, delta, reason, order_id, hub_id)
			 SELECT variant_id, qty, 'cancel_restore', $1, $2 FROM order_items
			 WHERE order_id = $1 AND variant_id IS NOT NULL`, orderID, *hubID); err != nil {
			return fmt.Errorf("log restore movement: %w", err)
		}
		return nil
	}

	// Standard: restore to central variant stock.
	if _, err := tx.Exec(ctx,
		`UPDATE product_variants v SET stock_qty = stock_qty + oi.qty
		 FROM order_items oi WHERE oi.order_id = $1 AND oi.variant_id = v.id`, orderID); err != nil {
		return fmt.Errorf("restore stock: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO stock_movements (variant_id, delta, reason, order_id)
		 SELECT variant_id, qty, 'cancel_restore', $1 FROM order_items
		 WHERE order_id = $1 AND variant_id IS NOT NULL`, orderID); err != nil {
		return fmt.Errorf("log restore movement: %w", err)
	}
	return nil
}

// releaseOrderCoupon reverses a cancelled order's coupon usage: it removes the
// redemption row and decrements the coupon's used_count, so cancelling frees the
// coupon for the customer (and the global usage budget) again. No-op when the
// order carried no coupon.
func releaseOrderCoupon(ctx context.Context, tx pgx.Tx, orderID string) error {
	var couponID *string
	if err := tx.QueryRow(ctx, `SELECT coupon_id FROM orders WHERE id=$1`, orderID).Scan(&couponID); err != nil {
		return fmt.Errorf("load order coupon: %w", err)
	}
	if couponID == nil {
		return nil
	}
	if _, err := tx.Exec(ctx, `DELETE FROM coupon_redemptions WHERE order_id=$1`, orderID); err != nil {
		return fmt.Errorf("delete redemption: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE coupons SET used_count = GREATEST(used_count - 1, 0) WHERE id=$1`, *couponID); err != nil {
		return fmt.Errorf("decrement coupon usage: %w", err)
	}
	return nil
}

// Cancel lets a customer cancel their OWN order while it is still 'placed'.
// It restores stock (mirrors the admin cancel), logs history, and notifies —
// all in one transaction. Once an order has moved past 'placed' the customer can
// no longer self-cancel (they must contact support / the dealer handles it).
func (s *Orders) Cancel(ctx context.Context, customerID, orderID, reason string) (*models.Order, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var status, owner string
	err = tx.QueryRow(ctx, `SELECT status, customer_id FROM orders WHERE id=$1 FOR UPDATE`, orderID).Scan(&status, &owner)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	// Don't leak other customers' orders — treat as not found.
	if owner != customerID {
		return nil, ErrNotFound
	}
	if status != string(models.StatusPlaced) {
		return nil, &CartError{Message: "This order can no longer be cancelled. Please contact support."}
	}

	// Restore stock to wherever it was taken from (hub for express, else central).
	if err := restoreOrderStock(ctx, tx, orderID); err != nil {
		return nil, err
	}
	// Give the coupon back (free the redemption + usage budget).
	if err := releaseOrderCoupon(ctx, tx, orderID); err != nil {
		return nil, err
	}

	if _, err := tx.Exec(ctx, `UPDATE orders SET status='cancelled' WHERE id=$1`, orderID); err != nil {
		return nil, fmt.Errorf("cancel order: %w", err)
	}
	note := strings.TrimSpace(reason)
	if note == "" {
		note = "Cancelled by customer"
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO order_status_history (order_id, status, note) VALUES ($1,'cancelled',$2)`,
		orderID, note); err != nil {
		return nil, fmt.Errorf("log history: %w", err)
	}

	var orderNo string
	_ = tx.QueryRow(ctx, `SELECT order_number FROM orders WHERE id=$1`, orderID).Scan(&orderNo)
	custBody := fmt.Sprintf("Your order %s was cancelled.", orderNo)
	insertNotification(ctx, tx, &customerID, false, "Order cancelled", &custBody, &orderID)
	adminBody := fmt.Sprintf("Order %s was cancelled by the customer.", orderNo)
	insertNotification(ctx, tx, nil, true, "Order cancelled by customer", &adminBody, &orderID)

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return s.GetForCustomer(ctx, customerID, orderID)
}

// ReorderLine is a cart-ready line built from a past order but re-priced against
// the LIVE product/variant (so reorder uses current price/availability, not the
// old snapshot). Items whose product/variant is gone or inactive are skipped.
type ReorderLine struct {
	VariantID    string  `json:"variant_id"`
	ProductSlug  string  `json:"product_slug"`
	ProductName  string  `json:"product_name"`
	VariantLabel string  `json:"variant_label"`
	Unit         string  `json:"unit"`
	UnitValue    float64 `json:"unit_value"`
	Price        float64 `json:"price"`
	Qty          int     `json:"qty"`
	ImageURL     *string `json:"image_url"`
}

// Reorder returns cart-ready lines for a customer's past order (still-available
// items only), for the "buy again" flow.
func (s *Orders) Reorder(ctx context.Context, customerID, orderID string) ([]ReorderLine, error) {
	var owner string
	err := s.db.QueryRow(ctx, `SELECT customer_id FROM orders WHERE id=$1`, orderID).Scan(&owner)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && owner != customerID) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Query(ctx,
		`SELECT oi.variant_id, p.slug, p.name, v.label, v.unit, v.unit_value, v.price, oi.qty,
		        (SELECT url FROM product_images pi WHERE pi.product_id = p.id
		         ORDER BY pi.is_primary DESC, pi.sort_order ASC LIMIT 1)
		 FROM order_items oi
		 JOIN product_variants v ON v.id = oi.variant_id AND v.is_active
		 JOIN products p ON p.id = v.product_id AND p.is_active
		 WHERE oi.order_id = $1`, orderID)
	if err != nil {
		return nil, fmt.Errorf("reorder lines: %w", err)
	}
	defer rows.Close()
	out := []ReorderLine{}
	for rows.Next() {
		var l ReorderLine
		if err := rows.Scan(&l.VariantID, &l.ProductSlug, &l.ProductName, &l.VariantLabel,
			&l.Unit, &l.UnitValue, &l.Price, &l.Qty, &l.ImageURL); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// ListForCustomer returns a customer's orders (newest first), without items.
func (s *Orders) ListForCustomer(ctx context.Context, customerID string) ([]models.Order, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id, order_number, customer_id, status, subtotal, discount_amount, coupon_code, total,
		        payment_method, ship_name, ship_phone, ship_line1, ship_line2, ship_city, ship_state, ship_pincode, placed_at, is_express
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
			&o.ShipCity, &o.ShipState, &o.ShipPincode, &o.PlacedAt, &o.IsExpress); err != nil {
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
		        payment_method, ship_name, ship_phone, ship_line1, ship_line2, ship_city, ship_state, ship_pincode, ship_lat, ship_lng, placed_at, is_express, delivery_note
		 FROM orders WHERE id=$1 AND customer_id=$2`, orderID, customerID).
		Scan(&o.ID, &o.OrderNumber, &o.CustomerID, &o.Status, &o.Subtotal, &o.DiscountAmount, &o.CouponCode, &o.Total,
			&o.PaymentMethod, &o.ShipName, &o.ShipPhone, &o.ShipLine1, &o.ShipLine2, &o.ShipCity, &o.ShipState, &o.ShipPincode, &o.ShipLat, &o.ShipLng, &o.PlacedAt, &o.IsExpress, &o.DeliveryNote)
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
	// Prefix is admin-configurable via settings; fall back to "OB" if unset.
	prefix := "OB"
	var p string
	if err := tx.QueryRow(ctx, `SELECT order_number_prefix FROM settings WHERE id=1`).Scan(&p); err == nil {
		if p = strings.TrimSpace(p); p != "" {
			prefix = p
		}
	}
	return fmt.Sprintf("%s-%s-%04d", prefix, today, seq), nil
}
