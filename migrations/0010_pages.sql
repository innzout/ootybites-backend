-- =====================================================================
-- CMS content pages — editable prose (Terms, Privacy, and any others).
-- Body is lightweight markdown (headings #/##, - lists, **bold**, blank-line
-- paragraphs) rendered safely on the client (no raw HTML). Terms & Privacy are
-- seeded from the previously hardcoded copy so existing links keep working.
-- =====================================================================
create table if not exists pages (
  id           uuid primary key default gen_random_uuid(),
  slug         text unique not null,
  title        text not null,
  body         text not null default '',
  is_published boolean not null default true,
  created_at   timestamptz not null default now(),
  updated_at   timestamptz not null default now()
);

insert into pages (slug, title, body, is_published) values
  ('terms', 'Terms & Conditions', $md$Ootybites sells Nilgiris/Ooty products on a Cash-on-Delivery basis.

Orders are confirmed at placement; prices and availability are re-checked on our servers before an order is accepted.

Full terms will be published here.$md$, true),
  ('privacy', 'Privacy Policy', $md$We collect your phone number for login (via OTP) and your delivery address to fulfil orders.

We never sell your data.

The full privacy policy will be published here.$md$, true)
on conflict (slug) do nothing;
