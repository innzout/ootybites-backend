package services

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/innzout/ootybites/internal/db"
	"github.com/innzout/ootybites/internal/models"
	"github.com/jackc/pgx/v5"
)

// OrderStateError signals an illegal status transition or a missing cancel reason.
type OrderStateError struct{ Message string }

func (e *OrderStateError) Error() string { return e.Message }

// allowedTransitions encodes the (simplified) order state machine:
// placed → delivered|cancelled; delivered/cancelled are terminal.
// (reached_dealer is retained in the enum for history but no longer used.)
var allowedTransitions = map[models.OrderStatus]map[models.OrderStatus]bool{
	models.StatusPlaced:        {models.StatusDelivered: true, models.StatusCancelled: true},
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
	// Ownership scopes. Set by handlers from the signed-in token - never from
	// the request - so a dealer or customer can only ever see their own orders.
	CustomerID string
	DealerID   string
	// Q matches order number, customer name or phone (substring, any case).
	Q      string
	Status string
	Month  string // YYYY-MM
	From   string // YYYY-MM-DD
	To     string // YYYY-MM-DD
	Sort   string // placed_at | total | status
	Order  string // asc | desc
	Page   int
	Limit  int
}

// likeEscaper escapes LIKE/ILIKE wildcards in user search input.
var likeEscaper = strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`)

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

	if f.CustomerID != "" {
		add("customer_id = $%d", f.CustomerID)
	}
	if f.DealerID != "" {
		add("dealer_id = $%d", f.DealerID)
	}
	if f.Status != "" {
		add("status = $%d", f.Status)
	}
	// An explicit date bound wins over month. Either bound may be given alone:
	// previously a range applied only when both were set, so "orders since
	// the 1st" silently returned everything.
	// Sargable date predicates: comparing `placed_at` directly lets
	// idx_orders_status_placed / idx_orders_placed be used. Wrapping the column
	// (placed_at::date, to_char(placed_at, ...)) makes those indexes unusable and
	// forces a full scan of `orders` on every admin page view — exactly what the
	// "index-aware admin order list" rule exists to prevent. The upper bound is
	// exclusive-next-day so the `to` day is still fully included.
	if f.From != "" || f.To != "" {
		if f.From != "" {
			add("placed_at >= $%d::date", f.From)
		}
		if f.To != "" {
			add("placed_at < ($%d::date + 1)", f.To)
		}
	} else if f.Month != "" {
		add("placed_at >= to_date($%d, 'YYYY-MM')", f.Month)
		add("placed_at < to_date($%d, 'YYYY-MM') + interval '1 month'", f.Month)
	}
	if q := strings.TrimSpace(f.Q); q != "" {
		// One parameter used three times. Wildcards in the input are escaped
		// so a search for "50%" means the text, not "anything".
		add(`(order_number ILIKE $%[1]d ESCAPE '\' OR ship_name ILIKE $%[1]d ESCAPE '\' OR ship_phone ILIKE $%[1]d ESCAPE '\')`,
			"%"+likeEscaper.Replace(q)+"%")
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
		        payment_method, ship_name, ship_phone, ship_line1, ship_line2, ship_city, ship_state, ship_pincode, placed_at, is_express
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
			&o.ShipCity, &o.ShipState, &o.ShipPincode, &o.PlacedAt, &o.IsExpress); err != nil {
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
		        o.placed_at, o.dealer_id, d.name, o.hub_id, o.is_express, o.delivery_note, o.ship_lat, o.ship_lng
		 FROM orders o LEFT JOIN dealers d ON d.id = o.dealer_id WHERE o.id=$1`, id).
		Scan(&o.ID, &o.OrderNumber, &o.CustomerID, &o.Status, &o.Subtotal, &o.DiscountAmount, &o.CouponCode, &o.Total,
			&o.PaymentMethod, &o.ShipName, &o.ShipPhone, &o.ShipLine1, &o.ShipLine2, &o.ShipCity, &o.ShipState, &o.ShipPincode,
			&o.PlacedAt, &o.DealerID, &o.DealerName, &o.HubID, &o.IsExpress, &o.DeliveryNote, &o.ShipLat, &o.ShipLng)
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

	// Cancel restores stock to wherever it was taken from (the fulfilling hub for
	// an express order, else central variant stock) and logs a matching
	// 'cancel_restore' movement to the inventory ledger.
	if next == models.StatusCancelled {
		if err := restoreOrderStock(ctx, tx, id); err != nil {
			return nil, err
		}
		// Free the coupon (redemption + usage budget) when an order is cancelled.
		if err := releaseOrderCoupon(ctx, tx, id); err != nil {
			return nil, err
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

	// Notify the customer of the outcome (best-effort).
	if next == models.StatusDelivered || next == models.StatusCancelled {
		var custID, orderNo string
		if err := tx.QueryRow(ctx, `SELECT customer_id, order_number FROM orders WHERE id=$1`, id).Scan(&custID, &orderNo); err == nil {
			title, body := "Order delivered ✅", fmt.Sprintf("Your order %s has been delivered.", orderNo)
			if next == models.StatusCancelled {
				title, body = "Order cancelled", fmt.Sprintf("Your order %s was cancelled.", orderNo)
			}
			insertNotification(ctx, tx, &custID, false, title, &body, &id)
		}
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
	// ship_lat/ship_lng are updated too: a manual edit with no pin sends NULL,
	// which clears the old pin so the map never points at a stale location.
	if _, err := tx.Exec(ctx,
		`UPDATE orders SET ship_name=$2, ship_phone=$3, ship_line1=$4, ship_line2=$5,
		   ship_city=$6, ship_state=$7, ship_pincode=$8, ship_lat=$9, ship_lng=$10 WHERE id=$1`,
		id, ship.Name, ship.Phone, ship.Line1, ship.Line2, ship.City, ship.State, ship.Pincode,
		ship.Lat, ship.Lng); err != nil {
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
	// Transactional, like every other order mutation: the assignment and its
	// audit row must land together. Previously these were three loose statements
	// whose history INSERT errors were discarded, so an order could change hands
	// with no trace of who did it — and CLAUDE.md requires every transition to be
	// logged to order_status_history.
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	// Lock the row so a concurrent status change can't interleave between the
	// status read and the history write.
	var current models.OrderStatus
	err = tx.QueryRow(ctx, `SELECT status FROM orders WHERE id=$1 FOR UPDATE`, orderID).Scan(&current)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("assign dealer: %w", err)
	}
	// A finished order has nobody left to deliver it; reassigning it would only
	// corrupt the dealer's worklist.
	if current == models.StatusDelivered || current == models.StatusCancelled {
		return nil, &OrderStateError{Message: fmt.Sprintf("Cannot reassign a %s order", current)}
	}

	if _, err := tx.Exec(ctx, `UPDATE orders SET dealer_id=$2 WHERE id=$1`, orderID, dealerID); err != nil {
		return nil, fmt.Errorf("assign dealer: %w", err)
	}

	note := "Unassigned from dealer"
	if dealerID != nil {
		note = "Assigned to dealer"
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO order_status_history (order_id, status, changed_by, note) VALUES ($1,$2,$3,$4)`,
		orderID, current, adminID, &note); err != nil {
		return nil, fmt.Errorf("assign dealer history: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("assign dealer commit: %w", err)
	}
	return s.Get(ctx, orderID)
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
	From           string         `json:"from"` // applied range (YYYY-MM-DD), empty = all time
	To             string         `json:"to"`
}

// dateRangePredicate builds an inclusive `placed_at` date filter for the dashboard
// queries. from/to are YYYY-MM-DD; either may be empty (open-ended). It returns a
// SQL fragment (already prefixed with " AND " when non-empty) plus its args, so
// callers append it after an existing WHERE. $1 begins at startIdx.
func dateRangePredicate(from, to string, startIdx int) (string, []any) {
	var sb strings.Builder
	args := []any{}
	i := startIdx
	if from != "" {
		fmt.Fprintf(&sb, " AND placed_at >= $%d::date", i)
		args = append(args, from)
		i++
	}
	if to != "" {
		// Inclusive of the whole `to` day.
		fmt.Fprintf(&sb, " AND placed_at < ($%d::date + 1)", i)
		args = append(args, to)
	}
	return sb.String(), args
}

// Stats computes the dashboard figures within an optional inclusive date range
// (from/to as YYYY-MM-DD; empty = all time). Revenue counts delivered orders
// only; cancelled orders are excluded from revenue (ARCHITECTURE §6).
func (s *AdminOrders) Stats(ctx context.Context, from, to string) (*DashboardStats, error) {
	st := &DashboardStats{CountsByStatus: map[string]int{}, From: from, To: to}

	// total orders in range
	rangeSQL, rangeArgs := dateRangePredicate(from, to, 1)
	if err := s.db.QueryRow(ctx,
		`SELECT count(*) FROM orders WHERE true`+rangeSQL, rangeArgs...).Scan(&st.TotalOrders); err != nil {
		return nil, err
	}
	// revenue (delivered) in range
	revSQL, revArgs := dateRangePredicate(from, to, 1)
	if err := s.db.QueryRow(ctx,
		`SELECT coalesce(sum(total),0) FROM orders WHERE status='delivered'`+revSQL, revArgs...).Scan(&st.Revenue); err != nil {
		return nil, err
	}
	// counts by status in range
	cntSQL, cntArgs := dateRangePredicate(from, to, 1)
	rows, err := s.db.Query(ctx,
		`SELECT status, count(*) FROM orders WHERE true`+cntSQL+` GROUP BY status`, cntArgs...)
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

	recent, _, err := s.List(ctx, OrderFilter{Sort: "placed_at", Order: "desc", Page: 1, Limit: 5, From: from, To: to})
	if err != nil {
		return nil, err
	}
	st.RecentOrders = recent
	return st, nil
}

// ---------------- Orders map ----------------

// OrderMapPoint is one pin on the admin orders map: just enough to draw the
// marker, identify the order, and route to it. Deliberately not the full order
// - a map may plot hundreds of these at once.
type OrderMapPoint struct {
	ID          string    `json:"id"`
	OrderNumber string    `json:"order_number"`
	Status      string    `json:"status"`
	PlacedAt    time.Time `json:"placed_at"`
	Total       float64   `json:"total"`
	IsExpress   bool      `json:"is_express"`

	ShipName    string  `json:"ship_name"`
	ShipPhone   string  `json:"ship_phone"`
	ShipLine1   string  `json:"ship_line1"`
	ShipCity    string  `json:"ship_city"`
	ShipPincode string  `json:"ship_pincode"`
	ShipLat     float64 `json:"ship_lat"`
	ShipLng     float64 `json:"ship_lng"`

	// The hub this order dispatches from - the origin of the delivery route.
	// Null when the order has no hub, or the hub has not been pinned yet; the
	// UI then falls back to the store address.
	HubID   *string  `json:"hub_id"`
	HubName *string  `json:"hub_name"`
	HubLat  *float64 `json:"hub_lat"`
	HubLng  *float64 `json:"hub_lng"`
}

// mapPointLimit caps how many pins one request can return. A map cannot
// usefully show more, and it keeps the payload bounded.
const mapPointLimit = 1000

// MapPoints returns pinned orders for the admin map, newest first.
//
// Only orders that actually carry a delivery pin are returned: an order placed
// before the map picker existed, or one where the customer typed the address
// without pinning it, has nothing to plot and is silently skipped rather than
// dropped at (0,0) in the Atlantic.
func (s *AdminOrders) MapPoints(ctx context.Context, status, from, to string) ([]OrderMapPoint, error) {
	where := "WHERE o.ship_lat IS NOT NULL AND o.ship_lng IS NOT NULL"
	args := []any{}
	if status != "" {
		args = append(args, status)
		where += fmt.Sprintf(" AND o.status = $%d", len(args))
	}
	// Same sargable predicates as the order list, so the status/date indexes
	// still apply (see the note on List).
	rangeSQL, rangeArgs := dateRangePredicate(from, to, len(args)+1)
	if rangeSQL != "" {
		where += strings.ReplaceAll(rangeSQL, " AND placed_at", " AND o.placed_at")
		args = append(args, rangeArgs...)
	}

	args = append(args, mapPointLimit)
	rows, err := s.db.Query(ctx, fmt.Sprintf(`
		SELECT o.id, o.order_number, o.status, o.placed_at, o.total, o.is_express,
		       o.ship_name, o.ship_phone, o.ship_line1, o.ship_city, o.ship_pincode,
		       o.ship_lat, o.ship_lng,
		       o.hub_id, h.name, h.lat, h.lng
		FROM orders o
		LEFT JOIN hubs h ON h.id = o.hub_id
		%s
		ORDER BY o.placed_at DESC
		LIMIT $%d`, where, len(args)), args...)
	if err != nil {
		return nil, fmt.Errorf("orders map: %w", err)
	}
	defer rows.Close()

	out := []OrderMapPoint{}
	for rows.Next() {
		var p OrderMapPoint
		if err := rows.Scan(&p.ID, &p.OrderNumber, &p.Status, &p.PlacedAt, &p.Total, &p.IsExpress,
			&p.ShipName, &p.ShipPhone, &p.ShipLine1, &p.ShipCity, &p.ShipPincode,
			&p.ShipLat, &p.ShipLng,
			&p.HubID, &p.HubName, &p.HubLat, &p.HubLng); err != nil {
			return nil, fmt.Errorf("scan map point: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
