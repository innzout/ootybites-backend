-- Customer-provided delivery instructions captured at checkout and snapshotted
-- on the order (distinct from admin/internal order notes).
alter table orders add column if not exists delivery_note text;
