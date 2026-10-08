# Ootybites — Architecture

E-commerce store for Nilgiris / Ooty products. Brand: **Ootybites** ("fresh from Ooty").
Part of INNZOUT Technologies, alongside Hirezout & Findzout.

---

## 1. Stack

| Layer      | Choice                                             |
|------------|----------------------------------------------------|
| Frontend   | Next.js (latest, App Router) + Tailwind + Zustand  |
| Backend    | Go (net/http + chi router)                         |
| Database   | Supabase Postgres                                  |
| Cache / RL | Redis (Upstash)                                    |
| Images     | Cloudinary (signed uploads, multiple per product)  |
| OTP        | MSG91                                              |
| Payment    | Cash on Delivery only                              |
| Auth       | JWT — customer (OTP) and admin (user/pass), separate audiences |

Sessions are **stateless JWT** for v1. A Redis token blacklist for instant
revoke/logout is a later add if needed.

---

## 2. Domain model

**Product → Variant** is the core idea. A product is the listing; variants are
the actual sellable units and carry price + stock. This is what lets tea
(250 g / 500 g / 1 kg) and varki (Pack of 15 nos) live under one model.

- Units: `mg | g | kg | ml | l | nos | packets`
- No categories in v1 — flat catalog.
- Orders **snapshot** product name, variant, price and address at placement, so
  later edits never rewrite historical orders.

See `backend/migrations/0001_init.sql` for the full schema.

---

## 3. Auth flows

### Customer (OTP via MSG91)
1. `POST /api/auth/otp/request { phone }`
   - Redis rate-limit check (`otp:rl:{phone}`, `otp:rl:ip:{ip}`)
   - generate 6-digit code, store `otp:{phone}` TTL 5 min, send via MSG91
2. `POST /api/auth/otp/verify { phone, otp }`
   - check attempts (`otp:attempts:{phone}`, max 5), verify code
   - find-or-create customer, issue **customer JWT**

### Admin (username + password)
- `POST /api/admin/auth/login { username, password }`
  - argon2id verify → issue **admin JWT** (`role=admin`)
- Middleware keeps the two token audiences strictly separate; a customer token
  can never reach admin routes and vice-versa.

---

## 4. Redis usage

| Key                       | Purpose                          | TTL          |
|---------------------------|----------------------------------|--------------|
| `otp:{phone}`             | active OTP code                  | 5 min        |
| `otp:rl:{phone}`          | OTP send limit (e.g. 3 / 15 min) | 15 min       |
| `otp:rl:ip:{ip}`          | OTP send limit per IP            | 15 min       |
| `otp:attempts:{phone}`    | verify attempts cap (5)          | 15 min       |
| `rl:{route}:{ip|cust}`    | sliding-window API rate limit    | window-sized |

Rate-limited routes: OTP request/verify, admin login, order place, coupon validate.

---

## 5. Coupon engine

Fields: see `coupons` table. Validation runs in order and returns the **specific
failing reason** (fail fast):

1. exists + `is_active` + within `valid_from`/`valid_to`
2. `used_count < usage_limit_total` AND per-user redemptions `< usage_limit_per_user`
3. cart contains ≥1 applicable product (scope check) → else "Coupon not applicable"
4. `subtotal ≥ min_order_value`
5. customer delivered-lifetime-value `≥ min_customer_lifetime_value`
6. `discount = min(applicable_items_subtotal × pct/100, max_discount_cap)`

- Product-specific coupons discount **only the applicable line items**.
- Coupon UI renders only when at least one active coupon exists (`is_active`).
- The engine is a **pure function** `EvaluateCoupon(cart, coupon, customerStats)`
  returning `{ valid, discount, reason }`, called from both `POST /coupons/validate`
  (preview) and order placement (authoritative recompute — never trust client total).

---

## 6. Order lifecycle

```
placed ─┬─▶ reached_dealer ─┬─▶ delivered
        └─▶ cancelled ◀──────┘   (cancel not allowed from delivered)
```

- Forward-only; admin-driven; every transition written to `order_status_history`.
- **Cancel** requires a reason (stored in history) and **restores stock** to the
  affected variants. Excluded from revenue stats, still visible/filterable.
- **Edit shipping address** allowed only while `placed` or `reached_dealer`;
  updates the order's shipping snapshot and logs a history note.

### Placement (transactional)
1. Re-validate each cart item server-side: variant active, in stock, current price.
2. Recompute subtotal; re-run coupon engine (authoritative).
3. Decrement `stock_qty` for each variant (guard against oversell).
4. Insert `orders` + `order_items` (all snapshotted) + first `order_status_history`.
5. If coupon used: insert `coupon_redemptions`, increment `used_count`.
6. All in one DB transaction.

`order_number` format: `OB-YYYYMMDD-NNNN`.

---

## 7. API route map

```
PUBLIC
  POST /api/auth/otp/request
  POST /api/auth/otp/verify
  GET  /api/products                    ?page&limit&q
  GET  /api/products/:slug              (variants + images)

CUSTOMER (customer JWT)
  GET  /api/me            PUT /api/me
  CRUD /api/addresses
  POST /api/coupons/validate            (cart + code -> discount | reason)
  POST /api/orders                      (place, COD)
  GET  /api/orders        GET /api/orders/:id

ADMIN (admin JWT, prefix /api/admin)
  POST   /auth/login
  GET/POST        /products
  GET/PUT/DELETE  /products/:id
  POST            /products/:id/variants
  PUT/DELETE      /variants/:id
  POST            /products/:id/images        (Cloudinary signed)
  DELETE          /images/:id
  CRUD            /coupons
  GET   /orders   ?status&month=YYYY-MM&from&to&sort&order&page&limit
  GET   /orders/:id
  PATCH /orders/:id/status                     (guarded, reason on cancel)
  PUT   /orders/:id/address                    (placed|reached_dealer only)
  POST  /orders/:id/note
  GET   /orders/:id/invoice                    (COD receipt, printable)
  GET   /dashboard/stats
```

### Admin orders list (tracking screen)
Filter by `status`, by `month`, or by custom `from`/`to` range (range wins when
set). Sort by `placed_at | total | status`. Server-side pagination.
Default view: **status=placed, oldest first** + a "days since placed" column so
aging orders surface for prioritization. Backed by `idx_orders_status_placed`.

---

## 8. Cloudinary

Backend issues a **signed** upload signature; the browser uploads directly to
Cloudinary and returns `{ public_id, url }`, which the backend saves to
`product_images`. Secret never leaves the server. Images support `sort_order`
and one `is_primary`.

---

## 9. Frontend structure (Next.js App Router)

```
src/
  app/
    (shop)/
      page.tsx                      # home — product grid
      products/[slug]/page.tsx
      cart/page.tsx
      checkout/page.tsx
      orders/page.tsx
      orders/[id]/page.tsx
      terms/page.tsx  privacy/page.tsx
      layout.tsx
    (auth)/login/page.tsx           # OTP
    admin/
      login/page.tsx
      (protected)/
        dashboard/page.tsx
        products/page.tsx  products/new/page.tsx  products/[id]/edit/page.tsx
        coupons/page.tsx
        orders/page.tsx  orders/[id]/page.tsx
        layout.tsx                  # admin shell + auth guard
    layout.tsx  globals.css
  components/
    ui/       Button Input Select Modal ConfirmDialog Toast Badge Spinner
    product/  ProductCard ProductGrid VariantSelector ImageGallery
    cart/     CartItem CartSummary CouponInput
    checkout/ AddressForm OrderSummary
    admin/    ProductForm VariantEditor ImageUploader CouponForm
              OrderTable OrderDetail StatusBadge StatCard OrderFilters
    layout/   Navbar Footer
  lib/        api.ts  validators.ts  format.ts  cloudinary.ts
  hooks/      useAuth  useCart  useConfirm  useToast
  store/      cartStore  authStore     # Zustand
  types/      index.ts
```

Cart is client-side (Zustand), **re-validated server-side at checkout** — a stale
cart can never place a bad order. Code-split admin from shop via route groups;
lazy-load heavy admin widgets (image uploader, charts).

---

## 10. Backend structure (Go)

```
backend/
  cmd/api/main.go
  internal/
    config/                 # env loading
    db/                     # pgx pool, sqlc queries
    redis/                  # client + rate-limit helpers
    handlers/               # auth, products, orders, coupons, admin, dashboard
    middleware/             # auth(customer/admin), ratelimit, cors, recover, logger
    services/               # otp(msg91), coupon, order, cloudinary
    models/
    validators/             # server-side field validators (mirror the FE)
  pkg/response/             # {success, data, error{code,message,fields}}
  migrations/
```

---

## 11. Reusable validation & UX layer

- `lib/validators.ts`: pure validators (`required`, `phoneIN`, `pincode`,
  `positiveNumber`, `minLength`, …) each returning an error string or `null`;
  a `useForm` hook wires them to fields. Backend `validators/` mirrors these and
  returns the same `error.fields` shape, so client and server share one error
  language.
- `<FieldError>` for inline errors; `useToast` for API-level errors.
- `useConfirm()` → `await confirm({title, message})` returns a boolean, backed by
  one `<ConfirmDialog>`. Wired only to meaningful actions: place order, delete
  product/variant/image/coupon, change order status, cancel order, edit address,
  logout — **not** every button.

---

## 12. Static / legal
- `/terms` and `/privacy` pages (content editable later; static for v1).

---

## 13. v1 scope checklist
- [ ] Phone-OTP customer auth (MSG91) + admin user/pass
- [ ] Flat product catalog with variants + multi-image (Cloudinary)
- [ ] Cart + client validation, server re-validation at checkout
- [ ] Coupon CRUD + engine (percentage, product/all scope, order & account gates)
- [ ] COD checkout with address capture
- [ ] Orders: placed → reached_dealer → delivered → (+cancelled)
- [ ] Admin order list: status + month + range filters, sort, pagination
- [ ] Admin order detail: status update, cancel (+restock), edit address, note, invoice
- [ ] Dashboard stats
- [ ] Redis rate limiting on sensitive routes
- [ ] Terms & Privacy pages
