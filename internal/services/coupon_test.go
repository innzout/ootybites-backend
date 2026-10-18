package services

import (
	"math"
	"testing"
	"time"

	"github.com/innzout/ootybites/internal/models"
)

// EvaluateCoupon decides how much money comes off an order, so it is the one
// function where a silent arithmetic or ordering change is most expensive.
// These tests pin the gate order (ARCHITECTURE section 5), the discount cap and
// the rounding, none of which were covered before.

func ptrF(v float64) *float64     { return &v }
func ptrI(v int) *int             { return &v }
func ptrT(t time.Time) *time.Time { return &t }

var testNow = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

// baseCoupon is a plain, unconditional 10% coupon on everything.
func baseCoupon() *models.Coupon {
	return &models.Coupon{
		Code:            "SAVE10",
		IsActive:        true,
		DiscountType:    "percentage",
		DiscountValue:   10,
		ApplicableScope: "all",
	}
}

func TestEvaluateCoupon_AppliesPercentage(t *testing.T) {
	got := EvaluateCoupon(nil, 1000, baseCoupon(), CustomerStats{}, testNow)
	if !got.Valid {
		t.Fatalf("expected valid, got reason %q", got.Reason)
	}
	if got.Discount != 100 {
		t.Errorf("discount = %v, want 100", got.Discount)
	}
}

func TestEvaluateCoupon_Rejections(t *testing.T) {
	past := testNow.Add(-48 * time.Hour)
	future := testNow.Add(48 * time.Hour)

	cases := []struct {
		name   string
		mutate func(*models.Coupon)
		stats  CustomerStats
		reason string
	}{
		{"inactive", func(c *models.Coupon) { c.IsActive = false }, CustomerStats{}, "This coupon is not active"},
		{"not yet valid", func(c *models.Coupon) { c.ValidFrom = ptrT(future) }, CustomerStats{}, "This coupon is not yet valid"},
		{"expired", func(c *models.Coupon) { c.ValidTo = ptrT(past) }, CustomerStats{}, "This coupon has expired"},
		{"total usage limit reached", func(c *models.Coupon) {
			c.UsageLimitTotal, c.UsedCount = ptrI(5), 5
		}, CustomerStats{}, "This coupon has reached its usage limit"},
		{"per-user limit reached", func(c *models.Coupon) {
			c.UsageLimitPerUser = ptrI(1)
		}, CustomerStats{CouponRedeemed: 1}, "You have already used this coupon"},
		{"lifetime value gate", func(c *models.Coupon) {
			c.MinCustomerLifetimeValue = 5000
		}, CustomerStats{LifetimeValue: 100}, "This coupon is not available for your account yet"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := baseCoupon()
			tc.mutate(c)
			got := EvaluateCoupon(nil, 1000, c, tc.stats, testNow)
			if got.Valid {
				t.Fatalf("expected rejection, got discount %v", got.Discount)
			}
			if got.Reason != tc.reason {
				t.Errorf("reason = %q, want %q", got.Reason, tc.reason)
			}
			if got.Discount != 0 {
				t.Errorf("rejected coupon returned discount %v, want 0", got.Discount)
			}
		})
	}
}

// The gates compare with >= and <=, so "exactly at the limit" must resolve the
// way the customer-facing copy implies rather than being off by one.
func TestEvaluateCoupon_Boundaries(t *testing.T) {
	t.Run("subtotal exactly meets minimum", func(t *testing.T) {
		c := baseCoupon()
		c.MinOrderValue = 1000
		if got := EvaluateCoupon(nil, 1000, c, CustomerStats{}, testNow); !got.Valid {
			t.Errorf("subtotal == MinOrderValue should pass, got %q", got.Reason)
		}
	})
	t.Run("subtotal one paisa short", func(t *testing.T) {
		c := baseCoupon()
		c.MinOrderValue = 1000
		if got := EvaluateCoupon(nil, 999.99, c, CustomerStats{}, testNow); got.Valid {
			t.Error("subtotal below MinOrderValue should be rejected")
		}
	})
	t.Run("lifetime value exactly meets gate", func(t *testing.T) {
		c := baseCoupon()
		c.MinCustomerLifetimeValue = 5000
		got := EvaluateCoupon(nil, 1000, c, CustomerStats{LifetimeValue: 5000}, testNow)
		if !got.Valid {
			t.Errorf("LTV == gate should pass, got %q", got.Reason)
		}
	})
	t.Run("usage one below total limit", func(t *testing.T) {
		c := baseCoupon()
		c.UsageLimitTotal, c.UsedCount = ptrI(5), 4
		if got := EvaluateCoupon(nil, 1000, c, CustomerStats{}, testNow); !got.Valid {
			t.Errorf("used 4 of 5 should pass, got %q", got.Reason)
		}
	})
}

func TestEvaluateCoupon_MaxDiscountCapAlwaysApplies(t *testing.T) {
	c := baseCoupon()
	c.DiscountValue = 50
	c.MaxDiscountCap = ptrF(200)
	// 50% of 1000 is 500, but the cap is 200.
	got := EvaluateCoupon(nil, 1000, c, CustomerStats{}, testNow)
	if !got.Valid {
		t.Fatalf("expected valid, got %q", got.Reason)
	}
	if got.Discount != 200 {
		t.Errorf("discount = %v, want the cap 200", got.Discount)
	}
}

func TestEvaluateCoupon_SpecificProductsScope(t *testing.T) {
	lines := []CartLine{
		{ProductID: "tea", LineTotal: 600},
		{ProductID: "honey", LineTotal: 400},
	}

	t.Run("discounts only applicable items", func(t *testing.T) {
		c := baseCoupon()
		c.ApplicableScope = "specific_products"
		c.ProductIDs = []string{"tea"}
		// 10% of the tea line (600) = 60, NOT 10% of the 1000 subtotal.
		got := EvaluateCoupon(lines, 1000, c, CustomerStats{}, testNow)
		if !got.Valid {
			t.Fatalf("expected valid, got %q", got.Reason)
		}
		if got.Discount != 60 {
			t.Errorf("discount = %v, want 60 (applicable line only)", got.Discount)
		}
	})

	t.Run("rejects when no cart item matches", func(t *testing.T) {
		c := baseCoupon()
		c.ApplicableScope = "specific_products"
		c.ProductIDs = []string{"varki"}
		got := EvaluateCoupon(lines, 1000, c, CustomerStats{}, testNow)
		if got.Valid {
			t.Fatalf("expected rejection, got discount %v", got.Discount)
		}
		if got.Reason != "Coupon not applicable to items in your cart" {
			t.Errorf("reason = %q", got.Reason)
		}
	})

	t.Run("minimum order value still measures the whole cart", func(t *testing.T) {
		// The gate is deliberately on the full subtotal, not the applicable
		// portion: a 1000-rupee minimum is met by a 1000-rupee cart even when
		// the coupon only discounts 600 of it.
		c := baseCoupon()
		c.ApplicableScope = "specific_products"
		c.ProductIDs = []string{"tea"}
		c.MinOrderValue = 1000
		if got := EvaluateCoupon(lines, 1000, c, CustomerStats{}, testNow); !got.Valid {
			t.Errorf("expected valid, got %q", got.Reason)
		}
	})
}

func TestEvaluateCoupon_RoundsToPaise(t *testing.T) {
	c := baseCoupon()
	c.DiscountValue = 33.33
	// 33.33% of 100 = 33.33, which float64 cannot represent exactly.
	got := EvaluateCoupon(nil, 100, c, CustomerStats{}, testNow)
	if !got.Valid {
		t.Fatalf("expected valid, got %q", got.Reason)
	}
	if math.Abs(got.Discount-33.33) > 1e-9 {
		t.Errorf("discount = %v, want 33.33 rounded to paise", got.Discount)
	}
}

// An out-of-range percentage must never make the order total negative. The
// admin handler bounds discount_value to 0-100, so this guards rows written
// around it: direct SQL, fixtures, or data predating that check.
func TestEvaluateCoupon_NeverExceedsApplicableTotal(t *testing.T) {
	c := baseCoupon()
	c.DiscountValue = 500 // an admin typing 500 instead of 50
	got := EvaluateCoupon(nil, 1000, c, CustomerStats{}, testNow)
	if got.Discount > 1000 {
		t.Errorf("discount = %v exceeds the 1000 subtotal; the order total would go negative", got.Discount)
	}
}

// The gates must fail fast in declared order, so the customer sees the most
// relevant reason rather than whichever check happens to run first.
func TestEvaluateCoupon_GateOrder(t *testing.T) {
	c := baseCoupon()
	c.IsActive = false
	c.ValidTo = ptrT(testNow.Add(-time.Hour))
	c.MinOrderValue = 99999
	// Inactive is checked first, so it wins over expiry and the minimum.
	if got := EvaluateCoupon(nil, 1, c, CustomerStats{}, testNow); got.Reason != "This coupon is not active" {
		t.Errorf("reason = %q, want the inactive reason to take precedence", got.Reason)
	}
}
