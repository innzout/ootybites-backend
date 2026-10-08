// Command api is the Ootybites HTTP server entrypoint. It loads config, opens
// Postgres + Redis, runs migrations, and serves the API with graceful shutdown.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/innzout/ootybites/internal/config"
	"github.com/innzout/ootybites/internal/db"
	"github.com/innzout/ootybites/internal/handlers"
	"github.com/innzout/ootybites/internal/middleware"
	"github.com/innzout/ootybites/internal/migrate"
	"github.com/innzout/ootybites/internal/redis"
	migrations "github.com/innzout/ootybites/migrations"
)

// fatal logs a startup failure at ERROR and exits non-zero.
//
// It exists because slog.SetDefault also redirects the stdlib log package into
// the slog handler at INFO — so log.Fatalf emitted fatal errors as
// {"level":"INFO"}, making a server that failed to boot invisible to any
// error-level alerting or log filter in production.
func fatal(msg string, err error) {
	slog.Error(msg, "error", err)
	os.Exit(1)
}

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))

	cfg, err := config.Load()
	if err != nil {
		fatal("config load failed", err)
	}
	// Log the parsed CORS allow-list: a mismatch here fails every browser request
	// while curl still works, which is otherwise very hard to spot from logs.
	slog.Info("config loaded", "env", cfg.Env, "cors_origins", cfg.CORSOrigins)

	ctx := context.Background()

	pool, err := db.New(ctx, cfg.DatabaseURL)
	if err != nil {
		fatal("database connection failed", err)
	}
	defer pool.Close()
	slog.Info("connected to Postgres")

	if err := migrate.Run(ctx, pool, migrations.Files); err != nil {
		fatal("migrations failed", err)
	}
	slog.Info("migrations up to date")

	rc, err := redis.New(ctx, cfg.RedisURL)
	if err != nil {
		fatal("redis connection failed", err)
	}

	// Rate limiter: Redis-backed when configured, otherwise in-memory (local dev).
	var limiter middleware.Limiter
	if rc != nil {
		defer rc.Close()
		limiter = rc
		slog.Info("connected to Redis")
	} else {
		limiter = middleware.NewMemoryLimiter()
		slog.Warn("REDIS_URL not set — using in-memory rate limiter (dev only)")
	}

	h := handlers.New(cfg, pool, rc, limiter)

	// Ensure the bootstrap admin exists so the admin panel is reachable.
	if err := h.Auth().SeedAdmin(ctx, cfg.AdminUser, cfg.AdminPass); err != nil {
		fatal("seed admin failed", err)
	}
	slog.Info("admin seeded", "username", cfg.AdminUser)

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           h.Router(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	// Serve in the background. A serve failure (most commonly the port already
	// being held) is reported back to main rather than exiting from inside the
	// goroutine — os.Exit there would skip every defer above, leaking the
	// Postgres pool and the Redis client on a failed boot.
	serveErr := make(chan error, 1)
	go func() {
		slog.Info("listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
			return
		}
		serveErr <- nil
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-serveErr:
		if err != nil {
			// os.Exit skips defers, so close explicitly here.
			slog.Error("server stopped unexpectedly", "error", err)
			pool.Close()
			if rc != nil {
				rc.Close()
			}
			os.Exit(1)
		}
		return
	case <-stop:
	}
	slog.Info("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("graceful shutdown failed", "error", err)
	}
}
