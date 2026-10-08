-- =====================================================================
-- Admin users: roles + display name.
-- Two roles for v1:
--   super_admin — full access, including managing admin users
--   manager     — full operational access, but cannot manage admin users
-- Existing admins default to super_admin so the bootstrap admin keeps
-- full control after this migration.
-- =====================================================================
alter table admins
  add column if not exists role text not null default 'super_admin'
    check (role in ('super_admin', 'manager')),
  add column if not exists name text;
