// Package db owns the pgx connection pool to Supabase Postgres.
// sqlc-generated queries will live alongside this package.
package db

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Pool is a thin alias so the rest of the app depends on our package, not pgx
// directly, keeping the swap surface small.
type Pool = pgxpool.Pool

// statementTimeoutMS kills any query running longer than 30s, so a runaway
// query can't hold a pool connection and stall unrelated requests.
const statementTimeoutMS = "30000"

// New opens and verifies a pooled connection to Postgres, tuned for Supabase.
func New(ctx context.Context, databaseURL string) (*Pool, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}

	// Supabase fronts Postgres with PgBouncer in transaction mode, which does
	// not support server-side prepared statements. SimpleProtocol avoids them.
	cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	cfg.ConnConfig.RuntimeParams["statement_timeout"] = statementTimeoutMS
	cfg.ConnConfig.Tracer = &slowQueryTracer{threshold: 500 * time.Millisecond}

	// Conservative pool — Supabase free tier caps direct connections.
	cfg.MaxConns = 10
	cfg.MinConns = 2
	cfg.MaxConnLifetime = time.Hour
	cfg.MaxConnIdleTime = 30 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return pool, nil
}

// slowQueryTracer logs any query slower than threshold to the default logger.
type slowQueryTracer struct{ threshold time.Duration }

type ctxQueryKey struct{}

type queryMeta struct {
	start time.Time
	sql   string
}

func (t *slowQueryTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	return context.WithValue(ctx, ctxQueryKey{}, queryMeta{start: time.Now(), sql: data.SQL})
}

func (t *slowQueryTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryEndData) {
	meta, ok := ctx.Value(ctxQueryKey{}).(queryMeta)
	if !ok || meta.start.IsZero() {
		return
	}
	if elapsed := time.Since(meta.start); elapsed >= t.threshold {
		sql := meta.sql
		if len(sql) > 300 {
			sql = sql[:300] + "…"
		}
		slog.Warn("slow query detected", "duration_ms", elapsed.Milliseconds(), "sql", sql)
	}
}
