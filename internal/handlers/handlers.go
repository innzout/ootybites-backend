// Package handlers wires HTTP routes to business logic. Handlers stay thin —
// they validate input, call internal/services, and answer via pkg/response.
// Most methods here are scaffolding stubs (return NotImplemented) filled in as
// the build order in CLAUDE.md progresses.
package handlers

import (
	"net/http"

	"github.com/innzout/ootybites/internal/config"
	"github.com/innzout/ootybites/internal/db"
	"github.com/innzout/ootybites/internal/middleware"
	"github.com/innzout/ootybites/internal/redis"
	"github.com/innzout/ootybites/internal/services"
	"github.com/innzout/ootybites/pkg/response"
)

// Handlers carries the shared dependencies and services every route needs.
type Handlers struct {
	cfg     *config.Config
	db      *db.Pool
	rc      *redis.Client // may be nil when Redis is not configured
	limiter middleware.Limiter

	auth          *services.Auth
	catalog       *services.Catalog
	coupons       *services.Coupons
	orders        *services.Orders
	adminOrders   *services.AdminOrders
	banners       *services.Banners
	cloudinary    *services.Cloudinary
	dealers       *services.Dealers
	areas         *services.Areas
	addresses     *services.Addresses
	vendors       *services.Vendors
	inventory     *services.Inventory
	hubs          *services.Hubs
	notifications *services.Notifications
	admins        *services.Admins
	settings      *services.Settings
	pages         *services.Pages
	categories    *services.Categories
	game          *services.Game
}

// New builds the handler set and its services. `limiter` is Redis-backed when
// available, else the in-memory fallback.
func New(cfg *config.Config, pool *db.Pool, rc *redis.Client, limiter middleware.Limiter) *Handlers {
	coupons := services.NewCoupons(pool)
	// One short-lived TTL store, shared by OTP codes and single-use game run
	// tickets — two in-memory maps would be pure duplication.
	otpStore := services.NewMemoryOTPStore()

	return &Handlers{
		cfg:           cfg,
		db:            pool,
		rc:            rc,
		limiter:       limiter,
		auth:          services.NewAuth(cfg, pool, otpStore, newSMSSender(cfg)),
		catalog:       services.NewCatalog(pool),
		coupons:       coupons,
		orders:        services.NewOrders(pool, coupons),
		adminOrders:   services.NewAdminOrders(pool),
		banners:       services.NewBanners(pool),
		cloudinary:    services.NewCloudinary(cfg),
		dealers:       services.NewDealers(pool),
		areas:         services.NewAreas(pool),
		addresses:     services.NewAddresses(pool),
		vendors:       services.NewVendors(pool),
		inventory:     services.NewInventory(pool),
		hubs:          services.NewHubs(pool),
		notifications: services.NewNotifications(pool),
		admins:        services.NewAdmins(pool),
		settings:      services.NewSettings(pool),
		pages:         services.NewPages(pool),
		categories:    services.NewCategories(pool),
		game:          services.NewGame(pool, cfg.JWTSecret, otpStore),
	}
}

// Auth exposes the auth service (used by main to seed the bootstrap admin).
func (h *Handlers) Auth() *services.Auth { return h.auth }

// Health is a liveness probe — always OK if the process is up.
func (h *Handlers) Health(w http.ResponseWriter, r *http.Request) {
	response.OK(w, map[string]string{"status": "ok", "service": "ootybites-api"})
}

// Ready is a readiness probe — verifies downstream dependencies respond.
func (h *Handlers) Ready(w http.ResponseWriter, r *http.Request) {
	if err := h.db.Ping(r.Context()); err != nil {
		response.Fail(w, http.StatusServiceUnavailable, response.CodeInternal, "database unavailable")
		return
	}
	response.OK(w, map[string]string{"status": "ready"})
}

// newSMSSender returns a live sender, or an untyped nil interface when MSG91 is
// unconfigured. Returning *services.MSG91 directly would produce a non-nil
// interface holding a nil pointer, so the `a.sms == nil` guard in the auth
// service would not fire and OTP sends would panic instead of failing cleanly.
func newSMSSender(cfg *config.Config) services.SMSSender {
	if m := services.NewMSG91(cfg); m != nil {
		return m
	}
	return nil
}
