-- =====================================================================
-- Fix: order_items.variant_id was created without an ON DELETE clause, so it
-- defaulted to RESTRICT — blocking deletion of any product/variant that had
-- ever been ordered ("Could not delete product"). The intended behaviour
-- (per the column comment) is SET NULL: keep the order line as a historical
-- snapshot (product_name/variant_label/price are already snapshotted) while
-- releasing the reference to the deleted variant.
-- =====================================================================
alter table order_items drop constraint if exists order_items_variant_id_fkey;
alter table order_items
  add constraint order_items_variant_id_fkey
  foreign key (variant_id) references product_variants(id) on delete set null;
