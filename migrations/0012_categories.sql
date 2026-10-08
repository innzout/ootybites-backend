-- =====================================================================
-- Product categories (taxonomy) — lets shoppers browse by collection and
-- powers the storefront category filter. A product belongs to at most one
-- category; deleting a category unassigns its products (SET NULL).
-- =====================================================================
create table if not exists categories (
  id         uuid primary key default gen_random_uuid(),
  name       text not null,
  slug       text unique not null,
  sort_order int not null default 0,
  is_active  boolean not null default true,
  created_at timestamptz not null default now()
);

alter table products add column if not exists category_id uuid references categories(id) on delete set null;
create index if not exists idx_products_category on products(category_id);
