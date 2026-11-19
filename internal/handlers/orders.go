package handlers

import (
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/innzout/ootybites/internal/middleware"
	"github.com/innzout/ootybites/internal/services"
	"github.com/innzout/ootybites/internal/validators"
	"github.com/innzout/ootybites/pkg/response"
)

// --- Customer profile ---

// GetMe returns the authenticated customer's profile.
func (h *Handlers) GetMe(w http.ResponseWriter, r *http.Request) {
	id := middleware.SubjectFrom(r.Context())
	c, err := h.auth.GetCustomer(r.Context(), id)
	if errors.Is(err, services.ErrNotFound) {
		response.Fail(w, http.StatusNotFound, response.CodeNotFound, "Customer not found")
		return
	}
	if err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not load profile")
		return
	}
	response.OK(w, c)
}

type updateMeBody struct {
	Name string `json:"name"`
}

// UpdateMe updates the customer's display name.
func (h *Handlers) UpdateMe(w http.ResponseWriter, r *http.Request) {
	var b updateMeBody
	if !decodeJSON(w, r, &b) {
		return
	}
	if fields := validators.Fields(map[string]string{"name": validators.Required(b.Name)}); fields != nil {
		response.FailFields(w, fields)
		return
	}
	c, err := h.auth.UpdateCustomerName(r.Context(), middleware.SubjectFrom(r.Context()), b.Name)
	if err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not update profile")
		return
	}
	response.OK(w, c)
}

// --- Coupons (preview) ---

type validateCouponBody struct {
	Items []services.CartItemInput `json:"items"`
	Code  string                   `json:"code"`
}

// ValidateCoupon previews a coupon against the customer's cart (authoritative
// re-validation happens again at placement).
func (h *Handlers) ValidateCoupon(w http.ResponseWriter, r *http.Request) {
	var b validateCouponBody
	if !decodeJSON(w, r, &b) {
		return
	}
	if fields := validators.Fields(map[string]string{"code": validators.Required(b.Code)}); fields != nil {
		response.FailFields(w, fields)
		return
	}
	res, subtotal, err := h.orders.PreviewCoupon(r.Context(), middleware.SubjectFrom(r.Context()), b.Items, b.Code)
	var cartErr *services.CartError
	if errors.As(err, &cartErr) {
		response.Fail(w, http.StatusConflict, response.CodeConflict, cartErr.Message)
		return
	}
	if err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not validate coupon")
		return
	}
	response.OK(w, map[string]any{
		"valid": res.Valid, "discount": res.Discount, "reason": res.Reason, "subtotal": subtotal,
	})
}

// --- Orders ---

type placeOrderBody struct {
	Items        []services.CartItemInput `json:"items"`
	Code         string                   `json:"coupon_code"`
	Shipping     services.ShippingInput   `json:"shipping"`
	DeliveryNote string                   `json:"delivery_note"`
}

// PlaceOrder validates the cart server-side, recomputes money, and places a COD
// order transactionally.
// maxIdempotencyKey bounds the client-supplied key (a UUID is 36 characters).
const maxIdempotencyKey = 100

func (h *Handlers) PlaceOrder(w http.ResponseWriter, r *http.Request) {
	var b placeOrderBody
	if !decodeJSON(w, r, &b) {
		return
	}
	s := b.Shipping
	if fields := validators.Fields(map[string]string{
		"shipping.name":    validators.Required(s.Name),
		"shipping.phone":   validators.PhoneIN(s.Phone),
		"shipping.line1":   validators.Required(s.Line1),
		"shipping.city":    validators.Required(s.City),
		"shipping.state":   validators.Required(s.State),
		"shipping.pincode": validators.Pincode(s.Pincode),
	}); fields != nil {
		response.FailFields(w, fields)
		return
	}

	// Optional. Bounded so the header cannot be used to store arbitrary data.
	idemKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if len(idemKey) > maxIdempotencyKey {
		response.Fail(w, http.StatusBadRequest, response.CodeValidation, "Idempotency-Key is too long")
		return
	}

	order, err := h.orders.PlaceOrder(r.Context(), middleware.SubjectFrom(r.Context()), b.Items, b.Code, s, b.DeliveryNote, idemKey)
	var cartErr *services.CartError
	switch {
	case errors.Is(err, services.ErrSessionInvalid):
		response.Fail(w, http.StatusUnauthorized, response.CodeUnauthorized, "Your session has expired. Please sign in again.")
		return
	case errors.As(err, &cartErr):
		response.Fail(w, http.StatusConflict, response.CodeConflict, cartErr.Message)
		return
	case err != nil:
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not place order")
		return
	}
	response.Created(w, order)
}

// ListOrders returns a page of the customer's own orders (newest first).
func (h *Handlers) ListOrders(w http.ResponseWriter, r *http.Request) {
	h.scopedOrderList(w, r, services.OrderFilter{CustomerID: middleware.SubjectFrom(r.Context())})
}

// GetOrder returns one of the customer's orders with items.
func (h *Handlers) GetOrder(w http.ResponseWriter, r *http.Request) {
	o, err := h.orders.GetForCustomer(r.Context(), middleware.SubjectFrom(r.Context()), chi.URLParam(r, "id"))
	if errors.Is(err, services.ErrNotFound) {
		response.Fail(w, http.StatusNotFound, response.CodeNotFound, "Order not found")
		return
	}
	if err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not load order")
		return
	}
	response.OK(w, o)
}

// Reorder returns the still-available lines of a past order for "buy again".
func (h *Handlers) Reorder(w http.ResponseWriter, r *http.Request) {
	lines, err := h.orders.Reorder(r.Context(), middleware.SubjectFrom(r.Context()), chi.URLParam(r, "id"))
	if errors.Is(err, services.ErrNotFound) {
		response.Fail(w, http.StatusNotFound, response.CodeNotFound, "Order not found")
		return
	}
	if err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not load order")
		return
	}
	response.OK(w, map[string]any{"items": lines})
}

type cancelBody struct {
	Reason string `json:"reason"`
}

// CancelOrder lets the customer cancel their own order while it is still 'placed'.
func (h *Handlers) CancelOrder(w http.ResponseWriter, r *http.Request) {
	var b cancelBody
	_ = decodeJSONOptional(r, &b) // reason is optional
	o, err := h.orders.Cancel(r.Context(), middleware.SubjectFrom(r.Context()), chi.URLParam(r, "id"), b.Reason)
	var cartErr *services.CartError
	switch {
	case errors.Is(err, services.ErrNotFound):
		response.Fail(w, http.StatusNotFound, response.CodeNotFound, "Order not found")
	case errors.As(err, &cartErr):
		response.Fail(w, http.StatusConflict, response.CodeConflict, cartErr.Message)
	case err != nil:
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not cancel order")
	default:
		response.OK(w, o)
	}
}
