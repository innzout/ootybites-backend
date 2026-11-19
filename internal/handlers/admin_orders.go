package handlers

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/innzout/ootybites/internal/middleware"
	"github.com/innzout/ootybites/internal/models"
	"github.com/innzout/ootybites/internal/services"
	"github.com/innzout/ootybites/internal/validators"
	"github.com/innzout/ootybites/pkg/response"
)

// AdminListOrders serves the admin tracking screen: filter by status/month/range,
// sort, paginate. Default is status=placed oldest-first (surfaced by the client).
// maxSearchLen bounds free-text search parameters.
const maxSearchLen = 100

func (h *Handlers) AdminListOrders(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	limit, _ := strconv.Atoi(q.Get("limit"))
	// A malformed date used to reach Postgres as a failed cast and come back
	// as a 500. Reject it here with a field error instead.
	fields := map[string]string{}
	for _, k := range []string{"from", "to"} {
		if v := q.Get(k); v != "" {
			if _, err := time.Parse("2006-01-02", v); err != nil {
				fields[k] = "Use a date in YYYY-MM-DD format"
			}
		}
	}
	if v := q.Get("month"); v != "" {
		if _, err := time.Parse("2006-01", v); err != nil {
			fields["month"] = "Use a month in YYYY-MM format"
		}
	}
	if len(q.Get("q")) > maxSearchLen {
		fields["q"] = "Search is too long"
	}
	if len(fields) > 0 {
		response.FailFields(w, fields)
		return
	}
	orders, total, err := h.adminOrders.List(r.Context(), services.OrderFilter{
		Q:      q.Get("q"),
		Status: q.Get("status"),
		Month:  q.Get("month"),
		From:   q.Get("from"),
		To:     q.Get("to"),
		Sort:   q.Get("sort"),
		Order:  q.Get("order"),
		Page:   page,
		Limit:  limit,
	})
	if err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not load orders")
		return
	}
	response.OK(w, map[string]any{"orders": orders, "total": total})
}

func (h *Handlers) AdminGetOrder(w http.ResponseWriter, r *http.Request) {
	o, err := h.adminOrders.Get(r.Context(), chi.URLParam(r, "id"))
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

type statusBody struct {
	Status string `json:"status"`
	Note   string `json:"note"`
}

// AdminUpdateOrderStatus advances the order through the state machine (reason
// required to cancel; cancel restores stock).
func (h *Handlers) AdminUpdateOrderStatus(w http.ResponseWriter, r *http.Request) {
	var b statusBody
	if !decodeJSON(w, r, &b) {
		return
	}
	adminID := middleware.SubjectFrom(r.Context())
	o, err := h.adminOrders.UpdateStatus(r.Context(), adminID, chi.URLParam(r, "id"), models.OrderStatus(b.Status), b.Note)
	if h.writeOrderResult(w, o, err) {
		return
	}
}

// AdminUpdateOrderAddress edits the shipping snapshot (placed/reached_dealer only).
func (h *Handlers) AdminUpdateOrderAddress(w http.ResponseWriter, r *http.Request) {
	var s services.ShippingInput
	if !decodeJSON(w, r, &s) {
		return
	}
	if fields := validators.Fields(map[string]string{
		"name":    validators.Required(s.Name),
		"phone":   validators.PhoneIN(s.Phone),
		"line1":   validators.Required(s.Line1),
		"city":    validators.Required(s.City),
		"state":   validators.Required(s.State),
		"pincode": validators.Pincode(s.Pincode),
	}); fields != nil {
		response.FailFields(w, fields)
		return
	}
	adminID := middleware.SubjectFrom(r.Context())
	o, err := h.adminOrders.UpdateAddress(r.Context(), adminID, chi.URLParam(r, "id"), s)
	if h.writeOrderResult(w, o, err) {
		return
	}
}

type noteBody struct {
	Note string `json:"note"`
}

func (h *Handlers) AdminAddOrderNote(w http.ResponseWriter, r *http.Request) {
	var b noteBody
	if !decodeJSON(w, r, &b) {
		return
	}
	if fields := validators.Fields(map[string]string{"note": validators.Required(b.Note)}); fields != nil {
		response.FailFields(w, fields)
		return
	}
	adminID := middleware.SubjectFrom(r.Context())
	o, err := h.adminOrders.AddNote(r.Context(), adminID, chi.URLParam(r, "id"), b.Note)
	if h.writeOrderResult(w, o, err) {
		return
	}
}

// AdminOrderInvoice returns the full order (items + address) for a printable COD
// receipt; the admin UI renders/prints it.
func (h *Handlers) AdminOrderInvoice(w http.ResponseWriter, r *http.Request) {
	o, err := h.adminOrders.Get(r.Context(), chi.URLParam(r, "id"))
	if errors.Is(err, services.ErrNotFound) {
		response.Fail(w, http.StatusNotFound, response.CodeNotFound, "Order not found")
		return
	}
	if err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not load invoice")
		return
	}
	response.OK(w, o)
}

func (h *Handlers) AdminDashboardStats(w http.ResponseWriter, r *http.Request) {
	// Date range (YYYY-MM-DD, inclusive). When neither is supplied, default to
	// the current day so the dashboard opens on "today".
	q := r.URL.Query()
	from, to := q.Get("from"), q.Get("to")
	if from == "" && to == "" {
		today := time.Now().Format("2006-01-02")
		from, to = today, today
	}
	st, err := h.adminOrders.Stats(r.Context(), from, to)
	if err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not load stats")
		return
	}
	response.OK(w, st)
}

// writeOrderResult centralises the error→response mapping shared by the order
// mutation handlers. Returns true once it has written a response.
func (h *Handlers) writeOrderResult(w http.ResponseWriter, o *models.Order, err error) bool {
	var stateErr *services.OrderStateError
	switch {
	case errors.Is(err, services.ErrNotFound):
		response.Fail(w, http.StatusNotFound, response.CodeNotFound, "Order not found")
	case errors.As(err, &stateErr):
		response.Fail(w, http.StatusConflict, response.CodeConflict, stateErr.Message)
	case err != nil:
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not update order")
	default:
		response.OK(w, o)
	}
	return true
}

// AdminOrdersMap returns pinned orders for the admin map view.
//
// Separate from the order list on purpose: the list is paginated for a table,
// whereas a map needs every pin in the filtered range at once, and needs far
// fewer columns per row.
func (h *Handlers) AdminOrdersMap(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	points, err := h.adminOrders.MapPoints(r.Context(), q.Get("status"), q.Get("from"), q.Get("to"))
	if err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not load the orders map")
		return
	}
	response.OK(w, map[string]any{"points": points})
}

// scopedOrderList serves a dealer's or customer's own order list through the
// same filtered, paginated query the admin list uses. scope carries the owner
// id taken from the token; the request may narrow the list but never widen it.
func (h *Handlers) scopedOrderList(w http.ResponseWriter, r *http.Request, scope services.OrderFilter) {
	q := r.URL.Query()
	if len(q.Get("q")) > maxSearchLen {
		response.FailFields(w, map[string]string{"q": "Search is too long"})
		return
	}
	page, _ := strconv.Atoi(q.Get("page"))
	limit, _ := strconv.Atoi(q.Get("limit"))
	scope.Q = q.Get("q")
	scope.Status = q.Get("status")
	scope.Sort = q.Get("sort")
	scope.Order = q.Get("order")
	scope.Page = page
	scope.Limit = limit
	orders, total, err := h.adminOrders.List(r.Context(), scope)
	if err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not load orders")
		return
	}
	response.OK(w, map[string]any{"orders": orders, "total": total})
}
