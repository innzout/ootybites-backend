-- =====================================================================
-- Geolocation pin: the customer's exact delivery location (from the map
-- picker) captured on their saved addresses and snapshotted onto orders so
-- the admin/dealer can view the pin for fulfilment.
-- =====================================================================
alter table addresses
  add column if not exists lat double precision,
  add column if not exists lng double precision;

alter table orders
  add column if not exists ship_lat double precision,
  add column if not exists ship_lng double precision;
