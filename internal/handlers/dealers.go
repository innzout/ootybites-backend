package handlers

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/innzout/ootybites/internal/middleware"
	"github.com/innzout/ootybites/internal/models"
	"github.com/innzout/ootybites/internal/services"
	"github.com/innzout/ootybites/internal/token"
	"github.com/innzout/ootybites/internal/validators"
	"github.com/innzout/ootybites/pkg/response"
)

// ---------------- Admin: dealer CRUD ----------------

type dealerBody struct {
	Name     string  `json:"name"`
	Mobile   string  `json:"mobile"`
	Address  *string `json:"address"`
	Username string  `json:"username"`
	Password string  `json:"password"`
	IsActive *bool   `json:"is_active"`
}

func (b dealerBody) validate(requirePassword bool) map[string]string {
	f := map[string]string{
		"name":     validators.Required(b.Name),
		"mobile":   validators.PhoneIN(b.Mobile),
		"username": validators.MinLength(b.Username, 3),
	}
	if requirePassword {
		f["password"] = validators.MinLength(b.Password, 6)
	}
	return validators.Fields(f)
}

func (b dealerBody) toInput() services.DealerInput {
	return services.DealerInput{
		Name:     b.Name,
		Mobile:   b.Mobile,
		Address:  trimPtr(b.Address),
		Username: b.Username,
		Password: b.Password,
		IsActive: deref(b.IsActive, true),
	}
}

func (h *Handlers) AdminListDealers(w http.ResponseWriter, r *http.Request) {
	dealers, err := h.dealers.List(r.Context())
	if err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not load dealers")
		return
	}
	response.OK(w, map[string]any{"dealers": dealers})
}

func (h *Handlers) AdminCreateDealer(w http.ResponseWriter, r *http.Request) {
	var b dealerBody
	if !decodeJSON(w, r, &b) {
		return
	}
	if fields := b.validate(true); fields != nil {
		response.FailFields(w, fields)
		return
	}
	d, err := h.dealers.Create(r.Context(), b.toInput())
	if err != nil {
		response.Fail(w, http.StatusConflict, response.CodeConflict, "Could not create dealer (username may be taken)")
		return
	}
	response.Created(w, d)
}

func (h *Handlers) AdminUpdateDealer(w http.ResponseWriter, r *http.Request) {
	var b dealerBody
	if !decodeJSON(w, r, &b) {
		return
	}
	if fields := b.validate(false); fields != nil {
		response.FailFields(w, fields)
		return
	}
	d, err := h.dealers.Update(r.Context(), chi.URLParam(r, "id"), b.toInput())
	if errors.Is(err, services.ErrNotFound) {
		response.Fail(w, http.StatusNotFound, response.CodeNotFound, "Dealer not found")
		return
	}
	if err != nil {
		response.Fail(w, http.StatusConflict, response.CodeConflict, "Could not update dealer")
		return
	}
	response.OK(w, d)
}

func (h *Handlers) AdminDeleteDealer(w http.ResponseWriter, r *http.Request) {
	err := h.dealers.Delete(r.Context(), chi.URLParam(r, "id"))
	if errors.Is(err, services.ErrNotFound) {
		response.Fail(w, http.StatusNotFound, response.CodeNotFound, "Dealer not found")
		return
	}
	if err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not delete dealer")
		return
	}
	response.OK(w, map[string]bool{"deleted": true})
}

type assignBody struct {
	DealerID *string `json:"dealer_id"` // null clears the assignment
}

// AdminAssignOrderDealer maps an order to a dealer (or clears it).
func (h *Handlers) AdminAssignOrderDealer(w http.ResponseWriter, r *http.Request) {
	var b assignBody
	if !decodeJSON(w, r, &b) {
		return
	}
	adminID := middleware.SubjectFrom(r.Context())
	o, err := h.adminOrders.AssignDealer(r.Context(), chi.URLParam(r, "id"), b.DealerID, adminID)
	if h.writeOrderResult(w, o, err) {
		return
	}
}

// ---------------- Dealer portal ----------------

type dealerLoginBody struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (h *Handlers) DealerLogin(w http.ResponseWriter, r *http.Request) {
	var b dealerLoginBody
	if !decodeJSON(w, r, &b) {
		return
	}
	d, err := h.dealers.Authenticate(r.Context(), b.Username, b.Password)
	if err != nil {
		response.Fail(w, http.StatusUnauthorized, response.CodeUnauthorized, "Invalid username or password")
		return
	}
	tok, err := token.IssueDealer(h.cfg.JWTSecret, d.ID, d.Username)
	if err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not sign in")
		return
	}
	response.OK(w, map[string]any{"token": tok, "dealer": d})
}

func (h *Handlers) DealerListOrders(w http.ResponseWriter, r *http.Request) {
	dealerID := middleware.SubjectFrom(r.Context())
	orders, err := h.adminOrders.ListForDealer(r.Context(), dealerID)
	if err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not load orders")
		return
	}
	response.OK(w, map[string]any{"orders": orders})
}

func (h *Handlers) DealerGetOrder(w http.ResponseWriter, r *http.Request) {
	dealerID := middleware.SubjectFrom(r.Context())
	o, err := h.adminOrders.GetForDealer(r.Context(), dealerID, chi.URLParam(r, "id"))
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

func (h *Handlers) DealerUpdateOrderStatus(w http.ResponseWriter, r *http.Request) {
	var b statusBody
	if !decodeJSON(w, r, &b) {
		return
	}
	dealerID := middleware.SubjectFrom(r.Context())
	o, err := h.adminOrders.DealerUpdateStatus(r.Context(), dealerID, chi.URLParam(r, "id"), models.OrderStatus(b.Status), b.Note)
	if h.writeOrderResult(w, o, err) {
		return
	}
}
