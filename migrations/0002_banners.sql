-- =====================================================================
-- Ootybites — 0002_banners.sql
-- Homepage promotional banners. Each banner is an image that optionally
-- links somewhere (a product page or any URL) when clicked.
-- =====================================================================

create table banners (
  id         uuid primary key default gen_random_uuid(),
  title      text,                                  -- optional caption / alt text
  image_url  text not null,                         -- Cloudinary/hosted image URL
  link_url   text,                                  -- e.g. '/products/ooty-tea' or external URL
  sort_order int  not null default 0,               -- display order (asc)
  is_active  boolean not null default true,         -- show on the storefront
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);

create trigger trg_banners_updated before update on banners
  for each row execute function set_updated_at();

-- Storefront query: active banners in display order.
create index idx_banners_active_sort on banners(is_active, sort_order);
