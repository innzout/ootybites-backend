-- Bound discount_value at the database.
--
-- The admin handler already rejects anything outside 0 < value <= 100, but the
-- column itself accepted any numeric. A row written by a migration, a fixture
-- or a direct SQL edit could therefore discount more than the cart is worth and
-- produce a negative order total, which is exactly the class of error the
-- "server is the source of truth for money" rule exists to prevent.
--
-- NOT VALID so an existing out-of-range row cannot block the deploy; the
-- constraint still applies to every insert and update from here on.
alter table coupons
  add constraint coupons_discount_value_pct
  check (discount_value > 0 and discount_value <= 100) not valid;
