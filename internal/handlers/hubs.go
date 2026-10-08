package handlers

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/innzout/ootybites/internal/services"
	"github.com/innzout/ootybites/internal/validators"
	"github.com/innzout/ootybites/pkg/response"
)

// ---------------- Admin: hubs ----------------

type hubBody struct {
	Name     string  `json:"name"`
	DealerID *string `json:"dealer_id"`
	IsActive *bool   `json:"is_active"`
}

func (b hubBody) toInput() services.HubInput {
	return services.HubInput{Name: b.Name, DealerID: b.DealerID, IsActive: deref(b.IsActive, true)}
}

func (h *Handlers) AdminListHubs(w http.ResponseWriter, r *http.Request) {
	hubs, err := h.hubs.List(r.Context())
	if err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not load hubs")
		return
	}
	response.OK(w, map[string]any{"hubs": hubs})
}

func (h *Handlers) AdminGetHub(w http.ResponseWriter, r *http.Request) {
	hub, err := h.hubs.Get(r.Context(), chi.URLParam(r, "id"))
	if errors.Is(err, services.ErrNotFound) {
		response.Fail(w, http.StatusNotFound, response.CodeNotFound, "Hub not found")
		return
	}
	if err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not load hub")
		return
	}
	response.OK(w, hub)
}

func (h *Handlers) AdminCreateHub(w http.ResponseWriter, r *http.Request) {
	var b hubBody
	if !decodeJSON(w, r, &b) {
		return
	}
	if fields := validators.Fields(map[string]string{"name": validators.Required(b.Name)}); fields != nil {
		response.FailFields(w, fields)
		return
	}
	hub, err := h.hubs.Create(r.Context(), b.toInput())
	if err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not create hub")
		return
	}
	response.Created(w, hub)
}

func (h *Handlers) AdminUpdateHub(w http.ResponseWriter, r *http.Request) {
	var b hubBody
	if !decodeJSON(w, r, &b) {
		return
	}
	if fields := validators.Fields(map[string]string{"name": validators.Required(b.Name)}); fields != nil {
		response.FailFields(w, fields)
		return
	}
	hub, err := h.hubs.Update(r.Context(), chi.URLParam(r, "id"), b.toInput())
	if errors.Is(err, services.ErrNotFound) {
		response.Fail(w, http.StatusNotFound, response.CodeNotFound, "Hub not found")
		return
	}
	if err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not update hub")
		return
	}
	response.OK(w, hub)
}

func (h *Handlers) AdminDeleteHub(w http.ResponseWriter, r *http.Request) {
	err := h.hubs.Delete(r.Context(), chi.URLParam(r, "id"))
	if errors.Is(err, services.ErrNotFound) {
		response.Fail(w, http.StatusNotFound, response.CodeNotFound, "Hub not found")
		return
	}
	if err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not delete hub")
		return
	}
	response.OK(w, map[string]bool{"deleted": true})
}

type assignAreasBody struct {
	AreaIDs []string `json:"area_ids"`
}

// AdminAssignHubAreas sets exactly which areas a hub serves (exclusive).
func (h *Handlers) AdminAssignHubAreas(w http.ResponseWriter, r *http.Request) {
	var b assignAreasBody
	if !decodeJSON(w, r, &b) {
		return
	}
	if err := h.hubs.AssignAreas(r.Context(), chi.URLParam(r, "id"), b.AreaIDs); err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not assign areas")
		return
	}
	response.OK(w, map[string]bool{"ok": true})
}

// AdminHubStock returns a hub's per-variant stock.
func (h *Handlers) AdminHubStock(w http.ResponseWriter, r *http.Request) {
	rows, err := h.hubs.StockOverview(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not load hub stock")
		return
	}
	response.OK(w, map[string]any{"stock": rows})
}

// AdminHubReceiveStock adds stock to a hub (from a vendor).
func (h *Handlers) AdminHubReceiveStock(w http.ResponseWriter, r *http.Request) {
	var b receiveBody
	if !decodeJSON(w, r, &b) {
		return
	}
	if b.VariantID == "" || b.Qty <= 0 {
		response.FailFields(w, map[string]string{"qty": "Select a variant and a quantity of at least 1"})
		return
	}
	err := h.hubs.ReceiveStock(r.Context(), chi.URLParam(r, "id"), b.VariantID, b.VendorID, b.Qty, b.UnitCost, b.Note)
	if h.writeStockResult(w, err) {
		return
	}
}

// AdminHubAdjustStock applies a manual +/- correction to a hub's stock.
func (h *Handlers) AdminHubAdjustStock(w http.ResponseWriter, r *http.Request) {
	var b adjustBody
	if !decodeJSON(w, r, &b) {
		return
	}
	if b.VariantID == "" {
		response.FailFields(w, map[string]string{"variant_id": "Variant is required"})
		return
	}
	err := h.hubs.AdjustStock(r.Context(), chi.URLParam(r, "id"), b.VariantID, b.Delta, b.Note)
	if h.writeStockResult(w, err) {
		return
	}
}

// AdminHubMovements returns a hub's stock ledger.
func (h *Handlers) AdminHubMovements(w http.ResponseWriter, r *http.Request) {
	moves, err := h.hubs.Movements(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not load movements")
		return
	}
	response.OK(w, map[string]any{"movements": moves})
}

// ---------------- Customer: express (24h) availability ----------------

type expressBody struct {
	Pincode string                   `json:"pincode"`
	Items   []services.CartItemInput `json:"items"`
}

// ExpressCheck reports whether the pincode's hub can deliver the cart in 24h.
func (h *Handlers) ExpressCheck(w http.ResponseWriter, r *http.Request) {
	var b expressBody
	if !decodeJSON(w, r, &b) {
		return
	}
	if validators.Pincode(b.Pincode) != "" {
		response.OK(w, map[string]any{"express": false})
		return
	}
	express, hubName, err := h.hubs.ExpressCheck(r.Context(), b.Pincode, b.Items)
	if err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not check availability")
		return
	}
	response.OK(w, map[string]any{"express": express, "hub_name": hubName})
}

// ExpressAreas (public) lists the serviceable areas for the storefront's 24-hour
// delivery section — active areas attached to an active hub.
func (h *Handlers) ExpressAreas(w http.ResponseWriter, r *http.Request) {
	areas, err := h.areas.Serviceable(r.Context())
	if err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not load areas")
		return
	}
	response.OK(w, map[string]any{"areas": areas})
}

// ExpressProducts (public) lists the products available for 24-hour express
// delivery in a given area — i.e. items in stock at the hub serving that area.
func (h *Handlers) ExpressProducts(w http.ResponseWriter, r *http.Request) {
	areaID := r.URL.Query().Get("area_id")
	if areaID == "" {
		response.Fail(w, http.StatusBadRequest, response.CodeValidation, "area_id is required")
		return
	}
	area, err := h.areas.Get(r.Context(), areaID)
	if errors.Is(err, services.ErrNotFound) {
		response.OK(w, map[string]any{"products": []any{}, "hub_name": nil})
		return
	}
	if err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not load area")
		return
	}
	if area.HubID == nil {
		response.OK(w, map[string]any{"products": []any{}, "hub_name": nil})
		return
	}
	products, err := h.hubs.ExpressProducts(r.Context(), *area.HubID)
	if err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not load products")
		return
	}
	response.OK(w, map[string]any{"products": products, "hub_name": area.HubName})
}
