package services

import (
	"context"
	"fmt"

	"github.com/innzout/ootybites/internal/db"
	"github.com/innzout/ootybites/internal/models"
	"github.com/jackc/pgx/v5"
)

// Inventory manages stock levels and the movement ledger. Purchases add stock
// (bought from a vendor), sales/cancels are logged by the order flow, and admins
// can make manual adjustments — all mirrored to product_variants.stock_qty.
type Inventory struct {
	db *db.Pool
}

// NewInventory builds the inventory service.
func NewInventory(pool *db.Pool) *Inventory { return &Inventory{db: pool} }

// Overview returns the current stock for every variant.
func (s *Inventory) Overview(ctx context.Context) ([]models.StockRow, error) {
	rows, err := s.db.Query(ctx,
		`SELECT v.id, v.product_id, p.name, v.label, v.sku, v.stock_qty, v.price, v.is_active
		 FROM product_variants v JOIN products p ON p.id = v.product_id
		 ORDER BY p.name, v.unit_value`)
	if err != nil {
		return nil, fmt.Errorf("stock overview: %w", err)
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

// ReceiveStock records a purchase from a vendor: +qty to the variant and a
// 'purchase' ledger entry (with unit cost) in one transaction.
func (s *Inventory) ReceiveStock(ctx context.Context, variantID string, vendorID *string, qty int, unitCost *float64, note string) error {
	if qty <= 0 {
		return &CartError{Message: "Quantity must be at least 1"}
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	tag, err := tx.Exec(ctx, `UPDATE product_variants SET stock_qty = stock_qty + $2 WHERE id = $1`, variantID, qty)
	if err != nil {
		return fmt.Errorf("receive stock: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if err := insertMovement(ctx, tx, variantID, qty, "purchase", vendorID, nil, unitCost, note); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// AdjustStock applies a manual +/- correction (e.g. damage, recount) and logs it.
// Guards against going negative.
func (s *Inventory) AdjustStock(ctx context.Context, variantID string, delta int, note string) error {
	if delta == 0 {
		return &CartError{Message: "Adjustment cannot be zero"}
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	tag, err := tx.Exec(ctx,
		`UPDATE product_variants SET stock_qty = stock_qty + $2 WHERE id = $1 AND stock_qty + $2 >= 0`,
		variantID, delta)
	if err != nil {
		return fmt.Errorf("adjust stock: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return &CartError{Message: "Adjustment would make stock negative (or variant not found)"}
	}
	if err := insertMovement(ctx, tx, variantID, delta, "adjustment", nil, nil, nil, note); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Movements returns a variant's ledger (newest first) with vendor/order context.
func (s *Inventory) Movements(ctx context.Context, variantID string) ([]models.StockMovement, error) {
	rows, err := s.db.Query(ctx,
		`SELECT m.id, m.variant_id, m.delta, m.reason, m.vendor_id, ve.name, m.order_id, o.order_number,
		        m.unit_cost, m.note, m.created_at
		 FROM stock_movements m
		 LEFT JOIN vendors ve ON ve.id = m.vendor_id
		 LEFT JOIN orders  o  ON o.id  = m.order_id
		 WHERE m.variant_id = $1 ORDER BY m.created_at DESC LIMIT 200`, variantID)
	if err != nil {
		return nil, fmt.Errorf("movements: %w", err)
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

// insertMovement appends one row to the stock ledger (shared by the inventory
// service and the order flow, which pass their own transaction).
func insertMovement(ctx context.Context, tx pgx.Tx, variantID string, delta int, reason string, vendorID, orderID *string, unitCost *float64, note string) error {
	var notePtr *string
	if note != "" {
		notePtr = &note
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO stock_movements (variant_id, delta, reason, vendor_id, order_id, unit_cost, note)
		 VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		variantID, delta, reason, vendorID, orderID, unitCost, notePtr); err != nil {
		return fmt.Errorf("insert stock movement: %w", err)
	}
	return nil
}
