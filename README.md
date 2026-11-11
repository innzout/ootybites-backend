# Ootybites — backend

Go (chi + pgx) API for the Ootybites storefront. See `CLAUDE.md` for the
non-negotiable domain rules and `docs/ARCHITECTURE.md` for the blueprint.

## Run locally

```bash
cp .env.example .env     # fill DATABASE_URL, JWT_SECRET, ADMIN_USER, ADMIN_PASS
go run ./cmd/api         # serves on :8080
```

Migrations in `migrations/*.sql` are embedded in the binary and applied
automatically at boot — there is no separate migrate step.

## Tests

```bash
go test ./...            # unit tests; no database needed
```

The coupon engine is covered by pure unit tests — it decides how much money
comes off an order, so a silent change there is the most expensive kind.

The review permission rules (who may write a review, who may delete one) live
in SQL, so they are covered by **integration tests against a real Postgres**.
A mock would test nothing: the whole rule is the
`orders → order_items → product_variants` join and the ownership predicate
inside the `DELETE`.

```bash
createdb ootybites_test
TEST_DATABASE_URL='postgres://postgres:PASS@127.0.0.1:5432/ootybites_test?sslmode=disable' \
  go test ./internal/services/ -run Integration -v
```

The tests run the migrations themselves and clean up the rows they create, so
the database can be reused. Without `TEST_DATABASE_URL` they skip, which keeps
plain `go test ./...` green on a machine with no database.

Point it at a **throwaway database**, never a real one.

## Staging (Railway)

**Full step-by-step guide: [docs/DEPLOY.md](docs/DEPLOY.md)** — Supabase connection
string, every Railway variable, and the two defaults that are unsafe if left unset.

Railway detects Go and builds `./cmd/api`. It injects `PORT`; the server already
reads it, so no Procfile is needed.

Set these variables in the Railway service:

| Variable | Value |
|---|---|
| `APP_ENV` | `production` |
| `DATABASE_URL` | Supabase **pooled** connection string (PgBouncer, transaction mode) |
| `JWT_SECRET` | `openssl rand -base64 48` |
| `ADMIN_USER` / `ADMIN_PASS` | a **new** password, not the local one |
| `CORS_ORIGINS` | the Vercel URL, e.g. `https://ootybites.vercel.app` |
| `OTP_DEV_MODE` | `true` until MSG91 is live |
| `REDIS_URL` | optional — without it rate limiting is in-memory, which is per-instance only |

Two things to know about this stage:

- **`OTP_DEV_MODE=true` returns the OTP in the API response.** Anyone who can
  reach the API can log in as any phone number. Acceptable for a closed staging
  URL; it must be `false` before real customers, which requires the MSG91 keys.
- **`CORS_ORIGINS` must list the exact Vercel origin.** In `APP_ENV=production`
  only the listed origins are allowed; the dev-mode loopback exemption is off.
