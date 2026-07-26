// Command api is the Ootybites HTTP server entrypoint. It loads config, opens
// Postgres + Redis, runs migrations, and serves the API with graceful shutdown.
package main

import (
	"context"
	"errors"
	"log"
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

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	slog.Info("config loaded", "env", cfg.Env)

	ctx := context.Background()

	pool, err := db.New(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	defer pool.Close()
	slog.Info("connected to Postgres")

	if err := migrate.Run(ctx, pool, migrations.Files); err != nil {
		log.Fatalf("migrate: %v", err)
	}
	slog.Info("migrations up to date")

	rc, err := redis.New(ctx, cfg.RedisURL)
	if err != nil {
		log.Fatalf("redis: %v", err)
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
		log.Fatalf("seed admin: %v", err)
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

	// Serve in the background; block on a shutdown signal.
	go func() {
		slog.Info("listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	slog.Info("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("graceful shutdown failed", "error", err)
	}
}
