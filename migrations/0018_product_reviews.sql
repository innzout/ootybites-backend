-- Product reviews and ratings.
--
-- Visibility: signed-in customers and admins. Not public - anonymous visitors
-- and crawlers never see these, which is why the storefront deliberately emits
-- no AggregateRating structured data for them.
--
-- Writing is earned, not open: a customer may review a product only if they
-- have a *delivered* order containing it (enforced in the service, which is the
-- only place that can see the orders -> order_items -> product_variants chain).
create table product_reviews (
  id          uuid primary key default gen_random_uuid(),
  product_id  uuid not null references products(id) on delete cascade,
  customer_id uuid not null references customers(id) on delete cascade,
  -- The delivered order that earned the right to review. Kept for support and
  -- moderation ("which purchase was this?"). Nulled rather than cascading if
  -- the order is ever removed, so the review itself survives.
  order_id    uuid references orders(id) on delete set null,
  rating      int  not null check (rating between 1 and 5),
  body        text not null default '',
  created_at  timestamptz not null default now(),
  updated_at  timestamptz not null default now(),
  -- One review per customer per product; re-reviewing updates it in place.
  unique (product_id, customer_id)
);

-- The product page reads reviews newest-first for one product.
create index idx_product_reviews_product on product_reviews(product_id, created_at desc);
-- "My reviews" and the eligibility lookup read by customer.
create index idx_product_reviews_customer on product_reviews(customer_id, created_at desc);
