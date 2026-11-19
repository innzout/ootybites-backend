package services_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/innzout/ootybites/internal/db"
	"github.com/innzout/ootybites/internal/migrate"
	"github.com/innzout/ootybites/internal/services"
	migrations "github.com/innzout/ootybites/migrations"
)

// Integration tests for the review permission rules.
//
// These are the rules that decide who may write and delete a review, which is
// exactly the code that should not be trusted without tests: "can a customer
// review something they never bought?" and "can a customer delete someone
// else's review by passing its id?" are the questions worth answering
// automatically rather than by hand each time the query is touched.
//
// They need a real Postgres because the whole rule lives in SQL - the
// orders -> order_items -> product_variants join, and the ownership predicate
// inside the DELETE. A mock would test nothing.
//
// Run them with a throwaway database:
//
//	TEST_DATABASE_URL='postgres://postgres:PASS@127.0.0.1:5432/ootybites_test?sslmode=disable' go test ./internal/services/ -run Integration
//
// Without TEST_DATABASE_URL they skip, so the default `go test ./...` stays
// green on a machine with no database.

func testPool(t *testing.T) *db.Pool {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set — skipping database integration test")
	}
	ctx := context.Background()
	pool, err := db.New(ctx, url, 4)
	if err != nil {
		t.Fatalf("connect to test database: %v", err)
	}
	if err := migrate.Run(ctx, pool, migrations.Files); err != nil {
		pool.Close()
		t.Fatalf("run migrations: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// fixture is one self-contained scenario: a product with a variant, a customer
// who has had it delivered, and a customer who has not.
type fixture struct {
	productID  string
	variantID  string
	buyerID    string // has a DELIVERED order containing the product
	nonBuyerID string // has only a PLACED order
	buyerOrder string
}

// seed inserts the scenario under ids unique to this test run, and removes it
// afterwards, so tests can run against a database that already has data in it
// and can run repeatedly without colliding.
func seed(t *testing.T, pool *db.Pool) fixture {
	t.Helper()
	ctx := context.Background()
	suffix := time.Now().UnixNano()

	var f fixture
	must := func(label string, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("seed %s: %v", label, err)
		}
	}

	must("customer buyer", pool.QueryRow(ctx,
		`INSERT INTO customers (phone, name) VALUES ($1,'Buyer') RETURNING id`,
		fmt.Sprintf("9%09d", suffix%1e9)).Scan(&f.buyerID))
	must("customer non-buyer", pool.QueryRow(ctx,
		`INSERT INTO customers (phone, name) VALUES ($1,'NonBuyer') RETURNING id`,
		fmt.Sprintf("8%09d", suffix%1e9)).Scan(&f.nonBuyerID))

	must("product", pool.QueryRow(ctx,
		`INSERT INTO products (name, slug, is_active) VALUES ('Test Varki', $1, true) RETURNING id`,
		fmt.Sprintf("test-varki-%d", suffix)).Scan(&f.productID))
	must("variant", pool.QueryRow(ctx,
		`INSERT INTO product_variants (product_id, label, unit, unit_value, mrp, price, stock_qty, is_active)
		 VALUES ($1,'250g','g',250,249,199,10,true) RETURNING id`, f.productID).Scan(&f.variantID))

	// Delivered order for the buyer — this is what earns the right to review.
	must("delivered order", pool.QueryRow(ctx,
		`INSERT INTO orders (order_number, customer_id, status, subtotal, total,
		                     ship_name, ship_phone, ship_line1, ship_city, ship_state, ship_pincode)
		 VALUES ($1,$2,'delivered',199,199,'Buyer','9000000001','L1','Ooty','Tamil Nadu','643001')
		 RETURNING id`, fmt.Sprintf("OB-TEST-%d", suffix), f.buyerID).Scan(&f.buyerOrder))
	_, err := pool.Exec(ctx,
		`INSERT INTO order_items (order_id, variant_id, product_name, variant_label, unit, unit_value, price, qty, line_total)
		 VALUES ($1,$2,'Test Varki','250g','g',250,199,1,199)`, f.buyerOrder, f.variantID)
	must("delivered order item", err)

	// The non-buyer's order exists but has NOT been delivered.
	var placedOrder string
	must("placed order", pool.QueryRow(ctx,
		`INSERT INTO orders (order_number, customer_id, status, subtotal, total,
		                     ship_name, ship_phone, ship_line1, ship_city, ship_state, ship_pincode)
		 VALUES ($1,$2,'placed',199,199,'NonBuyer','9000000002','L1','Ooty','Tamil Nadu','643001')
		 RETURNING id`, fmt.Sprintf("OB-TEST-%d-B", suffix), f.nonBuyerID).Scan(&placedOrder))
	_, err = pool.Exec(ctx,
		`INSERT INTO order_items (order_id, variant_id, product_name, variant_label, unit, unit_value, price, qty, line_total)
		 VALUES ($1,$2,'Test Varki','250g','g',250,199,1,199)`, placedOrder, f.variantID)
	must("placed order item", err)

	t.Cleanup(func() {
		c := context.Background()
		// product cascades to variants and reviews; orders are removed by id.
		// Every order for these customers, not just the two seeded here: tests
		// that place orders through the service add more.
		pool.Exec(c, `DELETE FROM orders WHERE customer_id = ANY($1)`, []string{f.buyerID, f.nonBuyerID})
		pool.Exec(c, `DELETE FROM products WHERE id = $1`, f.productID)
		pool.Exec(c, `DELETE FROM customers WHERE id = ANY($1)`, []string{f.buyerID, f.nonBuyerID})
	})
	return f
}

func TestIntegration_CanReview_RequiresDeliveredOrder(t *testing.T) {
	pool := testPool(t)
	f := seed(t, pool)
	svc := services.NewReviews(pool)
	ctx := context.Background()

	ok, err := svc.CanReview(ctx, f.buyerID, f.productID)
	if err != nil {
		t.Fatalf("CanReview(buyer): %v", err)
	}
	if !ok {
		t.Error("a customer with a delivered order containing the product should be able to review it")
	}

	// The order exists and contains the product — only its status differs.
	ok, err = svc.CanReview(ctx, f.nonBuyerID, f.productID)
	if err != nil {
		t.Fatalf("CanReview(non-buyer): %v", err)
	}
	if ok {
		t.Error("an order that is not delivered must not earn the right to review")
	}
}

func TestIntegration_Upsert_RejectsNonPurchaser(t *testing.T) {
	pool := testPool(t)
	f := seed(t, pool)
	svc := services.NewReviews(pool)
	ctx := context.Background()

	if _, err := svc.Upsert(ctx, f.nonBuyerID, f.productID, 5, "never bought it"); err == nil {
		t.Fatal("expected a review from a non-purchaser to be rejected")
	} else if err != services.ErrNotPurchased {
		t.Errorf("error = %v, want ErrNotPurchased", err)
	}
}

func TestIntegration_Upsert_IsOneReviewPerCustomerPerProduct(t *testing.T) {
	pool := testPool(t)
	f := seed(t, pool)
	svc := services.NewReviews(pool)
	ctx := context.Background()

	if _, err := svc.Upsert(ctx, f.buyerID, f.productID, 4, "good"); err != nil {
		t.Fatalf("first review: %v", err)
	}
	// Reviewing again must edit the existing row, not add a second one.
	if _, err := svc.Upsert(ctx, f.buyerID, f.productID, 5, "even better"); err != nil {
		t.Fatalf("second review: %v", err)
	}

	_, avg, count, err := svc.ListForProduct(ctx, f.productID, 50)
	if err != nil {
		t.Fatalf("ListForProduct: %v", err)
	}
	if count != 1 {
		t.Errorf("review count = %d, want 1 (re-reviewing must update in place)", count)
	}
	if avg != 5 {
		t.Errorf("average = %v, want 5 (the updated rating)", avg)
	}
}

// The security case: a review id is not a capability. Knowing someone else's
// review id must not be enough to delete it.
func TestIntegration_DeleteOwn_CannotDeleteAnotherCustomersReview(t *testing.T) {
	pool := testPool(t)
	f := seed(t, pool)
	svc := services.NewReviews(pool)
	ctx := context.Background()

	review, err := svc.Upsert(ctx, f.buyerID, f.productID, 5, "mine")
	if err != nil {
		t.Fatalf("seed review: %v", err)
	}

	// The non-buyer knows the id and tries to delete it.
	if err := svc.DeleteOwn(ctx, f.nonBuyerID, review.ID); err != services.ErrNotFound {
		t.Errorf("deleting another customer's review: err = %v, want ErrNotFound", err)
	}

	// It must still be there.
	_, _, count, err := svc.ListForProduct(ctx, f.productID, 50)
	if err != nil {
		t.Fatalf("ListForProduct: %v", err)
	}
	if count != 1 {
		t.Fatalf("review count = %d, want 1 — another customer deleted it", count)
	}

	// And the owner can still delete their own.
	if err := svc.DeleteOwn(ctx, f.buyerID, review.ID); err != nil {
		t.Errorf("owner deleting their own review: %v", err)
	}
}

func TestIntegration_MineFor_IsScopedToTheCustomer(t *testing.T) {
	pool := testPool(t)
	f := seed(t, pool)
	svc := services.NewReviews(pool)
	ctx := context.Background()

	if _, err := svc.Upsert(ctx, f.buyerID, f.productID, 3, "ok"); err != nil {
		t.Fatalf("seed review: %v", err)
	}

	mine, err := svc.MineFor(ctx, f.buyerID, f.productID)
	if err != nil {
		t.Fatalf("MineFor(owner): %v", err)
	}
	if mine == nil || mine.Rating != 3 {
		t.Errorf("owner should see their own review, got %+v", mine)
	}

	other, err := svc.MineFor(ctx, f.nonBuyerID, f.productID)
	if err != nil {
		t.Fatalf("MineFor(other): %v", err)
	}
	if other != nil {
		t.Error("MineFor must not return another customer's review")
	}
}
