-- =====================================================================
-- Ootybites — 0003_dealers.sql
-- Dealers: local partners who handle (fulfil) orders assigned to them.
-- They log in with username/password and can update their orders' status.
-- =====================================================================

create table dealers (
  id            uuid primary key default gen_random_uuid(),
  name          text not null,
  mobile        text not null,
  address       text,
  username      text unique not null,
  password_hash text not null,
  is_active     boolean not null default true,
  created_at    timestamptz not null default now(),
  updated_at    timestamptz not null default now()
);
create trigger trg_dealers_updated before update on dealers
  for each row execute function set_updated_at();

-- An order may be assigned to a dealer for fulfilment.
alter table orders add column dealer_id uuid references dealers(id);
create index idx_orders_dealer on orders(dealer_id);
