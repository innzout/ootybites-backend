# Deploying the Ootybites API — Railway + Supabase

Staging topology: **Railway** runs this Go service, **Supabase** hosts Postgres,
**Vercel** serves the frontend.

---

## 1. Supabase

Create the project, then take the connection string from
**Project Settings → Database → Connection string → URI**.

Use the **pooled (Transaction mode)** string — host contains `pooler`, port
**6543** — not the direct `db.<ref>.supabase.co:5432` one. Railway dials from
outside the VPC and the pooler is what survives that.

```
postgresql://postgres.<ref>:<password>@aws-0-<region>.pooler.supabase.com:6543/postgres
```

**This already works — no flags needed.** PgBouncer in transaction mode cannot
do server-side prepared statements, which normally breaks pgx. `internal/db`
sets `QueryExecModeSimpleProtocol` for exactly this reason. Likewise the order
number sequence uses `pg_advisory_xact_lock` (transaction-scoped, not session)
so it is safe through the pooler.

Two things to know:

- **URL-encode the password.** Supabase generates passwords containing `@`, `#`
  and `?`. Unencoded they corrupt the URI and you get a confusing
  `parse database url` failure at boot.
- **The pool asks for up to 10 connections** (`MaxConns = 10` in `internal/db`).
  That is sized for the Supabase free tier. If you scale Railway past one
  replica, lower it or raise the Supabase pool.

**You do not need to run migrations.** They are embedded in the binary and
applied at boot; the log line is `migrations up to date`.

---

## 2. Railway

Point a new service at `innzout/ootybites-backend`. Railway detects Go, builds
`./cmd/api`, and injects `PORT` — which the server already reads, so there is no
Procfile or Dockerfile to add.

Set the healthcheck path to **`/healthz`** (liveness). There is also `/readyz`,
which pings the database — better as an uptime check than a deploy gate, since
it fails while Supabase is briefly unreachable.

### Variables

| Variable | Staging value | Notes |
|---|---|---|
| `APP_ENV` | `staging` | `development` | `staging` | `production`. Only `development` relaxes CORS, so staging is as strict as production |
| `DATABASE_URL` | Supabase pooled URI | Port 6543, password URL-encoded |
| `JWT_SECRET` | `openssl rand -base64 48` | Changing it logs everyone out |
| `ADMIN_USER` | your choice | |
| `ADMIN_PASS` | **a new password** | ⚠️ see below |
| `CORS_ORIGINS` | `https://<app>.vercel.app` | Exact origin, no trailing slash |
| `OTP_DEV_MODE` | `true` for now | ⚠️ see below |
| `REDIS_URL` | *(optional)* | Upstash; without it rate limiting is per-instance |
| `CLOUDINARY_*` | *(optional)* | Image uploads fall back to local disk without them |

---

## 3. Two defaults that are dangerous in production

Both of these **default to the unsafe value when unset**, so forgetting them is
silent rather than loud.

### `ADMIN_PASS` defaults to `admin123`

`SeedAdmin` re-upserts the bootstrap admin on **every boot**. Deploy without
setting this and your staging admin panel is `admin` / `admin123`, with full
access to orders, customers and pricing.

Setting it to a new value and redeploying is enough to rotate — no migration.

### `OTP_DEV_MODE` defaults to `true`

In dev mode the API **returns the login OTP in its own response** and sends no
SMS. Anyone who can reach the API can sign in as any phone number.

That is acceptable only behind a staging URL nobody has been given. Before real
customers it must be `false`, which requires `MSG91_AUTH_KEY` and
`MSG91_TEMPLATE_ID` — with `false` and no keys, login returns **503** by design
rather than pretending to send a code.

---

## 4. CORS is the usual first failure

In `APP_ENV=development` any `localhost` origin is allowed on any port. In
`production` that exemption is off and **only the exact strings in
`CORS_ORIGINS` are accepted**.

A mismatch does not look like a CORS problem from the frontend — every call just
fails and the screens show generic errors. Check the browser console for
`No 'Access-Control-Allow-Origin' header`.

Vercel gives each deployment its own preview URL. Those are *not* covered by the
production domain entry, so preview deployments will fail CORS unless you add
them. `CORS_ORIGINS` is comma-separated.

---

## 5. Order of operations

1. Supabase project → copy the pooled URI
2. Railway service → set the variables above → deploy
3. Confirm the logs show `connected to Postgres`, `migrations up to date`,
   `listening`
4. `curl https://<svc>.up.railway.app/readyz` → `{"status":"ready"}`
5. Vercel → set `NEXT_PUBLIC_API_BASE_URL` to `https://<svc>.up.railway.app/api`
6. Come back to Railway and set `CORS_ORIGINS` to the Vercel URL, redeploy

Steps 5 and 6 are circular — each side needs the other's URL — so the API goes
up first with a placeholder origin, and gets the real one once Vercel has built.

---

## 6. Startup failures

Every boot failure now logs at `level":"ERROR"` and exits non-zero, so Railway
will show it as a crash rather than a silent restart loop.

| Log | Cause |
|---|---|
| `config load failed` | A required variable is missing or malformed |
| `database connection failed` | Wrong URI, unencoded password, or wrong port |
| `migrations failed` | The DB user cannot create tables |
| `server stopped unexpectedly … bind` | Something else holds the port — do not hardcode `PORT` |

`REDIS_URL not set — using in-memory rate limiter` is a **warning, not an
error**. The service runs fine; the limiter is just per-instance, so it does not
hold across replicas.
