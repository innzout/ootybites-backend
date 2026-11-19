-- Lock the tables against Supabase's public API roles.
--
-- Supabase exposes every table in the public schema through its REST API
-- (PostgREST) to the `anon` and `authenticated` roles, and by default grants
-- them read and write. With row level security off, anyone holding the
-- project's anon key - which Supabase treats as publishable, not secret - could
-- read customers' phone numbers and addresses, or rewrite and delete orders,
-- without going near this API.
--
-- This app never uses that REST API: the Go backend connects directly as the
-- role that owns these tables. Postgres does not apply row level security to a
-- table's owner (absent FORCE ROW LEVEL SECURITY), so the backend is
-- unaffected, while every other role - anon and authenticated included - now
-- matches no policy and sees no rows at all.
--
-- On a plain Postgres (local development) these roles do not exist; enabling
-- RLS is then harmless and the role-specific statements are skipped.
do $$
declare
  t record;
begin
  for t in select tablename from pg_tables where schemaname = 'public' loop
    execute format('alter table public.%I enable row level security', t.tablename);
  end loop;
end $$;

-- Belt and braces on Supabase: also withdraw the grants themselves, now and for
-- tables created by future migrations.
do $$
declare
  r text;
begin
  foreach r in array array['anon', 'authenticated'] loop
    if exists (select 1 from pg_roles where rolname = r) then
      execute format('revoke all on all tables in schema public from %I', r);
      execute format('revoke all on all sequences in schema public from %I', r);
      execute format('alter default privileges in schema public revoke all on tables from %I', r);
      execute format('alter default privileges in schema public revoke all on sequences from %I', r);
    end if;
  end loop;
end $$;
