-- Give hubs a map pin.
--
-- A hub is the point an express order is dispatched from, so it is the origin
-- for the delivery route shown in the admin orders map. Until now a hub had a
-- name and nothing locatable, which meant there was no "our end" to route from.
--
-- Nullable: existing hubs have no pin yet, and the map falls back to the store
-- address (settings.store_address) when a hub has not been pinned.
alter table hubs
  add column if not exists lat double precision,
  add column if not exists lng double precision;
