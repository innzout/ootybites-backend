package services_test

import (
	"context"
	"sync"
	"testing"

	"github.com/innzout/ootybites/internal/db"
	"github.com/innzout/ootybites/internal/services"
)

// Idempotent order placement. The failure this prevents is concrete: the
// connection drops after the server commits, the customer presses "Place
// order" again, and a second cash-on-delivery parcel is packed and dispatched.

func shipTo() services.ShippingInput {
	return services.ShippingInput{
		Name: "Buyer", Phone: "9000000001", Line1: "1 Hill Road",
		City: "Ooty", State: "Tamil Nadu", Pincode: "643001",
	}
}

func stockOf(t *testing.T, pool *db.Pool, f fixture) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT stock_qty FROM product_variants WHERE id=$1`, f.variantID).Scan(&n); err != nil {
		t.Fatalf("read stock: %v", err)
	}
	return n
}

func countOrders(t *testing.T, pool *db.Pool, f fixture) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM orders WHERE customer_id=$1`, f.buyerID).Scan(&n); err != nil {
		t.Fatalf("count orders: %v", err)
	}
	return n
}

func TestIntegration_PlaceOrder_RetryWithSameKeyReturnsTheSameOrder(t *testing.T) {
	pool := testPool(t)
	f := seed(t, pool)
	svc := services.NewOrders(pool, services.NewCoupons(pool))
	ctx := context.Background()
	items := []services.CartItemInput{{VariantID: f.variantID, Qty: 2}}

	before, ordersBefore := stockOf(t, pool, f), countOrders(t, pool, f)

	first, err := svc.PlaceOrder(ctx, f.buyerID, items, "", shipTo(), "", "retry-key-1")
	if err != nil {
		t.Fatalf("first attempt: %v", err)
	}
	// The retry: same key, as if the first response never arrived.
	second, err := svc.PlaceOrder(ctx, f.buyerID, items, "", shipTo(), "", "retry-key-1")
	if err != nil {
		t.Fatalf("retry: %v", err)
	}

	if first.ID != second.ID {
		t.Errorf("retry created a second order (%s then %s)", first.OrderNumber, second.OrderNumber)
	}
	if got := countOrders(t, pool, f) - ordersBefore; got != 1 {
		t.Errorf("orders created = %d, want 1", got)
	}
	if got := before - stockOf(t, pool, f); got != 2 {
		t.Errorf("stock taken = %d, want 2 (taken once, not twice)", got)
	}
}

func TestIntegration_PlaceOrder_DifferentKeysAreDifferentOrders(t *testing.T) {
	pool := testPool(t)
	f := seed(t, pool)
	svc := services.NewOrders(pool, services.NewCoupons(pool))
	ctx := context.Background()
	items := []services.CartItemInput{{VariantID: f.variantID, Qty: 1}}

	a, err := svc.PlaceOrder(ctx, f.buyerID, items, "", shipTo(), "", "key-a")
	if err != nil {
		t.Fatalf("order a: %v", err)
	}
	b, err := svc.PlaceOrder(ctx, f.buyerID, items, "", shipTo(), "", "key-b")
	if err != nil {
		t.Fatalf("order b: %v", err)
	}
	if a.ID == b.ID {
		t.Error("two genuinely separate purchases were collapsed into one order")
	}
}

// A double tap sends both requests at once. They must still produce one order.
func TestIntegration_PlaceOrder_ConcurrentDuplicatesProduceOneOrder(t *testing.T) {
	pool := testPool(t)
	f := seed(t, pool)
	svc := services.NewOrders(pool, services.NewCoupons(pool))
	ctx := context.Background()
	items := []services.CartItemInput{{VariantID: f.variantID, Qty: 1}}
	ordersBefore := countOrders(t, pool, f)

	const n = 5
	ids := make([]string, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			o, err := svc.PlaceOrder(ctx, f.buyerID, items, "", shipTo(), "", "double-tap")
			errs[i] = err
			if o != nil {
				ids[i] = o.ID
			}
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("request %d failed: %v", i, err)
		}
		if ids[i] != ids[0] {
			t.Errorf("request %d got a different order", i)
		}
	}
	if got := countOrders(t, pool, f) - ordersBefore; got != 1 {
		t.Errorf("orders created by %d simultaneous duplicates = %d, want 1", n, got)
	}
}

// Keys are client-generated, so two customers may send the same one. That must
// never hand one customer the other's order.
func TestIntegration_PlaceOrder_KeysAreScopedToTheCustomer(t *testing.T) {
	pool := testPool(t)
	f := seed(t, pool)
	svc := services.NewOrders(pool, services.NewCoupons(pool))
	ctx := context.Background()
	items := []services.CartItemInput{{VariantID: f.variantID, Qty: 1}}

	mine, err := svc.PlaceOrder(ctx, f.buyerID, items, "", shipTo(), "", "shared-key")
	if err != nil {
		t.Fatalf("buyer order: %v", err)
	}
	theirs, err := svc.PlaceOrder(ctx, f.nonBuyerID, items, "", shipTo(), "", "shared-key")
	if err != nil {
		t.Fatalf("other customer order: %v", err)
	}
	if theirs.ID == mine.ID {
		t.Fatal("a second customer reusing a key was handed the first customer's order")
	}
	if theirs.CustomerID != f.nonBuyerID {
		t.Errorf("order belongs to %s, want %s", theirs.CustomerID, f.nonBuyerID)
	}
}

// The first attempt bought the last of the stock. A retry must still return
// that order, not fail with "out of stock" against its own purchase - which
// would tell the customer their order failed when it had in fact gone through.
func TestIntegration_PlaceOrder_RetryAfterTakingTheLastStock(t *testing.T) {
	pool := testPool(t)
	f := seed(t, pool)
	svc := services.NewOrders(pool, services.NewCoupons(pool))
	ctx := context.Background()
	all := stockOf(t, pool, f)
	items := []services.CartItemInput{{VariantID: f.variantID, Qty: all}}

	first, err := svc.PlaceOrder(ctx, f.buyerID, items, "", shipTo(), "", "last-units")
	if err != nil {
		t.Fatalf("first attempt: %v", err)
	}
	if left := stockOf(t, pool, f); left != 0 {
		t.Fatalf("stock left = %d, want 0", left)
	}
	second, err := svc.PlaceOrder(ctx, f.buyerID, items, "", shipTo(), "", "last-units")
	if err != nil {
		t.Fatalf("retry reported a failure for an order that succeeded: %v", err)
	}
	if second.ID != first.ID {
		t.Error("retry did not return the original order")
	}
}

// The product is unpublished between the attempt and the retry. Cart
// validation rejects unpublished products, so without the early idempotency
// check the retry would report "no longer available" for an order that had in
// fact been placed.
func TestIntegration_PlaceOrder_RetryAfterProductUnpublished(t *testing.T) {
	pool := testPool(t)
	f := seed(t, pool)
	svc := services.NewOrders(pool, services.NewCoupons(pool))
	ctx := context.Background()
	items := []services.CartItemInput{{VariantID: f.variantID, Qty: 1}}

	first, err := svc.PlaceOrder(ctx, f.buyerID, items, "", shipTo(), "", "then-unpublished")
	if err != nil {
		t.Fatalf("first attempt: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE products SET is_active=false WHERE id=$1`, f.productID); err != nil {
		t.Fatalf("unpublish: %v", err)
	}
	second, err := svc.PlaceOrder(ctx, f.buyerID, items, "", shipTo(), "", "then-unpublished")
	if err != nil {
		t.Fatalf("retry reported a failure for an order that succeeded: %v", err)
	}
	if second.ID != first.ID {
		t.Error("retry did not return the original order")
	}
}
