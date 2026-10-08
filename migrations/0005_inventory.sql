-- =====================================================================
-- Ootybites — 0005_inventory.sql
-- Inventory / procurement.
--   vendors          : suppliers in Ooty we BUY stock from.
--   stock_movements  : the ledger — every change to a variant's stock
--                      (purchase in, sale out, cancel restore, manual adjust).
-- product_variants.stock_qty stays the authoritative running total; each
-- change is mirrored here for a full, auditable history.
-- =====================================================================

create table vendors (
  id         uuid primary key default gen_random_uuid(),
  name       text not null,
  phone      text,
  location   text not null default 'Ooty',
  notes      text,
  is_active  boolean not null default true,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);
create trigger trg_vendors_updated before update on vendors
  for each row execute function set_updated_at();

create table stock_movements (
  id         uuid primary key default gen_random_uuid(),
  variant_id uuid not null references product_variants(id) on delete cascade,
  delta      int  not null,                       -- + stock in, - stock out
  reason     text not null,                       -- purchase | sale | cancel_restore | adjustment
  vendor_id  uuid references vendors(id),         -- set for purchases
  order_id   uuid references orders(id) on delete set null, -- set for sales/cancels
  unit_cost  numeric(10,2),                        -- purchase cost per unit
  note       text,
  created_at timestamptz not null default now()
);
create index idx_stock_movements_variant on stock_movements(variant_id, created_at desc);
create index idx_stock_movements_vendor  on stock_movements(vendor_id);
