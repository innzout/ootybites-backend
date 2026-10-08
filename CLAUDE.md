# CLAUDE.md — Ootybites

Project intelligence for AI-assisted development. Read this before generating code.

## What this is
**Ootybites** — e-commerce store for Nilgiris/Ooty products (tea, varki, snacks,
oils, honey). Brand meaning: "fresh from Ooty". INNZOUT Technologies project,
sibling to Hirezout & Findzout. Follow the same conventions as those repos.

## Stack (use latest stable of each)
- **Frontend:** Next.js App Router, Tailwind, Zustand, TypeScript
- **Backend:** Go (chi router, pgx, sqlc)
- **DB:** Supabase Postgres  ·  **Cache/RL:** Redis (Upstash)
- **Images:** Cloudinary (signed uploads)  ·  **OTP:** MSG91  ·  **Pay:** COD only

## Non-negotiable rules
1. **Product → Variant.** Price and stock live on the *variant*, never the product.
   Cart and orders reference `variant_id`.
2. **Orders snapshot everything** (product name, variant, unit, price, address).
   Never join an old order back to a live product for display.
3. **Server is the source of truth for money.** Re-validate cart and re-run the
   coupon engine on the server at checkout; never trust a client-sent total.
4. **Two separate JWT audiences:** customer (OTP) vs admin (user/pass). A customer
   token must never reach admin routes.
5. **Every user input is validated** on both client (`lib/validators.ts`) and
   server (`internal/validators`), sharing the same `error.fields` error shape.
6. **Confirm popups only for meaningful actions** (place order, delete anything,
   change/cancel order status, edit address, logout) — not for every button.
7. **Rate-limit sensitive routes** in Redis: OTP request/verify, admin login,
   order place, coupon validate.
8. **Secrets stay server-side.** Cloudinary signing, MSG91, DB, JWT secret — never
   shipped to the client.

## Units
`mg | g | kg | ml | l | nos | packets`. Admin picks unit + value per variant.

## Order state machine
`placed → reached_dealer → delivered`, plus `cancelled` (reachable from placed /
reached_dealer, never from delivered). Every transition logged to
`order_status_history`. Cancel requires a reason and **restores stock**.

## Coupons
Percentage only for v1 (keep `discount_type` column for future flat). Scope =
`all | specific_products`. Gates: `min_order_value` and
`min_customer_lifetime_value` (customer's delivered-order total). Always apply
`max_discount_cap`. Product-specific coupons discount only applicable items.
`is_active` is the "show when enabled" toggle.

## Code quality expectations
- Code splitting: admin and shop are separate route groups; lazy-load heavy admin
  widgets (image uploader, charts).
- Small, reusable, single-purpose components; shared primitives in `components/ui`.
- No duplicated fetch logic — one typed `lib/api.ts` client with auth + error
  handling. No duplicated validation — reuse `lib/validators.ts`.
- Keep handlers thin; business logic in `internal/services`.
- Index-aware queries for the admin order list (status + date filters, sort, page).

## Key files
- `docs/ARCHITECTURE.md` — full blueprint (auth, routes, coupon engine, structure).
- `backend/migrations/0001_init.sql` — schema (source of truth for the data model).

## Order number format
`OB-YYYYMMDD-NNNN`.

## Environment variables (expected)
```
# backend
DATABASE_URL=            # Supabase Postgres
REDIS_URL=               # Upstash
JWT_SECRET=
MSG91_AUTH_KEY=
MSG91_TEMPLATE_ID=
MSG91_SENDER_ID=
CLOUDINARY_CLOUD_NAME=
CLOUDINARY_API_KEY=
CLOUDINARY_API_SECRET=
# frontend
NEXT_PUBLIC_API_BASE_URL=
NEXT_PUBLIC_CLOUDINARY_CLOUD_NAME=
NEXT_PUBLIC_GOOGLE_MAPS_API_KEY=   # address/checkout map picker (optional; degrades to manual entry)
```

## Build order (scaffolding plan)
1. ✅ Schema + docs (this drop)
2. Backend skeleton: config, db pool, redis, router, middleware, response envelope
3. Auth: OTP (MSG91) + admin login
4. Catalog: products/variants/images (+ Cloudinary signing)
5. Cart validate + coupon engine + order placement (transactional)
6. Admin: order list (filters/sort/paginate) + order detail actions + dashboard
7. Frontend: ui primitives → shop pages → checkout → admin
8. Terms/Privacy, polish, rate-limit tuning
