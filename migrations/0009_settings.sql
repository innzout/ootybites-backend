-- =====================================================================
-- Store settings — a single-row table holding admin-editable store config
-- (profile, contact, delivery copy, order-number prefix). The id check keeps
-- it to exactly one row; a default row is seeded so reads never miss.
-- =====================================================================
create table if not exists settings (
  id                     smallint primary key default 1 check (id = 1),
  store_name             text not null default 'Ootybites',
  tagline                text not null default 'Taste of the Hills',
  support_email          text not null default '',
  support_phone          text not null default '',
  store_address          text not null default 'Ooty, Tamil Nadu',
  standard_delivery_text text not null default 'Arrives in 3–5 days',
  express_delivery_text  text not null default 'Delivered within 24 hours',
  cod_note               text not null default 'Cash on delivery across India',
  order_number_prefix    text not null default 'OB',
  updated_at             timestamptz not null default now()
);

insert into settings (id) values (1) on conflict (id) do nothing;
