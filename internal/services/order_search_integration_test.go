package services_test

import (
	"context"
	"testing"

	"github.com/innzout/ootybites/internal/services"
)

// Admin order search: by number, customer name or phone, with date bounds that
// work on their own.
func TestIntegration_AdminOrderSearch(t *testing.T) {
	pool := testPool(t)
	f := seed(t, pool)
	ctx := context.Background()
	admin := services.NewAdminOrders(pool)

	var number string
	if err := pool.QueryRow(ctx, `SELECT order_number FROM orders WHERE id=$1`, f.buyerOrder).Scan(&number); err != nil {
		t.Fatalf("read order number: %v", err)
	}
	ids := func(fl services.OrderFilter) map[string]bool {
		t.Helper()
		fl.Limit = 100
		out, _, err := admin.List(ctx, fl)
		if err != nil {
			t.Fatalf("List(%+v): %v", fl, err)
		}
		m := map[string]bool{}
		for _, o := range out {
			m[o.ID] = true
		}
		return m
	}

	if !ids(services.OrderFilter{Q: number})[f.buyerOrder] {
		t.Errorf("search by order number %q did not find it", number)
	}
	// The buyer order ships to "Buyer"; "NonBuyer" must not match it.
	if got := ids(services.OrderFilter{Q: "nonbuyer"}); got[f.buyerOrder] {
		t.Error(`"nonbuyer" matched the "Buyer" order`)
	}
	if !ids(services.OrderFilter{Q: "BUYER"})[f.buyerOrder] {
		t.Error("name search should be case-insensitive")
	}
	if !ids(services.OrderFilter{Q: "9000000001"})[f.buyerOrder] {
		t.Error("search by phone did not find the order")
	}
	// Wildcards are text, not patterns: "%" alone must not match everything.
	if got := ids(services.OrderFilter{Q: "%"}); len(got) != 0 {
		t.Errorf(`search "%%" matched %d orders; wildcards must be escaped`, len(got))
	}
	if got := ids(services.OrderFilter{Q: "_"}); len(got) != 0 {
		t.Errorf(`search "_" matched %d orders; wildcards must be escaped`, len(got))
	}
	// One-sided date bounds apply on their own.
	if got := ids(services.OrderFilter{From: "2999-01-01"}); got[f.buyerOrder] {
		t.Error("a from-date in the future still returned today's order: one-sided bound ignored")
	}
	if got := ids(services.OrderFilter{To: "2000-01-01"}); got[f.buyerOrder] {
		t.Error("a to-date in the past still returned today's order: one-sided bound ignored")
	}
}

// The customer and dealer order lists reuse List with an ownership scope. A
// search must narrow within that scope, never reach outside it.
func TestIntegration_ScopedOrderListNeverCrossesOwners(t *testing.T) {
	pool := testPool(t)
	f := seed(t, pool)
	ctx := context.Background()
	admin := services.NewAdminOrders(pool)

	list := func(fl services.OrderFilter) []string {
		t.Helper()
		fl.Limit = 100
		out, _, err := admin.List(ctx, fl)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		var owners []string
		for _, o := range out {
			owners = append(owners, o.CustomerID)
		}
		return owners
	}
	for _, q := range []string{"", "Buyer", "NonBuyer", "9000000002", "OB-"} {
		for _, owner := range list(services.OrderFilter{CustomerID: f.buyerID, Q: q}) {
			if owner != f.buyerID {
				t.Fatalf("customer-scoped list with q=%q returned another customer's order", q)
			}
		}
	}
	if got := list(services.OrderFilter{CustomerID: f.buyerID}); len(got) == 0 {
		t.Error("customer-scoped list returned none of the customer's own orders")
	}
	// A dealer with no assigned orders sees nothing, whatever the search.
	if got := list(services.OrderFilter{DealerID: "00000000-0000-0000-0000-000000000000", Q: "OB-"}); len(got) != 0 {
		t.Errorf("dealer with no orders saw %d", len(got))
	}
}
