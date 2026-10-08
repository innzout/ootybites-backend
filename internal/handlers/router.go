package handlers

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/innzout/ootybites/internal/middleware"
)

// Router builds the full API surface described in docs/ARCHITECTURE.md §7.
// Route groups mirror the auth boundaries: public, customer (customer JWT),
// and admin (admin JWT). Rate limits guard only the sensitive routes.
func (h *Handlers) Router() http.Handler {
	r := chi.NewRouter()

	// Global middleware — order matters: recover first, then correlate + log.
	r.Use(middleware.Recover)
	r.Use(middleware.RequestID)
	r.Use(middleware.Logger)
	r.Use(middleware.CORS(h.cfg.CORSOrigins, h.cfg.Env == "development"))

	secret := h.cfg.JWTSecret

	// Ops probes.
	r.Get("/healthz", h.Health)
	r.Get("/readyz", h.Ready)

	// Serve locally-uploaded images (public read).
	r.Handle("/uploads/*", http.StripPrefix("/uploads/", http.FileServer(http.Dir(uploadsDir))))

	r.Route("/api", func(api chi.Router) {
		// ---------------- PUBLIC ----------------
		api.Group(func(pub chi.Router) {
			// OTP endpoints are rate-limited per phone/IP. The production limits
			// (5 requests / 15 min) exist to cap SMS spend and slow code
			// guessing — but they also key by IP, so on a dev machine every
			// browser tab, curl and test run shares one bucket and login becomes
			// unusable after a few attempts. OTP_DEV_MODE already returns the
			// code in the response body, so auth is not a real control there;
			// relaxing the limit alongside it changes nothing about production.
			otpReqMax, otpVerifyMax := 5, 10
			if h.cfg.OTPDevMode {
				otpReqMax, otpVerifyMax = 100, 200
			}
			pub.With(middleware.RateLimit(h.limiter, "otp_request", otpReqMax, 15*time.Minute)).
				Post("/auth/otp/request", h.OTPRequest)
			pub.With(middleware.RateLimit(h.limiter, "otp_verify", otpVerifyMax, 15*time.Minute)).
				Post("/auth/otp/verify", h.OTPVerify)

			pub.Get("/products", h.ListProducts)
			pub.Get("/products/{slug}", h.GetProduct)
			pub.Get("/categories", h.ListCategories)
			pub.Get("/banners", h.ListBanners)
			pub.Get("/cities", h.ListCities)
			pub.Get("/settings", h.PublicSettings)
			pub.Get("/pages/{slug}", h.PublicGetPage)
			pub.Get("/express/areas", h.ExpressAreas)
			pub.Get("/express/products", h.ExpressProducts)
			pub.Post("/express-check", h.ExpressCheck)

			// Public league table for the mini-game.
			pub.Get("/game/leaderboard", h.GameLeaderboard)
			// What the league is and what you win — read by the storefront promo.
			pub.Get("/game/season", h.GameSeason)
		})

		// ---------------- CUSTOMER (customer JWT) ----------------
		api.Group(func(cust chi.Router) {
			cust.Use(middleware.RequireCustomer(secret, h.auth.CustomerExists))

			cust.Get("/me", h.GetMe)
			cust.Put("/me", h.UpdateMe)

			cust.Get("/addresses", h.ListAddresses)
			cust.Post("/addresses", h.CreateAddress)
			cust.Put("/addresses/{id}", h.UpdateAddress)
			cust.Delete("/addresses/{id}", h.DeleteAddress)

			cust.With(middleware.RateLimit(h.limiter, "coupon_validate", 30, time.Minute)).
				Post("/coupons/validate", h.ValidateCoupon)
			cust.With(middleware.RateLimit(h.limiter, "order_place", 10, time.Minute)).
				Post("/orders", h.PlaceOrder)

			cust.Get("/orders", h.ListOrders)
			cust.Get("/orders/{id}", h.GetOrder)
			cust.Get("/orders/{id}/reorder", h.Reorder)
			cust.Post("/orders/{id}/cancel", h.CancelOrder)

			cust.Get("/notifications", h.ListNotifications)
			cust.Post("/notifications/read", h.MarkNotificationsRead)

			// Mini-game high scores (per customer).
			cust.Get("/game/highscore", h.GameHighScore)
			cust.With(middleware.RateLimit(h.limiter, "game_start", 60, time.Minute)).
				Post("/game/start", h.GameStartRun)
			cust.With(middleware.RateLimit(h.limiter, "game_score", 60, time.Minute)).
				Post("/game/score", h.GameSubmitScore)
		})

		// ---------------- DEALER (dealer JWT) ----------------
		api.Route("/dealer", func(dl chi.Router) {
			dl.With(middleware.RateLimit(h.limiter, "dealer_login", 10, 15*time.Minute)).
				Post("/auth/login", h.DealerLogin)

			dl.Group(func(sec chi.Router) {
				sec.Use(middleware.RequireDealer(secret, h.dealers.Exists))
				sec.Get("/orders", h.DealerListOrders)
				sec.Get("/orders/{id}", h.DealerGetOrder)
				sec.Patch("/orders/{id}/status", h.DealerUpdateOrderStatus)
			})
		})

		// ---------------- ADMIN (admin JWT) ----------------
		api.Route("/admin", func(adm chi.Router) {
			// Public admin endpoint: login (rate-limited).
			adm.With(middleware.RateLimit(h.limiter, "admin_login", 10, 15*time.Minute)).
				Post("/auth/login", h.AdminLogin)

			// Everything else requires an admin token (whose admin still exists).
			adm.Group(func(sec chi.Router) {
				sec.Use(middleware.RequireAdmin(secret, h.auth.AdminExists))

				// Current admin (any role) — lets the client tailor the UI.
				sec.Get("/me", h.AdminMe)

				// Store settings — any admin reads; only super-admin writes.
				sec.Get("/settings", h.AdminGetSettings)

				// CMS content pages (any admin can manage content).
				sec.Get("/pages", h.AdminListPages)
				sec.Post("/pages", h.AdminCreatePage)
				sec.Get("/pages/{id}", h.AdminGetPage)
				sec.Put("/pages/{id}", h.AdminUpdatePage)
				sec.Delete("/pages/{id}", h.AdminDeletePage)

				// Admin-user management + settings writes — super-admin only.
				sec.Group(func(su chi.Router) {
					su.Use(middleware.RequireSuperAdmin)
					su.Put("/settings", h.AdminUpdateSettings)
					su.Get("/admins", h.AdminListAdmins)
					su.Post("/admins", h.AdminCreateAdmin)
					su.Get("/admins/{id}", h.AdminGetAdmin)
					su.Put("/admins/{id}", h.AdminUpdateAdmin)
					su.Delete("/admins/{id}", h.AdminDeleteAdmin)
				})

				// Catalog
				sec.Get("/products", h.AdminListProducts)
				sec.Post("/products", h.AdminCreateProduct)
				sec.Get("/products/{id}", h.AdminGetProduct)
				sec.Put("/products/{id}", h.AdminUpdateProduct)
				sec.Delete("/products/{id}", h.AdminDeleteProduct)
				sec.Post("/products/{id}/variants", h.AdminCreateVariant)
				sec.Put("/variants/{id}", h.AdminUpdateVariant)
				sec.Delete("/variants/{id}", h.AdminDeleteVariant)
				sec.Post("/uploads/sign", h.AdminSignImageUpload)
				sec.Post("/uploads", h.AdminUploadImage)
				sec.Post("/products/{id}/images", h.AdminAddImage)
				sec.Delete("/images/{id}", h.AdminDeleteImage)

				// Categories (product taxonomy)
				sec.Get("/categories", h.AdminListCategories)
				sec.Post("/categories", h.AdminCreateCategory)
				sec.Get("/categories/{id}", h.AdminGetCategory)
				sec.Put("/categories/{id}", h.AdminUpdateCategory)
				sec.Delete("/categories/{id}", h.AdminDeleteCategory)

				// Hubs (fulfilment centres) + per-hub stock
				sec.Get("/hubs", h.AdminListHubs)
				sec.Post("/hubs", h.AdminCreateHub)
				sec.Get("/hubs/{id}", h.AdminGetHub)
				sec.Put("/hubs/{id}", h.AdminUpdateHub)
				sec.Delete("/hubs/{id}", h.AdminDeleteHub)
				sec.Put("/hubs/{id}/areas", h.AdminAssignHubAreas)
				sec.Get("/hubs/{id}/stock", h.AdminHubStock)
				sec.Post("/hubs/{id}/stock/receive", h.AdminHubReceiveStock)
				sec.Post("/hubs/{id}/stock/adjust", h.AdminHubAdjustStock)
				sec.Get("/hubs/{id}/movements", h.AdminHubMovements)

				// Vendors (suppliers) + inventory
				sec.Get("/vendors", h.AdminListVendors)
				sec.Post("/vendors", h.AdminCreateVendor)
				sec.Get("/vendors/{id}", h.AdminGetVendor)
				sec.Put("/vendors/{id}", h.AdminUpdateVendor)
				sec.Delete("/vendors/{id}", h.AdminDeleteVendor)
				sec.Get("/inventory", h.AdminStockOverview)
				sec.Post("/inventory/receive", h.AdminReceiveStock)
				sec.Post("/inventory/adjust", h.AdminAdjustStock)
				sec.Get("/inventory/{variantId}/movements", h.AdminStockMovements)

				// Delivery areas (pincode → dealer auto-assign)
				sec.Get("/areas", h.AdminListAreas)
				sec.Post("/areas", h.AdminCreateArea)
				sec.Get("/areas/{id}", h.AdminGetArea)
				sec.Put("/areas/{id}", h.AdminUpdateArea)
				sec.Delete("/areas/{id}", h.AdminDeleteArea)

				// Dealers + order assignment
				sec.Get("/dealers", h.AdminListDealers)
				sec.Post("/dealers", h.AdminCreateDealer)
				sec.Get("/dealers/{id}", h.AdminGetDealer)
				sec.Put("/dealers/{id}", h.AdminUpdateDealer)
				sec.Delete("/dealers/{id}", h.AdminDeleteDealer)
				sec.Patch("/orders/{id}/assign", h.AdminAssignOrderDealer)

				// Banners
				sec.Get("/banners", h.AdminListBanners)
				sec.Post("/banners", h.AdminCreateBanner)
				sec.Get("/banners/{id}", h.AdminGetBanner)
				sec.Put("/banners/{id}", h.AdminUpdateBanner)
				sec.Delete("/banners/{id}", h.AdminDeleteBanner)

				// Coupons
				sec.Get("/coupons", h.AdminListCoupons)
				sec.Post("/coupons", h.AdminCreateCoupon)
				sec.Get("/coupons/{id}", h.AdminGetCoupon)
				sec.Put("/coupons/{id}", h.AdminUpdateCoupon)
				sec.Delete("/coupons/{id}", h.AdminDeleteCoupon)

				// Orders (tracking screen) + dashboard
				sec.Get("/orders", h.AdminListOrders)
				sec.Get("/orders/{id}", h.AdminGetOrder)
				sec.Patch("/orders/{id}/status", h.AdminUpdateOrderStatus)
				sec.Put("/orders/{id}/address", h.AdminUpdateOrderAddress)
				sec.Post("/orders/{id}/note", h.AdminAddOrderNote)
				sec.Get("/orders/{id}/invoice", h.AdminOrderInvoice)
				sec.Get("/dashboard/stats", h.AdminDashboardStats)

				sec.Get("/notifications", h.AdminListNotifications)
				sec.Post("/notifications/read", h.AdminMarkNotificationsRead)

				// Game league: seasons + winners (prize fulfilment).
				sec.Get("/game/seasons", h.AdminListSeasons)
				sec.Post("/game/seasons", h.AdminOpenSeason)
				sec.Post("/game/seasons/close", h.AdminCloseSeason)
			})
		})
	})

	return r
}
