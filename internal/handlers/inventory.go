package handlers

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/innzout/ootybites/internal/services"
	"github.com/innzout/ootybites/internal/validators"
	"github.com/innzout/ootybites/pkg/response"
)

// ---------------- Vendors (suppliers we buy from) ----------------

type vendorBody struct {
	Name     string  `json:"name"`
	Phone    *string `json:"phone"`
	Location string  `json:"location"`
	Notes    *string `json:"notes"`
	IsActive *bool   `json:"is_active"`
}

func (b vendorBody) toInput() services.VendorInput {
	return services.VendorInput{
		Name:     b.Name,
		Phone:    trimPtr(b.Phone),
		Location: b.Location,
		Notes:    trimPtr(b.Notes),
		IsActive: deref(b.IsActive, true),
	}
}

func (h *Handlers) AdminListVendors(w http.ResponseWriter, r *http.Request) {
	vendors, err := h.vendors.List(r.Context())
	if err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not load vendors")
		return
	}
	response.OK(w, map[string]any{"vendors": vendors})
}

func (h *Handlers) AdminGetVendor(w http.ResponseWriter, r *http.Request) {
	v, err := h.vendors.Get(r.Context(), chi.URLParam(r, "id"))
	if errors.Is(err, services.ErrNotFound) {
		response.Fail(w, http.StatusNotFound, response.CodeNotFound, "Vendor not found")
		return
	}
	if err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not load vendor")
		return
	}
	response.OK(w, v)
}

func (h *Handlers) AdminCreateVendor(w http.ResponseWriter, r *http.Request) {
	var b vendorBody
	if !decodeJSON(w, r, &b) {
		return
	}
	if fields := validators.Fields(map[string]string{"name": validators.Required(b.Name)}); fields != nil {
		response.FailFields(w, fields)
		return
	}
	v, err := h.vendors.Create(r.Context(), b.toInput())
	if err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not create vendor")
		return
	}
	response.Created(w, v)
}

func (h *Handlers) AdminUpdateVendor(w http.ResponseWriter, r *http.Request) {
	var b vendorBody
	if !decodeJSON(w, r, &b) {
		return
	}
	if fields := validators.Fields(map[string]string{"name": validators.Required(b.Name)}); fields != nil {
		response.FailFields(w, fields)
		return
	}
	v, err := h.vendors.Update(r.Context(), chi.URLParam(r, "id"), b.toInput())
	if errors.Is(err, services.ErrNotFound) {
		response.Fail(w, http.StatusNotFound, response.CodeNotFound, "Vendor not found")
		return
	}
	if err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not update vendor")
		return
	}
	response.OK(w, v)
}

func (h *Handlers) AdminDeleteVendor(w http.ResponseWriter, r *http.Request) {
	err := h.vendors.Delete(r.Context(), chi.URLParam(r, "id"))
	if errors.Is(err, services.ErrNotFound) {
		response.Fail(w, http.StatusNotFound, response.CodeNotFound, "Vendor not found")
		return
	}
	if err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not delete vendor")
		return
	}
	response.OK(w, map[string]bool{"deleted": true})
}

// ---------------- Inventory ----------------

func (h *Handlers) AdminStockOverview(w http.ResponseWriter, r *http.Request) {
	rows, err := h.inventory.Overview(r.Context())
	if err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not load inventory")
		return
	}
	response.OK(w, map[string]any{"stock": rows})
}

func (h *Handlers) AdminStockMovements(w http.ResponseWriter, r *http.Request) {
	moves, err := h.inventory.Movements(r.Context(), chi.URLParam(r, "variantId"))
	if err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not load movements")
		return
	}
	response.OK(w, map[string]any{"movements": moves})
}

type receiveBody struct {
	VariantID string   `json:"variant_id"`
	VendorID  *string  `json:"vendor_id"`
	Qty       int      `json:"qty"`
	UnitCost  *float64 `json:"unit_cost"`
	Note      string   `json:"note"`
}

// AdminReceiveStock records a purchase (stock in) from a vendor.
func (h *Handlers) AdminReceiveStock(w http.ResponseWriter, r *http.Request) {
	var b receiveBody
	if !decodeJSON(w, r, &b) {
		return
	}
	if b.VariantID == "" || b.Qty <= 0 {
		response.FailFields(w, map[string]string{"qty": "Enter a variant and a quantity of at least 1"})
		return
	}
	err := h.inventory.ReceiveStock(r.Context(), b.VariantID, b.VendorID, b.Qty, b.UnitCost, b.Note)
	if h.writeStockResult(w, err) {
		return
	}
}

type adjustBody struct {
	VariantID string `json:"variant_id"`
	Delta     int    `json:"delta"`
	Note      string `json:"note"`
}

// AdminAdjustStock applies a manual +/- stock correction.
func (h *Handlers) AdminAdjustStock(w http.ResponseWriter, r *http.Request) {
	var b adjustBody
	if !decodeJSON(w, r, &b) {
		return
	}
	if b.VariantID == "" {
		response.FailFields(w, map[string]string{"variant_id": "Variant is required"})
		return
	}
	err := h.inventory.AdjustStock(r.Context(), b.VariantID, b.Delta, b.Note)
	if h.writeStockResult(w, err) {
		return
	}
}

// writeStockResult maps inventory errors to responses. Returns true once written.
func (h *Handlers) writeStockResult(w http.ResponseWriter, err error) bool {
	var cartErr *services.CartError
	switch {
	case errors.Is(err, services.ErrNotFound):
		response.Fail(w, http.StatusNotFound, response.CodeNotFound, "Variant not found")
	case errors.As(err, &cartErr):
		response.Fail(w, http.StatusUnprocessableEntity, response.CodeValidation, cartErr.Message)
	case err != nil:
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not update stock")
	default:
		response.OK(w, map[string]bool{"ok": true})
	}
	return true
}
