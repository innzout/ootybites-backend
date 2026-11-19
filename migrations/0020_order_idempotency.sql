-- Idempotent order placement.
--
-- Placing an order is the one request a customer is most likely to retry: the
-- browser times out or the connection drops after the server has already
-- committed, and pressing "Place order" again created a second order - double
-- the stock taken and a second cash-on-delivery parcel dispatched.
--
-- The checkout now sends an Idempotency-Key per purchase attempt. A repeat of
-- the same key from the same customer returns the order already created
-- instead of making another. Nullable because orders placed before this, and
-- any client that omits the header, have none.
alter table orders add column if not exists idempotency_key text;

-- Scoped per customer: keys are client-generated, so two customers' keys must
-- never collide into each other's orders.
create unique index if not exists idx_orders_customer_idempotency
  on orders (customer_id, idempotency_key)
  where idempotency_key is not null;
