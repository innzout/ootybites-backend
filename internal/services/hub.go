package services

import (
	"context"
	"errors"
	"fmt"

	"github.com/innzout/ootybites/internal/db"
	"github.com/innzout/ootybites/internal/models"
	"github.com/jackc/pgx/v5"
)

// Hubs is the fulfilment-hub service: CRUD, area assignment, per-hub stock, and
// the express (24-hour) availability check.
type Hubs struct {
	db *db.Pool
}

// NewHubs builds the hub service.
func NewHubs(pool *db.Pool) *Hubs { return &Hubs{db: pool} }

// HubInput carries admin create/update fields.
type HubInput struct {
	Name     string
	DealerID *string
	IsActive bool
}

// List returns hubs with dealer name and how many areas they serve.
func (s *Hubs) List(ctx context.Context) ([]models.Hub, error) {
	rows, err := s.db.Query(ctx,
		`SELECT h.id, h.name, h.dealer_id, d.name, h.is_active,
		        (SELECT count(*) FROM areas a WHERE a.hub_id = h.id), h.created_at
		 FROM hubs h LEFT JOIN dealers d ON d.id = h.dealer_id
		 ORDER BY h.name`)
	if err != nil {
		return nil, fmt.Errorf("list hubs: %w", err)
	}
	defer rows.Close()
	out := []models.Hub{}
	for rows.Next() {
		var h models.Hub
		if err := rows.Scan(&h.ID, &h.Name, &h.DealerID, &h.DealerName, &h.IsActive, &h.AreaCount, &h.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

func (s *Hubs) Create(ctx context.Context, in HubInput) (*models.Hub, error) {
	var id string
	if err := s.db.QueryRow(ctx,
		`INSERT INTO hubs (name, dealer_id, is_active) VALUES ($1,$2,$3) RETURNING id`,
		in.Name, in.DealerID, in.IsActive).Scan(&id); err != nil {
		return nil, fmt.Errorf("insert hub: %w", err)
	}
	return s.getByID(ctx, id)
}

func (s *Hubs) Update(ctx context.Context, id string, in HubInput) (*models.Hub, error) {
	tag, err := s.db.Exec(ctx, `UPDATE hubs SET name=$2, dealer_id=$3, is_active=$4 WHERE id=$1`,
		id, in.Name, in.DealerID, in.IsActive)
	if err != nil {
		return nil, fmt.Errorf("update hub: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrNotFound
	}
	return s.getByID(ctx, id)
}

func (s *Hubs) Delete(ctx context.Context, id string) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM hubs WHERE id=$1`, id)
	if err != nil {
		return fmt.Errorf("delete hub: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// AssignAreas sets the hub's served areas to exactly `areaIDs`. Because an area
// has a single hub_id, assigning it here removes it from any other hub — so an
// area can never belong to two hubs.
func (s *Hubs) AssignAreas(ctx context.Context, hubID string, areaIDs []string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `UPDATE areas SET hub_id=NULL WHERE hub_id=$1`, hubID); err != nil {
		return err
	}
	if len(areaIDs) > 0 {
		if _, err := tx.Exec(ctx, `UPDATE areas SET hub_id=$1 WHERE id = ANY($2)`, hubID, areaIDs); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// StockOverview returns a hub's per-variant stock (0 when not yet stocked).
func (s *Hubs) StockOverview(ctx context.Context, hubID string) ([]models.StockRow, error) {
	rows, err := s.db.Query(ctx,
		`SELECT v.id, v.product_id, p.name, v.label, v.sku,
		        coalesce(hs.stock_qty, 0), v.price, v.is_active
		 FROM product_variants v
		 JOIN products p ON p.id = v.product_id
		 LEFT JOIN hub_stock hs ON hs.variant_id = v.id AND hs.hub_id = $1
		 ORDER BY p.name, v.unit_value`, hubID)
	if err != nil {
		return nil, fmt.Errorf("hub stock: %w", err)
	}
	defer rows.Close()
	out := []models.StockRow{}
	for rows.Next() {
		var r models.StockRow
		if err := rows.Scan(&r.VariantID, &r.ProductID, &r.ProductName, &r.VariantLabel, &r.SKU, &r.StockQty, &r.Price, &r.IsActive); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ReceiveStock adds stock to a hub (purchase) and logs a hub-tagged movement.
func (s *Hubs) ReceiveStock(ctx context.Context, hubID, variantID string, vendorID *string, qty int, unitCost *float64, note string) error {
	if qty <= 0 {
		return &CartError{Message: "Quantity must be at least 1"}
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx,
		`INSERT INTO hub_stock (hub_id, variant_id, stock_qty) VALUES ($1,$2,$3)
		 ON CONFLICT (hub_id, variant_id) DO UPDATE SET stock_qty = hub_stock.stock_qty + EXCLUDED.stock_qty`,
		hubID, variantID, qty); err != nil {
		return fmt.Errorf("receive hub stock: %w", err)
	}
	var notePtr *string
	if note != "" {
		notePtr = &note
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO stock_movements (variant_id, delta, reason, vendor_id, unit_cost, note, hub_id)
		 VALUES ($1,$2,'purchase',$3,$4,$5,$6)`,
		variantID, qty, vendorID, unitCost, notePtr, hubID); err != nil {
		return fmt.Errorf("log hub movement: %w", err)
	}
	return tx.Commit(ctx)
}

// AdjustStock applies a manual +/- correction to a hub's stock and logs it.
func (s *Hubs) AdjustStock(ctx context.Context, hubID, variantID string, delta int, note string) error {
	if delta == 0 {
		return &CartError{Message: "Adjustment cannot be zero"}
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var have int
	e := tx.QueryRow(ctx, `SELECT stock_qty FROM hub_stock WHERE hub_id=$1 AND variant_id=$2 FOR UPDATE`, hubID, variantID).Scan(&have)
	if e != nil && !errors.Is(e, pgx.ErrNoRows) {
		return e
	}
	if have+delta < 0 {
		return &CartError{Message: "Adjustment would make hub stock negative"}
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO hub_stock (hub_id, variant_id, stock_qty) VALUES ($1,$2,$3)
		 ON CONFLICT (hub_id, variant_id) DO UPDATE SET stock_qty = hub_stock.stock_qty + $3`,
		hubID, variantID, delta); err != nil {
		return fmt.Errorf("adjust hub stock: %w", err)
	}
	var notePtr *string
	if note != "" {
		notePtr = &note
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO stock_movements (variant_id, delta, reason, note, hub_id) VALUES ($1,$2,'adjustment',$3,$4)`,
		variantID, delta, notePtr, hubID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Movements returns a hub's recent stock ledger (all variants).
func (s *Hubs) Movements(ctx context.Context, hubID string) ([]models.StockMovement, error) {
	rows, err := s.db.Query(ctx,
		`SELECT m.id, m.variant_id, m.delta, m.reason, m.vendor_id, ve.name, m.order_id, o.order_number,
		        m.unit_cost, m.note, m.created_at
		 FROM stock_movements m
		 LEFT JOIN vendors ve ON ve.id = m.vendor_id
		 LEFT JOIN orders  o  ON o.id  = m.order_id
		 WHERE m.hub_id = $1 ORDER BY m.created_at DESC LIMIT 100`, hubID)
	if err != nil {
		return nil, fmt.Errorf("hub movements: %w", err)
	}
	defer rows.Close()
	out := []models.StockMovement{}
	for rows.Next() {
		var m models.StockMovement
		if err := rows.Scan(&m.ID, &m.VariantID, &m.Delta, &m.Reason, &m.VendorID, &m.VendorName,
			&m.OrderID, &m.OrderNo, &m.UnitCost, &m.Note, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ExpressProducts returns the products a hub can deliver in 24 hours right now:
// active products whose active variants have hub stock > 0. Each returned variant
// carries the HUB stock in StockQty (not central stock), so the storefront shows
// only what's actually available for express from this hub. Includes images.
func (s *Hubs) ExpressProducts(ctx context.Context, hubID string) ([]models.Product, error) {
	rows, err := s.db.Query(ctx,
		`SELECT p.id, p.name, p.slug, p.description, p.is_active, p.created_at,
		        v.id, v.product_id, v.label, v.unit, v.unit_value, v.mrp, v.price, hs.stock_qty, v.sku, v.is_active
		 FROM hub_stock hs
		 JOIN product_variants v ON v.id = hs.variant_id AND v.is_active = true
		 JOIN products p ON p.id = v.product_id AND p.is_active = true
		 WHERE hs.hub_id = $1 AND hs.stock_qty > 0
		 ORDER BY p.name, v.unit_value`, hubID)
	if err != nil {
		return nil, fmt.Errorf("express products: %w", err)
	}
	defer rows.Close()

	order := []string{}
	byID := map[string]*models.Product{}
	for rows.Next() {
		var p models.Product
		var v models.Variant
		if err := rows.Scan(&p.ID, &p.Name, &p.Slug, &p.Description, &p.IsActive, &p.CreatedAt,
			&v.ID, &v.ProductID, &v.Label, &v.Unit, &v.UnitValue, &v.MRP, &v.Price, &v.StockQty, &v.SKU, &v.IsActive); err != nil {
			return nil, err
		}
		cur, ok := byID[p.ID]
		if !ok {
			p.Variants = []models.Variant{}
			p.Images = []models.ProductImage{}
			byID[p.ID] = &p
			cur = &p
			order = append(order, p.ID)
		}
		cur.Variants = append(cur.Variants, v)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(order) == 0 {
		return []models.Product{}, nil
	}

	// Attach images in one batch.
	iRows, err := s.db.Query(ctx,
		`SELECT id, product_id, cloudinary_public_id, url, sort_order, is_primary
		 FROM product_images WHERE product_id = ANY($1)
		 ORDER BY is_primary DESC, sort_order ASC`, order)
	if err != nil {
		return nil, fmt.Errorf("express product images: %w", err)
	}
	defer iRows.Close()
	for iRows.Next() {
		var img models.ProductImage
		if err := iRows.Scan(&img.ID, &img.ProductID, &img.CloudinaryPublicID, &img.URL, &img.SortOrder, &img.IsPrimary); err != nil {
			return nil, err
		}
		if p := byID[img.ProductID]; p != nil {
			p.Images = append(p.Images, img)
		}
	}
	if err := iRows.Err(); err != nil {
		return nil, err
	}

	out := make([]models.Product, 0, len(order))
	for _, id := range order {
		out = append(out, *byID[id])
	}
	return out, nil
}

// ExpressCheck reports whether the hub serving a pincode can fulfil the whole
// cart from its own stock (→ 24-hour delivery). Returns the hub name too.
func (s *Hubs) ExpressCheck(ctx context.Context, pincode string, items []CartItemInput) (bool, *string, error) {
	var hubID string
	var hubName string
	err := s.db.QueryRow(ctx,
		`SELECT h.id, h.name FROM areas a JOIN hubs h ON h.id = a.hub_id
		 WHERE a.pincode=$1 AND a.is_active AND h.is_active ORDER BY a.created_at LIMIT 1`, pincode).
		Scan(&hubID, &hubName)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil, nil
	}
	if err != nil {
		return false, nil, err
	}
	for _, it := range items {
		var have int
		if err := s.db.QueryRow(ctx,
			`SELECT coalesce(stock_qty,0) FROM hub_stock WHERE hub_id=$1 AND variant_id=$2`,
			hubID, it.VariantID).Scan(&have); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return false, nil, err
		}
		if have < it.Qty {
			return false, &hubName, nil
		}
	}
	return true, &hubName, nil
}

// Get returns one hub by id (ErrNotFound if absent).
func (s *Hubs) Get(ctx context.Context, id string) (*models.Hub, error) {
	return s.getByID(ctx, id)
}

func (s *Hubs) getByID(ctx context.Context, id string) (*models.Hub, error) {
	var h models.Hub
	err := s.db.QueryRow(ctx,
		`SELECT h.id, h.name, h.dealer_id, d.name, h.is_active,
		        (SELECT count(*) FROM areas a WHERE a.hub_id = h.id), h.created_at
		 FROM hubs h LEFT JOIN dealers d ON d.id = h.dealer_id WHERE h.id=$1`, id).
		Scan(&h.ID, &h.Name, &h.DealerID, &h.DealerName, &h.IsActive, &h.AreaCount, &h.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &h, nil
}

// tryHubExpress attempts to fulfil an order from the pincode's hub stock inside
// the placement transaction. If the hub has enough of every line, it decrements
// hub stock, logs movements, and returns (hubID, dealerID, true). Otherwise it
// returns (nil, nil, false) and the caller falls back to central stock.
func tryHubExpress(ctx context.Context, tx pgx.Tx, pincode string, lines []validatedLine) (hubID, dealerID *string, express bool, err error) {
	hubID, dealerID, err = hubForPincode(ctx, tx, pincode)
	if err != nil || hubID == nil {
		return nil, nil, false, err
	}
	// Phase 1: lock + verify the hub has enough of EVERY line (no decrements yet,
	// so we can cleanly fall back to central stock if it can't fulfil).
	for _, l := range lines {
		var have int
		e := tx.QueryRow(ctx,
			`SELECT stock_qty FROM hub_stock WHERE hub_id=$1 AND variant_id=$2 FOR UPDATE`,
			*hubID, l.VariantID).Scan(&have)
		if errors.Is(e, pgx.ErrNoRows) {
			return nil, nil, false, nil // not stocked at this hub → not express
		}
		if e != nil {
			return nil, nil, false, e
		}
		if have < l.Qty {
			return nil, nil, false, nil
		}
	}
	// Phase 2: all lines available — decrement hub stock. Sale movements are
	// logged by the caller once the order id exists.
	for _, l := range lines {
		if _, err := tx.Exec(ctx,
			`UPDATE hub_stock SET stock_qty = stock_qty - $3 WHERE hub_id=$1 AND variant_id=$2`,
			*hubID, l.VariantID, l.Qty); err != nil {
			return nil, nil, false, err
		}
	}
	return hubID, dealerID, true, nil
}
