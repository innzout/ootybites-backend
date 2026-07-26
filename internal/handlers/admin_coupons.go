package handlers

import (
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/innzout/ootybites/internal/services"
	"github.com/innzout/ootybites/internal/validators"
	"github.com/innzout/ootybites/pkg/response"
)

type couponBody struct {
	Code                     string     `json:"code"`
	IsActive                 bool       `json:"is_active"`
	DiscountValue            float64    `json:"discount_value"`
	MaxDiscountCap           *float64   `json:"max_discount_cap"`
	ApplicableScope          string     `json:"applicable_scope"`
	MinOrderValue            float64    `json:"min_order_value"`
	MinCustomerLifetimeValue float64    `json:"min_customer_lifetime_value"`
	UsageLimitTotal          *int       `json:"usage_limit_total"`
	UsageLimitPerUser        *int       `json:"usage_limit_per_user"`
	ValidFrom                *time.Time `json:"valid_from"`
	ValidTo                  *time.Time `json:"valid_to"`
	ProductIDs               []string   `json:"product_ids"`
}

func (b couponBody) validate() map[string]string {
	f := map[string]string{
		"code":  validators.Required(b.Code),
		"scope": validators.CouponScope(b.ApplicableScope),
	}
	if b.DiscountValue <= 0 || b.DiscountValue > 100 {
		f["discount_value"] = "Percentage must be between 1 and 100"
	}
	if b.ApplicableScope == "specific_products" && len(b.ProductIDs) == 0 {
		f["product_ids"] = "Select at least one product"
	}
	return validators.Fields(f)
}

func (b couponBody) toInput() services.CouponInput {
	return services.CouponInput{
		Code: b.Code, IsActive: b.IsActive, DiscountValue: b.DiscountValue, MaxDiscountCap: b.MaxDiscountCap,
		ApplicableScope: b.ApplicableScope, MinOrderValue: b.MinOrderValue,
		MinCustomerLifetimeValue: b.MinCustomerLifetimeValue, UsageLimitTotal: b.UsageLimitTotal,
		UsageLimitPerUser: b.UsageLimitPerUser, ValidFrom: b.ValidFrom, ValidTo: b.ValidTo, ProductIDs: b.ProductIDs,
	}
}

func (h *Handlers) AdminListCoupons(w http.ResponseWriter, r *http.Request) {
	coupons, err := h.coupons.List(r.Context())
	if err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not load coupons")
		return
	}
	response.OK(w, map[string]any{"coupons": coupons})
}

func (h *Handlers) AdminCreateCoupon(w http.ResponseWriter, r *http.Request) {
	var b couponBody
	if !decodeJSON(w, r, &b) {
		return
	}
	if fields := b.validate(); fields != nil {
		response.FailFields(w, fields)
		return
	}
	c, err := h.coupons.Create(r.Context(), b.toInput())
	if err != nil {
		response.Fail(w, http.StatusConflict, response.CodeConflict, "Could not create coupon (code may already exist)")
		return
	}
	response.Created(w, c)
}

func (h *Handlers) AdminUpdateCoupon(w http.ResponseWriter, r *http.Request) {
	var b couponBody
	if !decodeJSON(w, r, &b) {
		return
	}
	if fields := b.validate(); fields != nil {
		response.FailFields(w, fields)
		return
	}
	c, err := h.coupons.Update(r.Context(), chi.URLParam(r, "id"), b.toInput())
	if errors.Is(err, services.ErrNotFound) {
		response.Fail(w, http.StatusNotFound, response.CodeNotFound, "Coupon not found")
		return
	}
	if err != nil {
		response.Fail(w, http.StatusConflict, response.CodeConflict, "Could not update coupon")
		return
	}
	response.OK(w, c)
}

func (h *Handlers) AdminDeleteCoupon(w http.ResponseWriter, r *http.Request) {
	err := h.coupons.Delete(r.Context(), chi.URLParam(r, "id"))
	if errors.Is(err, services.ErrNotFound) {
		response.Fail(w, http.StatusNotFound, response.CodeNotFound, "Coupon not found")
		return
	}
	if err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not delete coupon")
		return
	}
	response.OK(w, map[string]bool{"deleted": true})
}
