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
	r.Use(middleware.CORS(h.cfg.CORSOrigins))

	secret := h.cfg.JWTSecret

	// Ops probes.
	r.Get("/healthz", h.Health)
	r.Get("/readyz", h.Ready)

	// Serve locally-uploaded images (public read).
	r.Handle("/uploads/*", http.StripPrefix("/uploads/", http.FileServer(http.Dir(uploadsDir))))

	r.Route("/api", func(api chi.Router) {
		// ---------------- PUBLIC ----------------
		api.Group(func(pub chi.Router) {
			// OTP endpoints are rate-limited per phone/IP.
			pub.With(middleware.RateLimit(h.limiter, "otp_request", 5, 15*time.Minute)).
				Post("/auth/otp/request", h.OTPRequest)
			pub.With(middleware.RateLimit(h.limiter, "otp_verify", 10, 15*time.Minute)).
				Post("/auth/otp/verify", h.OTPVerify)

			pub.Get("/products", h.ListProducts)
			pub.Get("/products/{slug}", h.GetProduct)
			pub.Get("/banners", h.ListBanners)
		})

		// ---------------- CUSTOMER (customer JWT) ----------------
		api.Group(func(cust chi.Router) {
			cust.Use(middleware.RequireCustomer(secret))

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
		})

		// ---------------- DEALER (dealer JWT) ----------------
		api.Route("/dealer", func(dl chi.Router) {
			dl.With(middleware.RateLimit(h.limiter, "dealer_login", 10, 15*time.Minute)).
				Post("/auth/login", h.DealerLogin)

			dl.Group(func(sec chi.Router) {
				sec.Use(middleware.RequireDealer(secret))
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

			// Everything else requires an admin token.
			adm.Group(func(sec chi.Router) {
				sec.Use(middleware.RequireAdmin(secret))

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

				// Delivery areas (pincode → dealer auto-assign)
				sec.Get("/areas", h.AdminListAreas)
				sec.Post("/areas", h.AdminCreateArea)
				sec.Put("/areas/{id}", h.AdminUpdateArea)
				sec.Delete("/areas/{id}", h.AdminDeleteArea)

				// Dealers + order assignment
				sec.Get("/dealers", h.AdminListDealers)
				sec.Post("/dealers", h.AdminCreateDealer)
				sec.Put("/dealers/{id}", h.AdminUpdateDealer)
				sec.Delete("/dealers/{id}", h.AdminDeleteDealer)
				sec.Patch("/orders/{id}/assign", h.AdminAssignOrderDealer)

				// Banners
				sec.Get("/banners", h.AdminListBanners)
				sec.Post("/banners", h.AdminCreateBanner)
				sec.Put("/banners/{id}", h.AdminUpdateBanner)
				sec.Delete("/banners/{id}", h.AdminDeleteBanner)

				// Coupons
				sec.Get("/coupons", h.AdminListCoupons)
				sec.Post("/coupons", h.AdminCreateCoupon)
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
			})
		})
	})

	return r
}
