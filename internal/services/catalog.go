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

// ErrNotFound is returned when a catalog entity does not exist.
var ErrNotFound = errors.New("not found")

// Catalog serves the public storefront reads and admin catalog writes.
type Catalog struct {
	db *db.Pool
}

// NewCatalog builds the catalog service.
func NewCatalog(pool *db.Pool) *Catalog { return &Catalog{db: pool} }

// ---------------- Public reads ----------------

// StorefrontQuery drives the public product listing: search, category filter,
// sort and pagination.
type StorefrontQuery struct {
	Q          string // name search
	CategoryID string // filter by category (empty = all)
	Sort       string // newest | price_asc | price_desc | name
	Page       int
	Limit      int
}

// ListProducts returns a page of active products (with active variants and
// images) matching the storefront query, plus the total count for pagination.
func (c *Catalog) ListProducts(ctx context.Context, f StorefrontQuery) ([]models.Product, int, error) {
	if f.Page < 1 {
		f.Page = 1
	}
	if f.Limit < 1 || f.Limit > 100 {
		f.Limit = 20
	}
	offset := (f.Page - 1) * f.Limit

	where := "WHERE is_active = true"
	args := []any{}
	if s := strings.TrimSpace(f.Q); s != "" {
		args = append(args, "%"+s+"%")
		where += fmt.Sprintf(" AND name ILIKE $%d", len(args))
	}
	if id := strings.TrimSpace(f.CategoryID); id != "" {
		args = append(args, id)
		where += fmt.Sprintf(" AND category_id = $%d", len(args))
	}

	// Sort whitelist. Price sorts use the product's lowest active variant price.
	minPrice := "(SELECT min(price) FROM product_variants v WHERE v.product_id = products.id AND v.is_active)"
	orderBy := "created_at DESC"
	switch f.Sort {
	case "price_asc":
		orderBy = minPrice + " ASC NULLS LAST"
	case "price_desc":
		orderBy = minPrice + " DESC NULLS LAST"
	case "name":
		orderBy = "name ASC"
	}

	var total int
	if err := c.db.QueryRow(ctx, "SELECT count(*) FROM products "+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count products: %w", err)
	}

	args = append(args, f.Limit, offset)
	rows, err := c.db.Query(ctx, fmt.Sprintf(
		`SELECT id, name, slug, description, is_active, created_at, category_id
		 FROM products %s ORDER BY %s LIMIT $%d OFFSET $%d`,
		where, orderBy, len(args)-1, len(args)), args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list products: %w", err)
	}
	products, ids, err := scanProducts(rows)
	if err != nil {
		return nil, 0, err
	}
	if err := c.attachChildren(ctx, products, ids, true); err != nil {
		return nil, 0, err
	}
	return products, total, nil
}

// GetBySlug returns a single active product with its variants and images.
func (c *Catalog) GetBySlug(ctx context.Context, slug string) (*models.Product, error) {
	return c.getOne(ctx, "slug = $1 AND is_active = true", slug, true)
}

// ---------------- Admin reads ----------------

// lowStockThreshold mirrors the storefront/admin "low stock" cutoff.
const lowStockThreshold = 5

// AdminProductQuery drives the paginated/searchable/filterable admin product list.
type AdminProductQuery struct {
	Q      string // name/slug search
	Active string // "" (any) | "true" | "false"
	Stock  string // "" (any) | "in" | "low" | "out" (over active variants)
	Page   int
	Limit  int
	Sort   string // name | created_at
	Order  string // asc | desc
}

// AdminListProducts returns a filtered, sorted, paginated page of products
// (active and inactive) with children, plus the total count for pagination.
func (c *Catalog) AdminListProducts(ctx context.Context, f AdminProductQuery) ([]models.Product, int, error) {
	if f.Page < 1 {
		f.Page = 1
	}
	if f.Limit < 1 || f.Limit > 100 {
		f.Limit = 20
	}
	offset := (f.Page - 1) * f.Limit

	where := "WHERE 1=1"
	args := []any{}
	if s := strings.TrimSpace(f.Q); s != "" {
		args = append(args, "%"+s+"%")
		where += fmt.Sprintf(" AND (name ILIKE $%d OR slug ILIKE $%d)", len(args), len(args))
	}
	if f.Active == "true" || f.Active == "false" {
		args = append(args, f.Active == "true")
		where += fmt.Sprintf(" AND is_active = $%d", len(args))
	}
	// Stock filter over a product's ACTIVE variants (products.id correlated).
	switch f.Stock {
	case "in": // at least one active variant comfortably in stock
		where += fmt.Sprintf(
			" AND EXISTS (SELECT 1 FROM product_variants v WHERE v.product_id = products.id AND v.is_active AND v.stock_qty > %d)",
			lowStockThreshold)
	case "low": // some stock, but nothing above the low threshold
		where += fmt.Sprintf(
			" AND EXISTS (SELECT 1 FROM product_variants v WHERE v.product_id = products.id AND v.is_active AND v.stock_qty > 0)"+
				" AND NOT EXISTS (SELECT 1 FROM product_variants v WHERE v.product_id = products.id AND v.is_active AND v.stock_qty > %d)",
			lowStockThreshold)
	case "out": // has active variants, none in stock
		where += " AND EXISTS (SELECT 1 FROM product_variants v WHERE v.product_id = products.id AND v.is_active)" +
			" AND NOT EXISTS (SELECT 1 FROM product_variants v WHERE v.product_id = products.id AND v.is_active AND v.stock_qty > 0)"
	}

	var total int
	if err := c.db.QueryRow(ctx, "SELECT count(*) FROM products "+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count products: %w", err)
	}

	// Whitelist sort to avoid injection.
	sortCol := map[string]string{"name": "name", "created_at": "created_at"}[f.Sort]
	if sortCol == "" {
		sortCol = "created_at"
	}
	dir := "DESC"
	if strings.EqualFold(f.Order, "asc") {
		dir = "ASC"
	}

	args = append(args, f.Limit, offset)
	q := fmt.Sprintf(
		`SELECT id, name, slug, description, is_active, created_at, category_id
		 FROM products %s ORDER BY %s %s LIMIT $%d OFFSET $%d`,
		where, sortCol, dir, len(args)-1, len(args))
	rows, err := c.db.Query(ctx, q, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("admin list products: %w", err)
	}
	products, ids, err := scanProducts(rows)
	if err != nil {
		return nil, 0, err
	}
	if err := c.attachChildren(ctx, products, ids, false); err != nil {
		return nil, 0, err
	}
	return products, total, nil
}

// AdminGetProduct returns one product by id with all children (incl. inactive).
func (c *Catalog) AdminGetProduct(ctx context.Context, id string) (*models.Product, error) {
	return c.getOne(ctx, "id = $1", id, false)
}

// ---------------- Admin writes: products ----------------

// CreateProduct inserts a product and returns it.
func (c *Catalog) CreateProduct(ctx context.Context, name, slug string, description *string, isActive bool, categoryID *string) (*models.Product, error) {
	var p models.Product
	err := c.db.QueryRow(ctx,
		`INSERT INTO products (name, slug, description, is_active, category_id)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING id, name, slug, description, is_active, created_at, category_id`,
		name, slug, description, isActive, categoryID).
		Scan(&p.ID, &p.Name, &p.Slug, &p.Description, &p.IsActive, &p.CreatedAt, &p.CategoryID)
	if err != nil {
		return nil, fmt.Errorf("create product: %w", err)
	}
	p.Variants, p.Images = []models.Variant{}, []models.ProductImage{}
	return &p, nil
}

// UpdateProduct updates a product's editable fields.
func (c *Catalog) UpdateProduct(ctx context.Context, id, name, slug string, description *string, isActive bool, categoryID *string) (*models.Product, error) {
	tag, err := c.db.Exec(ctx,
		`UPDATE products SET name=$2, slug=$3, description=$4, is_active=$5, category_id=$6 WHERE id=$1`,
		id, name, slug, description, isActive, categoryID)
	if err != nil {
		return nil, fmt.Errorf("update product: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrNotFound
	}
	return c.AdminGetProduct(ctx, id)
}

// DeleteProduct removes a product (cascades to variants and images).
func (c *Catalog) DeleteProduct(ctx context.Context, id string) error {
	tag, err := c.db.Exec(ctx, `DELETE FROM products WHERE id=$1`, id)
	if err != nil {
		return fmt.Errorf("delete product: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ---------------- Admin writes: variants ----------------

// VariantInput carries variant fields for create/update.
type VariantInput struct {
	Label     string
	Unit      string
	UnitValue float64
	MRP       float64
	Price     float64
	StockQty  int
	SKU       *string
	IsActive  bool
}

// CreateVariant adds a variant to a product.
func (c *Catalog) CreateVariant(ctx context.Context, productID string, in VariantInput) (*models.Variant, error) {
	var v models.Variant
	err := c.db.QueryRow(ctx,
		`INSERT INTO product_variants
		   (product_id, label, unit, unit_value, mrp, price, stock_qty, sku, is_active)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		 RETURNING id, product_id, label, unit, unit_value, mrp, price, stock_qty, sku, is_active`,
		productID, in.Label, in.Unit, in.UnitValue, in.MRP, in.Price, in.StockQty, in.SKU, in.IsActive).
		Scan(&v.ID, &v.ProductID, &v.Label, &v.Unit, &v.UnitValue, &v.MRP, &v.Price, &v.StockQty, &v.SKU, &v.IsActive)
	if err != nil {
		return nil, fmt.Errorf("create variant: %w", err)
	}
	return &v, nil
}

// UpdateVariant updates a variant.
func (c *Catalog) UpdateVariant(ctx context.Context, id string, in VariantInput) (*models.Variant, error) {
	var v models.Variant
	err := c.db.QueryRow(ctx,
		`UPDATE product_variants
		 SET label=$2, unit=$3, unit_value=$4, mrp=$5, price=$6, stock_qty=$7, sku=$8, is_active=$9
		 WHERE id=$1
		 RETURNING id, product_id, label, unit, unit_value, mrp, price, stock_qty, sku, is_active`,
		id, in.Label, in.Unit, in.UnitValue, in.MRP, in.Price, in.StockQty, in.SKU, in.IsActive).
		Scan(&v.ID, &v.ProductID, &v.Label, &v.Unit, &v.UnitValue, &v.MRP, &v.Price, &v.StockQty, &v.SKU, &v.IsActive)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("update variant: %w", err)
	}
	return &v, nil
}

// DeleteVariant removes a variant.
func (c *Catalog) DeleteVariant(ctx context.Context, id string) error {
	tag, err := c.db.Exec(ctx, `DELETE FROM product_variants WHERE id=$1`, id)
	if err != nil {
		return fmt.Errorf("delete variant: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ---------------- Admin writes: images (Cloudinary bypassed) ----------------

// AddImage attaches an image to a product by URL. Cloudinary signing is bypassed
// for now — the caller passes a direct URL (public_id optional).
func (c *Catalog) AddImage(ctx context.Context, productID, url, publicID string, sortOrder int, isPrimary bool) (*models.ProductImage, error) {
	var img models.ProductImage
	err := c.db.QueryRow(ctx,
		`INSERT INTO product_images (product_id, cloudinary_public_id, url, sort_order, is_primary)
		 VALUES ($1,$2,$3,$4,$5)
		 RETURNING id, product_id, cloudinary_public_id, url, sort_order, is_primary`,
		productID, publicID, url, sortOrder, isPrimary).
		Scan(&img.ID, &img.ProductID, &img.CloudinaryPublicID, &img.URL, &img.SortOrder, &img.IsPrimary)
	if err != nil {
		return nil, fmt.Errorf("add image: %w", err)
	}
	return &img, nil
}

// DeleteImage removes a product image row.
func (c *Catalog) DeleteImage(ctx context.Context, id string) error {
	tag, err := c.db.Exec(ctx, `DELETE FROM product_images WHERE id=$1`, id)
	if err != nil {
		return fmt.Errorf("delete image: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ---------------- internal helpers ----------------

func (c *Catalog) getOne(ctx context.Context, cond, arg string, activeChildren bool) (*models.Product, error) {
	var p models.Product
	err := c.db.QueryRow(ctx, fmt.Sprintf(
		`SELECT id, name, slug, description, is_active, created_at, category_id FROM products WHERE %s`, cond), arg).
		Scan(&p.ID, &p.Name, &p.Slug, &p.Description, &p.IsActive, &p.CreatedAt, &p.CategoryID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get product: %w", err)
	}
	list := []models.Product{p}
	if err := c.attachChildren(ctx, list, []string{p.ID}, activeChildren); err != nil {
		return nil, err
	}
	return &list[0], nil
}

func scanProducts(rows pgx.Rows) ([]models.Product, []string, error) {
	defer rows.Close()
	products := []models.Product{}
	var ids []string
	for rows.Next() {
		var p models.Product
		if err := rows.Scan(&p.ID, &p.Name, &p.Slug, &p.Description, &p.IsActive, &p.CreatedAt, &p.CategoryID); err != nil {
			return nil, nil, fmt.Errorf("scan product: %w", err)
		}
		p.Variants, p.Images = []models.Variant{}, []models.ProductImage{}
		products = append(products, p)
		ids = append(ids, p.ID)
	}
	return products, ids, rows.Err()
}

// attachChildren loads variants and images for the given product ids in two
// batch queries and attaches them, avoiding an N+1 per product.
func (c *Catalog) attachChildren(ctx context.Context, products []models.Product, ids []string, activeOnly bool) error {
	if len(ids) == 0 {
		return nil
	}
	byID := make(map[string]*models.Product, len(products))
	for i := range products {
		// Ensure non-nil slices so the JSON is [] not null (avoids client
		// crashes on .map/.find when a product has no variants/images yet).
		if products[i].Variants == nil {
			products[i].Variants = []models.Variant{}
		}
		if products[i].Images == nil {
			products[i].Images = []models.ProductImage{}
		}
		byID[products[i].ID] = &products[i]
	}

	vCond := ""
	if activeOnly {
		vCond = " AND is_active = true"
	}
	vRows, err := c.db.Query(ctx, fmt.Sprintf(
		`SELECT id, product_id, label, unit, unit_value, mrp, price, stock_qty, sku, is_active
		 FROM product_variants WHERE product_id = ANY($1)%s
		 ORDER BY unit_value ASC`, vCond), ids)
	if err != nil {
		return fmt.Errorf("load variants: %w", err)
	}
	for vRows.Next() {
		var v models.Variant
		if err := vRows.Scan(&v.ID, &v.ProductID, &v.Label, &v.Unit, &v.UnitValue, &v.MRP, &v.Price, &v.StockQty, &v.SKU, &v.IsActive); err != nil {
			vRows.Close()
			return fmt.Errorf("scan variant: %w", err)
		}
		if p := byID[v.ProductID]; p != nil {
			p.Variants = append(p.Variants, v)
		}
	}
	vRows.Close()
	if err := vRows.Err(); err != nil {
		return err
	}

	iRows, err := c.db.Query(ctx,
		`SELECT id, product_id, cloudinary_public_id, url, sort_order, is_primary
		 FROM product_images WHERE product_id = ANY($1)
		 ORDER BY is_primary DESC, sort_order ASC`, ids)
	if err != nil {
		return fmt.Errorf("load images: %w", err)
	}
	defer iRows.Close()
	for iRows.Next() {
		var img models.ProductImage
		if err := iRows.Scan(&img.ID, &img.ProductID, &img.CloudinaryPublicID, &img.URL, &img.SortOrder, &img.IsPrimary); err != nil {
			return fmt.Errorf("scan image: %w", err)
		}
		if p := byID[img.ProductID]; p != nil {
			p.Images = append(p.Images, img)
		}
	}
	return iRows.Err()
}
