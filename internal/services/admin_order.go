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

// OrderStateError signals an illegal status transition or a missing cancel reason.
type OrderStateError struct{ Message string }

func (e *OrderStateError) Error() string { return e.Message }

// allowedTransitions encodes the order state machine (ARCHITECTURE §6):
// placed → reached_dealer|cancelled, reached_dealer → delivered|cancelled,
// delivered/cancelled are terminal.
var allowedTransitions = map[models.OrderStatus]map[models.OrderStatus]bool{
	models.StatusPlaced:        {models.StatusReachedDealer: true, models.StatusCancelled: true},
	models.StatusReachedDealer: {models.StatusDelivered: true, models.StatusCancelled: true},
	models.StatusDelivered:     {},
	models.StatusCancelled:     {},
}

// AdminOrders is the admin-side order service.
type AdminOrders struct {
	db *db.Pool
}

// NewAdminOrders builds the admin order service.
func NewAdminOrders(pool *db.Pool) *AdminOrders { return &AdminOrders{db: pool} }

// nullUUID converts an empty id string to nil so a NULL is stored (e.g. a dealer
// transition has no admin author for order_status_history.changed_by).
func nullUUID(id string) *string {
	if strings.TrimSpace(id) == "" {
		return nil
	}
	return &id
}

// OrderFilter drives the admin order list query.
type OrderFilter struct {
	Status string
	Month  string // YYYY-MM
	From   string // YYYY-MM-DD
	To     string // YYYY-MM-DD
	Sort   string // placed_at | total | status
	Order  string // asc | desc
	Page   int
	Limit  int
}

// List returns a filtered, sorted, paginated page of orders plus the total.
func (s *AdminOrders) List(ctx context.Context, f OrderFilter) ([]models.Order, int, error) {
	if f.Page < 1 {
		f.Page = 1
	}
	if f.Limit < 1 || f.Limit > 100 {
		f.Limit = 20
	}

	where := []string{"1=1"}
	args := []any{}
	add := func(cond string, val any) {
		args = append(args, val)
		where = append(where, fmt.Sprintf(cond, len(args)))
	}

	if f.Status != "" {
		add("status = $%d", f.Status)
	}
	// Custom range wins over month when both are set.
	if f.From != "" && f.To != "" {
		add("placed_at::date >= $%d", f.From)
		add("placed_at::date <= $%d", f.To)
	} else if f.Month != "" {
		add("to_char(placed_at, 'YYYY-MM') = $%d", f.Month)
	}
	whereSQL := strings.Join(where, " AND ")

	var total int
	if err := s.db.QueryRow(ctx, "SELECT count(*) FROM orders WHERE "+whereSQL, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count orders: %w", err)
	}

	// Whitelist sort to avoid injection.
	sortCol := map[string]string{"placed_at": "placed_at", "total": "total", "status": "status"}[f.Sort]
	if sortCol == "" {
		sortCol = "placed_at"
	}
	dir := "DESC"
	if strings.EqualFold(f.Order, "asc") {
		dir = "ASC"
	}

	args = append(args, f.Limit, (f.Page-1)*f.Limit)
	q := fmt.Sprintf(
		`SELECT id, order_number, customer_id, status, subtotal, discount_amount, coupon_code, total,
		        payment_method, ship_name, ship_phone, ship_line1, ship_line2, ship_city, ship_state, ship_pincode, placed_at
		 FROM orders WHERE %s ORDER BY %s %s LIMIT $%d OFFSET $%d`,
		whereSQL, sortCol, dir, len(args)-1, len(args))
	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list orders: %w", err)
	}
	defer rows.Close()
	out := []models.Order{}
	for rows.Next() {
		var o models.Order
		if err := rows.Scan(&o.ID, &o.OrderNumber, &o.CustomerID, &o.Status, &o.Subtotal, &o.DiscountAmount,
			&o.CouponCode, &o.Total, &o.PaymentMethod, &o.ShipName, &o.ShipPhone, &o.ShipLine1, &o.ShipLine2,
			&o.ShipCity, &o.ShipState, &o.ShipPincode, &o.PlacedAt); err != nil {
			return nil, 0, err
		}
		out = append(out, o)
	}
	return out, total, rows.Err()
}

// Get returns one order with items and full status history.
func (s *AdminOrders) Get(ctx context.Context, id string) (*models.Order, error) {
	var o models.Order
	err := s.db.QueryRow(ctx,
		`SELECT o.id, o.order_number, o.customer_id, o.status, o.subtotal, o.discount_amount, o.coupon_code, o.total,
		        o.payment_method, o.ship_name, o.ship_phone, o.ship_line1, o.ship_line2, o.ship_city, o.ship_state, o.ship_pincode,
		        o.placed_at, o.dealer_id, d.name
		 FROM orders o LEFT JOIN dealers d ON d.id = o.dealer_id WHERE o.id=$1`, id).
		Scan(&o.ID, &o.OrderNumber, &o.CustomerID, &o.Status, &o.Subtotal, &o.DiscountAmount, &o.CouponCode, &o.Total,
			&o.PaymentMethod, &o.ShipName, &o.ShipPhone, &o.ShipLine1, &o.ShipLine2, &o.ShipCity, &o.ShipState, &o.ShipPincode,
			&o.PlacedAt, &o.DealerID, &o.DealerName)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get order: %w", err)
	}

	iRows, err := s.db.Query(ctx,
		`SELECT id, variant_id, product_name, variant_label, unit, unit_value, price, qty, line_total
		 FROM order_items WHERE order_id=$1`, id)
	if err != nil {
		return nil, err
	}
	for iRows.Next() {
		var it models.OrderItem
		if err := iRows.Scan(&it.ID, &it.VariantID, &it.ProductName, &it.VariantLabel, &it.Unit,
			&it.UnitValue, &it.Price, &it.Qty, &it.LineTotal); err != nil {
			iRows.Close()
			return nil, err
		}
		o.Items = append(o.Items, it)
	}
	iRows.Close()

	hRows, err := s.db.Query(ctx,
		`SELECT id, status, note, created_at FROM order_status_history
		 WHERE order_id=$1 ORDER BY created_at ASC`, id)
	if err != nil {
		return nil, err
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

// UpdateStatus transitions an order per the state machine. Cancelling requires a
// reason and restores stock. Every transition is logged. All in one transaction.
func (s *AdminOrders) UpdateStatus(ctx context.Context, adminID, id string, next models.OrderStatus, note string) (*models.Order, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var current models.OrderStatus
	if err := tx.QueryRow(ctx, `SELECT status FROM orders WHERE id=$1 FOR UPDATE`, id).Scan(&current); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	if !allowedTransitions[current][next] {
		return nil, &OrderStateError{Message: fmt.Sprintf("Cannot change status from %s to %s", current, next)}
	}
	if next == models.StatusCancelled && strings.TrimSpace(note) == "" {
		return nil, &OrderStateError{Message: "A reason is required to cancel an order"}
	}

	// Cancel restores stock for each line's variant (if still present).
	if next == models.StatusCancelled {
		if _, err := tx.Exec(ctx,
			`UPDATE product_variants v
			 SET stock_qty = stock_qty + oi.qty
			 FROM order_items oi
			 WHERE oi.order_id = $1 AND oi.variant_id = v.id`, id); err != nil {
			return nil, fmt.Errorf("restore stock: %w", err)
		}
	}

	if _, err := tx.Exec(ctx, `UPDATE orders SET status=$2 WHERE id=$1`, id, next); err != nil {
		return nil, fmt.Errorf("update status: %w", err)
	}
	var notePtr *string
	if strings.TrimSpace(note) != "" {
		notePtr = &note
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO order_status_history (order_id, status, changed_by, note) VALUES ($1,$2,$3,$4)`,
		id, next, nullUUID(adminID), notePtr); err != nil {
		return nil, fmt.Errorf("log history: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

// UpdateAddress edits the shipping snapshot while placed/reached_dealer only.
func (s *AdminOrders) UpdateAddress(ctx context.Context, adminID, id string, ship ShippingInput) (*models.Order, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var current models.OrderStatus
	if err := tx.QueryRow(ctx, `SELECT status FROM orders WHERE id=$1 FOR UPDATE`, id).Scan(&current); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if current != models.StatusPlaced && current != models.StatusReachedDealer {
		return nil, &OrderStateError{Message: "Address can only be changed before delivery"}
	}
	if _, err := tx.Exec(ctx,
		`UPDATE orders SET ship_name=$2, ship_phone=$3, ship_line1=$4, ship_line2=$5,
		   ship_city=$6, ship_state=$7, ship_pincode=$8 WHERE id=$1`,
		id, ship.Name, ship.Phone, ship.Line1, ship.Line2, ship.City, ship.State, ship.Pincode); err != nil {
		return nil, fmt.Errorf("update address: %w", err)
	}
	note := "Shipping address updated"
	if _, err := tx.Exec(ctx,
		`INSERT INTO order_status_history (order_id, status, changed_by, note) VALUES ($1,$2,$3,$4)`,
		id, current, adminID, &note); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

// AssignDealer assigns (or clears, when dealerID is nil) the fulfilling dealer.
func (s *AdminOrders) AssignDealer(ctx context.Context, orderID string, dealerID *string, adminID string) (*models.Order, error) {
	tag, err := s.db.Exec(ctx, `UPDATE orders SET dealer_id=$2 WHERE id=$1`, orderID, dealerID)
	if err != nil {
		return nil, fmt.Errorf("assign dealer: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrNotFound
	}
	note := "Unassigned from dealer"
	if dealerID != nil {
		note = "Assigned to dealer"
	}
	var current models.OrderStatus
	if err := s.db.QueryRow(ctx, `SELECT status FROM orders WHERE id=$1`, orderID).Scan(&current); err == nil {
		_, _ = s.db.Exec(ctx,
			`INSERT INTO order_status_history (order_id, status, changed_by, note) VALUES ($1,$2,$3,$4)`,
			orderID, current, adminID, &note)
	}
	return s.Get(ctx, orderID)
}

// ListForDealer returns orders assigned to a dealer (newest first, no items).
func (s *AdminOrders) ListForDealer(ctx context.Context, dealerID string) ([]models.Order, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id, order_number, customer_id, status, subtotal, discount_amount, coupon_code, total,
		        payment_method, ship_name, ship_phone, ship_line1, ship_line2, ship_city, ship_state, ship_pincode, placed_at
		 FROM orders WHERE dealer_id=$1 ORDER BY placed_at DESC`, dealerID)
	if err != nil {
		return nil, fmt.Errorf("list dealer orders: %w", err)
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

// GetForDealer returns one of the dealer's assigned orders (with items+history).
func (s *AdminOrders) GetForDealer(ctx context.Context, dealerID, orderID string) (*models.Order, error) {
	var owner *string
	if err := s.db.QueryRow(ctx, `SELECT dealer_id FROM orders WHERE id=$1`, orderID).Scan(&owner); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if owner == nil || *owner != dealerID {
		return nil, ErrNotFound
	}
	return s.Get(ctx, orderID)
}

// DealerUpdateStatus lets a dealer advance one of their own orders through the
// state machine. Same guards as the admin path (cancel needs a reason + restock).
func (s *AdminOrders) DealerUpdateStatus(ctx context.Context, dealerID, orderID string, next models.OrderStatus, note string) (*models.Order, error) {
	var owner *string
	if err := s.db.QueryRow(ctx, `SELECT dealer_id FROM orders WHERE id=$1`, orderID).Scan(&owner); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if owner == nil || *owner != dealerID {
		return nil, ErrNotFound
	}
	// changed_by references admins; a dealer transition records NULL there.
	return s.UpdateStatus(ctx, "", orderID, next, note)
}

// AddNote appends a free-text note to the order's history (status unchanged).
func (s *AdminOrders) AddNote(ctx context.Context, adminID, id, note string) (*models.Order, error) {
	var current models.OrderStatus
	if err := s.db.QueryRow(ctx, `SELECT status FROM orders WHERE id=$1`, id).Scan(&current); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if _, err := s.db.Exec(ctx,
		`INSERT INTO order_status_history (order_id, status, changed_by, note) VALUES ($1,$2,$3,$4)`,
		id, current, adminID, &note); err != nil {
		return nil, fmt.Errorf("add note: %w", err)
	}
	return s.Get(ctx, id)
}

// DashboardStats is the admin dashboard summary.
type DashboardStats struct {
	TotalOrders    int            `json:"total_orders"`
	Revenue        float64        `json:"revenue"` // delivered only
	CountsByStatus map[string]int `json:"counts_by_status"`
	RecentOrders   []models.Order `json:"recent_orders"`
}

// Stats computes the dashboard figures. Revenue counts delivered orders only;
// cancelled orders are excluded from revenue (ARCHITECTURE §6).
func (s *AdminOrders) Stats(ctx context.Context) (*DashboardStats, error) {
	st := &DashboardStats{CountsByStatus: map[string]int{}}

	if err := s.db.QueryRow(ctx, `SELECT count(*) FROM orders`).Scan(&st.TotalOrders); err != nil {
		return nil, err
	}
	if err := s.db.QueryRow(ctx,
		`SELECT coalesce(sum(total),0) FROM orders WHERE status='delivered'`).Scan(&st.Revenue); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(ctx, `SELECT status, count(*) FROM orders GROUP BY status`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var status string
		var n int
		if err := rows.Scan(&status, &n); err != nil {
			rows.Close()
			return nil, err
		}
		st.CountsByStatus[status] = n
	}
	rows.Close()

	recent, _, err := s.List(ctx, OrderFilter{Sort: "placed_at", Order: "desc", Page: 1, Limit: 5})
	if err != nil {
		return nil, err
	}
	st.RecentOrders = recent
	return st, nil
}
