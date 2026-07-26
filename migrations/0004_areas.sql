-- =====================================================================
-- Ootybites — 0004_areas.sql
-- Delivery areas map a pincode (within a city) to a fulfilling dealer, so
-- orders can be auto-assigned to the right dealer at placement time.
-- (Chennai-only for now; the city column keeps it extensible.)
-- =====================================================================

create table areas (
  id         uuid primary key default gen_random_uuid(),
  code       text unique not null,                 -- e.g. 'SIR' (Siruseri)
  name       text not null,                        -- e.g. 'Siruseri'
  city       text not null default 'Chennai',
  pincode    text not null,                        -- delivery pincode this area covers
  dealer_id  uuid references dealers(id) on delete set null,
  is_active  boolean not null default true,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);
create trigger trg_areas_updated before update on areas
  for each row execute function set_updated_at();

-- Auto-assign lookup is by pincode.
create index idx_areas_pincode on areas(pincode);
