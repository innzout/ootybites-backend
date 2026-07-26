-- =====================================================================
-- Ootybites — 0001_init.sql
-- Initial schema for Supabase Postgres
-- Run order: this is the first migration.
-- =====================================================================

-- ---------- Extensions ----------
create extension if not exists "pgcrypto";   -- for gen_random_uuid()

-- ---------- Enums ----------
create type unit_type     as enum ('mg', 'g', 'kg', 'ml', 'l', 'nos', 'packets');
create type order_status  as enum ('placed', 'reached_dealer', 'delivered', 'cancelled');
create type discount_type as enum ('percentage');            -- 'flat' can be added later
create type coupon_scope  as enum ('all', 'specific_products');

-- ---------- updated_at trigger helper ----------
create or replace function set_updated_at()
returns trigger as $$
begin
  new.updated_at = now();
  return new;
end;
$$ language plpgsql;

-- =====================================================================
-- Customers  (phone-OTP identity)
-- =====================================================================
create table customers (
  id         uuid primary key default gen_random_uuid(),
  phone      text unique not null,
  name       text,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);
create trigger trg_customers_updated before update on customers
  for each row execute function set_updated_at();

-- Saved delivery addresses (Amazon/Flipkart style; order snapshots its own copy)
create table addresses (
  id          uuid primary key default gen_random_uuid(),
  customer_id uuid not null references customers(id) on delete cascade,
  name        text not null,
  phone       text not null,
  line1       text not null,
  line2       text,
  city        text not null,
  state       text not null,
  pincode     text not null,
  is_default  boolean not null default false,
  created_at  timestamptz not null default now()
);
create index idx_addresses_customer on addresses(customer_id);

-- =====================================================================
-- Admins  (username + password)
-- =====================================================================
create table admins (
  id            uuid primary key default gen_random_uuid(),
  username      text unique not null,
  password_hash text not null,          -- argon2id / bcrypt
  created_at    timestamptz not null default now()
);

-- =====================================================================
-- Catalog: products -> variants + images
-- Variants carry the real sellable unit, price and stock.
-- =====================================================================
create table products (
  id          uuid primary key default gen_random_uuid(),
  name        text not null,
  slug        text unique not null,
  description text,
  is_active   boolean not null default true,
  created_at  timestamptz not null default now(),
  updated_at  timestamptz not null default now()
);
create trigger trg_products_updated before update on products
  for each row execute function set_updated_at();
create index idx_products_active on products(is_active);

create table product_images (
  id                   uuid primary key default gen_random_uuid(),
  product_id           uuid not null references products(id) on delete cascade,
  cloudinary_public_id text not null,
  url                  text not null,
  sort_order           int  not null default 0,
  is_primary           boolean not null default false,
  created_at           timestamptz not null default now()
);
create index idx_product_images_product on product_images(product_id);

create table product_variants (
  id         uuid primary key default gen_random_uuid(),
  product_id uuid not null references products(id) on delete cascade,
  label      text not null,                 -- "500 g", "Pack of 15 nos", "6 packets"
  unit       unit_type not null,
  unit_value numeric(10,2) not null,        -- 500, 15, 6 ...
  mrp        numeric(10,2) not null,
  price      numeric(10,2) not null,
  stock_qty  int not null default 0,
  sku        text unique,
  is_active  boolean not null default true,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  constraint chk_price_positive check (price >= 0),
  constraint chk_stock_nonneg   check (stock_qty >= 0)
);
create trigger trg_variants_updated before update on product_variants
  for each row execute function set_updated_at();
create index idx_variants_product on product_variants(product_id);

-- =====================================================================
-- Coupons
-- =====================================================================
create table coupons (
  id                          uuid primary key default gen_random_uuid(),
  code                        text unique not null,
  is_active                   boolean not null default false,   -- "shows when enabled"
  discount_type               discount_type not null default 'percentage',
  discount_value              numeric(10,2) not null,           -- 10 = 10%
  max_discount_cap            numeric(10,2),                    -- 10% up to this amount
  applicable_scope            coupon_scope not null default 'all',
  min_order_value             numeric(10,2) not null default 0,
  min_customer_lifetime_value numeric(12,2) not null default 0, -- unlock by account history
  usage_limit_total           int,
  usage_limit_per_user        int,
  used_count                  int not null default 0,
  valid_from                  timestamptz,
  valid_to                    timestamptz,
  created_at                  timestamptz not null default now()
);
create index idx_coupons_active on coupons(is_active);

-- Which products a 'specific_products' coupon applies to
create table coupon_products (
  coupon_id  uuid not null references coupons(id) on delete cascade,
  product_id uuid not null references products(id) on delete cascade,
  primary key (coupon_id, product_id)
);

-- =====================================================================
-- Orders  (snapshots everything at placement time)
-- =====================================================================
create table orders (
  id             uuid primary key default gen_random_uuid(),
  order_number   text unique not null,               -- e.g. OB-20260721-0001
  customer_id    uuid not null references customers(id),
  status         order_status not null default 'placed',
  subtotal       numeric(12,2) not null,
  discount_amount numeric(12,2) not null default 0,
  coupon_id      uuid references coupons(id),
  coupon_code    text,
  total          numeric(12,2) not null,
  payment_method text not null default 'cod',
  -- shipping snapshot (frozen at order time)
  ship_name      text not null,
  ship_phone     text not null,
  ship_line1     text not null,
  ship_line2     text,
  ship_city      text not null,
  ship_state     text not null,
  ship_pincode   text not null,
  placed_at      timestamptz not null default now(),
  updated_at     timestamptz not null default now()
);
create trigger trg_orders_updated before update on orders
  for each row execute function set_updated_at();
-- Indexes tuned for the admin list: filter by status + month/range, sort by date
create index idx_orders_status_placed on orders(status, placed_at desc);
create index idx_orders_placed        on orders(placed_at desc);
create index idx_orders_customer      on orders(customer_id);

create table order_items (
  id            uuid primary key default gen_random_uuid(),
  order_id      uuid not null references orders(id) on delete cascade,
  variant_id    uuid references product_variants(id),   -- nullable: keep history if variant deleted
  product_name  text not null,        -- snapshot
  variant_label text not null,        -- snapshot
  unit          unit_type not null,   -- snapshot
  unit_value    numeric(10,2) not null,
  price         numeric(10,2) not null,
  qty           int not null,
  line_total    numeric(12,2) not null
);
create index idx_order_items_order on order_items(order_id);

-- Full audit trail of status changes (who / when / why)
create table order_status_history (
  id         uuid primary key default gen_random_uuid(),
  order_id   uuid not null references orders(id) on delete cascade,
  status     order_status not null,
  changed_by uuid references admins(id),
  note       text,
  created_at timestamptz not null default now()
);
create index idx_status_history_order on order_status_history(order_id, created_at);

-- Tracks per-user coupon usage for usage_limit_per_user
create table coupon_redemptions (
  id          uuid primary key default gen_random_uuid(),
  coupon_id   uuid not null references coupons(id) on delete cascade,
  customer_id uuid not null references customers(id) on delete cascade,
  order_id    uuid not null references orders(id) on delete cascade,
  created_at  timestamptz not null default now()
);
create index idx_redemptions_coupon_customer on coupon_redemptions(coupon_id, customer_id);

-- =====================================================================
-- Notes
-- * OTP codes / rate-limit counters live in Redis, not here.
-- * customer lifetime value for coupon gating = sum(total) of that
--   customer's orders where status = 'delivered'.
-- * on cancel: restore stock_qty for each order_item's variant.
-- =====================================================================
