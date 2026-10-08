-- =====================================================================
-- Ootybites — 0007_notifications.sql
-- In-app notifications. A row targets either a customer (customer_id set)
-- or the admin feed (is_admin = true). Emitted on order events.
-- =====================================================================

create table notifications (
  id          uuid primary key default gen_random_uuid(),
  customer_id uuid references customers(id) on delete cascade,  -- null for admin notifications
  is_admin    boolean not null default false,
  title       text not null,
  body        text,
  order_id    uuid references orders(id) on delete set null,
  is_read     boolean not null default false,
  created_at  timestamptz not null default now()
);
create index idx_notifications_customer on notifications(customer_id, created_at desc);
create index idx_notifications_admin    on notifications(is_admin, created_at desc);
