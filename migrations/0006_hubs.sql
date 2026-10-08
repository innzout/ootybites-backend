-- =====================================================================
-- Ootybites — 0006_hubs.sql
-- Hubs & per-hub stock (initial phase — Chennai).
--   hub        : a fulfilment centre run by a dealer; serves many areas.
--   area→hub   : each area belongs to exactly one hub (exclusive).
--   hub_stock  : stock held at a hub; if the serving hub has stock, the
--                customer gets 24-hour (express) delivery.
-- Central product_variants.stock_qty remains the default warehouse stock
-- used for standard delivery when a hub can't fulfil.
-- =====================================================================

create table hubs (
  id         uuid primary key default gen_random_uuid(),
  name       text not null,
  dealer_id  uuid references dealers(id) on delete set null,
  is_active  boolean not null default true,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);
create trigger trg_hubs_updated before update on hubs
  for each row execute function set_updated_at();

-- Areas now map to a hub (exclusive) instead of directly to a dealer.
alter table areas drop column if exists dealer_id;
alter table areas add column hub_id uuid references hubs(id) on delete set null;
create index idx_areas_hub on areas(hub_id);

-- Per-hub stock (one row per hub × variant).
create table hub_stock (
  id         uuid primary key default gen_random_uuid(),
  hub_id     uuid not null references hubs(id) on delete cascade,
  variant_id uuid not null references product_variants(id) on delete cascade,
  stock_qty  int  not null default 0,
  unique (hub_id, variant_id)
);
create index idx_hub_stock_variant on hub_stock(variant_id);

-- Ledger + orders learn about hubs.
alter table stock_movements add column hub_id uuid references hubs(id);
alter table orders add column hub_id uuid references hubs(id);
alter table orders add column is_express boolean not null default false;
