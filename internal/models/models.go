// Package models holds the domain structs shared across services and handlers.
// JSON tags define the API wire shape; DB reads scan into these directly.
package models

import "time"

// Unit mirrors the unit_type enum.
type Unit string

// OrderStatus mirrors the order_status enum.
type OrderStatus string

const (
	StatusPlaced        OrderStatus = "placed"
	StatusReachedDealer OrderStatus = "reached_dealer"
	StatusDelivered     OrderStatus = "delivered"
	StatusCancelled     OrderStatus = "cancelled"
)

// Customer is a phone-OTP identity.
type Customer struct {
	ID        string    `json:"id"`
	Phone     string    `json:"phone"`
	Name      *string   `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

// Address is a saved delivery address.
type Address struct {
	ID         string    `json:"id"`
	CustomerID string    `json:"-"`
	Name       string    `json:"name"`
	Phone      string    `json:"phone"`
	Line1      string    `json:"line1"`
	Line2      *string   `json:"line2"`
	City       string    `json:"city"`
	State      string    `json:"state"`
	Pincode    string    `json:"pincode"`
	IsDefault  bool      `json:"is_default"`
	CreatedAt  time.Time `json:"created_at"`
}

// Product is the catalog listing; price/stock live on its variants.
type Product struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Slug        string         `json:"slug"`
	Description *string        `json:"description"`
	IsActive    bool           `json:"is_active"`
	Images      []ProductImage `json:"images"`
	Variants    []Variant      `json:"variants"`
	CreatedAt   time.Time      `json:"created_at"`
}

// ProductImage is one Cloudinary-hosted image for a product.
type ProductImage struct {
	ID                 string `json:"id"`
	ProductID          string `json:"-"`
	CloudinaryPublicID string `json:"cloudinary_public_id"`
	URL                string `json:"url"`
	SortOrder          int    `json:"sort_order"`
	IsPrimary          bool   `json:"is_primary"`
}

// Variant is the sellable unit — the only place price and stock live.
type Variant struct {
	ID        string  `json:"id"`
	ProductID string  `json:"product_id"`
	Label     string  `json:"label"`
	Unit      Unit    `json:"unit"`
	UnitValue float64 `json:"unit_value"`
	MRP       float64 `json:"mrp"`
	Price     float64 `json:"price"`
	StockQty  int     `json:"stock_qty"`
	SKU       *string `json:"sku"`
	IsActive  bool    `json:"is_active"`
}

// Coupon mirrors the coupons table.
type Coupon struct {
	ID                       string     `json:"id"`
	Code                     string     `json:"code"`
	IsActive                 bool       `json:"is_active"`
	DiscountType             string     `json:"discount_type"`
	DiscountValue            float64    `json:"discount_value"`
	MaxDiscountCap           *float64   `json:"max_discount_cap"`
	ApplicableScope          string     `json:"applicable_scope"`
	MinOrderValue            float64    `json:"min_order_value"`
	MinCustomerLifetimeValue float64    `json:"min_customer_lifetime_value"`
	UsageLimitTotal          *int       `json:"usage_limit_total"`
	UsageLimitPerUser        *int       `json:"usage_limit_per_user"`
	UsedCount                int        `json:"used_count"`
	ValidFrom                *time.Time `json:"valid_from"`
	ValidTo                  *time.Time `json:"valid_to"`
	ProductIDs               []string   `json:"product_ids,omitempty"` // for specific_products scope
}

// Order is a placed order with everything snapshotted.
type Order struct {
	ID             string               `json:"id"`
	OrderNumber    string               `json:"order_number"`
	CustomerID     string               `json:"customer_id"`
	Status         OrderStatus          `json:"status"`
	Subtotal       float64              `json:"subtotal"`
	DiscountAmount float64              `json:"discount_amount"`
	CouponCode     *string              `json:"coupon_code"`
	Total          float64              `json:"total"`
	PaymentMethod  string               `json:"payment_method"`
	ShipName       string               `json:"ship_name"`
	ShipPhone      string               `json:"ship_phone"`
	ShipLine1      string               `json:"ship_line1"`
	ShipLine2      *string              `json:"ship_line2"`
	ShipCity       string               `json:"ship_city"`
	ShipState      string               `json:"ship_state"`
	ShipPincode    string               `json:"ship_pincode"`
	PlacedAt       time.Time            `json:"placed_at"`
	DealerID       *string              `json:"dealer_id"`
	DealerName     *string              `json:"dealer_name"`
	Items          []OrderItem          `json:"items,omitempty"`
	History        []OrderStatusHistory `json:"history,omitempty"`
}

// Area maps a delivery pincode (in a city) to a fulfilling dealer.
type Area struct {
	ID         string    `json:"id"`
	Code       string    `json:"code"`
	Name       string    `json:"name"`
	City       string    `json:"city"`
	Pincode    string    `json:"pincode"`
	DealerID   *string   `json:"dealer_id"`
	DealerName *string   `json:"dealer_name"`
	IsActive   bool      `json:"is_active"`
	CreatedAt  time.Time `json:"created_at"`
}

// Dealer is a local partner who fulfils orders assigned to them.
type Dealer struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Mobile    string    `json:"mobile"`
	Address   *string   `json:"address"`
	Username  string    `json:"username"`
	IsActive  bool      `json:"is_active"`
	CreatedAt time.Time `json:"created_at"`
}

// Banner is a homepage promo image that optionally links somewhere on click.
type Banner struct {
	ID        string    `json:"id"`
	Title     *string   `json:"title"`
	ImageURL  string    `json:"image_url"`
	LinkURL   *string   `json:"link_url"`
	SortOrder int       `json:"sort_order"`
	IsActive  bool      `json:"is_active"`
	CreatedAt time.Time `json:"created_at"`
}

// OrderStatusHistory is one row of an order's status audit trail.
type OrderStatusHistory struct {
	ID        string      `json:"id"`
	Status    OrderStatus `json:"status"`
	Note      *string     `json:"note"`
	CreatedAt time.Time   `json:"created_at"`
}

// OrderItem is a snapshotted line in an order.
type OrderItem struct {
	ID           string  `json:"id"`
	VariantID    *string `json:"variant_id"`
	ProductName  string  `json:"product_name"`
	VariantLabel string  `json:"variant_label"`
	Unit         Unit    `json:"unit"`
	UnitValue    float64 `json:"unit_value"`
	Price        float64 `json:"price"`
	Qty          int     `json:"qty"`
	LineTotal    float64 `json:"line_total"`
}
