package handlers

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/innzout/ootybites/internal/services"
	"github.com/innzout/ootybites/internal/validators"
	"github.com/innzout/ootybites/pkg/response"
)

type areaBody struct {
	Code     string  `json:"code"`
	Name     string  `json:"name"`
	City     string  `json:"city"`
	Pincode  string  `json:"pincode"`
	DealerID *string `json:"dealer_id"`
	IsActive *bool   `json:"is_active"`
}

func (b areaBody) validate() map[string]string {
	return validators.Fields(map[string]string{
		"code":    validators.Required(b.Code),
		"name":    validators.Required(b.Name),
		"pincode": validators.Pincode(b.Pincode),
	})
}

func (b areaBody) toInput() services.AreaInput {
	city := b.City
	if city == "" {
		city = "Chennai"
	}
	return services.AreaInput{
		Code:     b.Code,
		Name:     b.Name,
		City:     city,
		Pincode:  b.Pincode,
		DealerID: b.DealerID,
		IsActive: deref(b.IsActive, true),
	}
}

func (h *Handlers) AdminListAreas(w http.ResponseWriter, r *http.Request) {
	areas, err := h.areas.List(r.Context())
	if err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not load areas")
		return
	}
	response.OK(w, map[string]any{"areas": areas})
}

func (h *Handlers) AdminCreateArea(w http.ResponseWriter, r *http.Request) {
	var b areaBody
	if !decodeJSON(w, r, &b) {
		return
	}
	if fields := b.validate(); fields != nil {
		response.FailFields(w, fields)
		return
	}
	a, err := h.areas.Create(r.Context(), b.toInput())
	if err != nil {
		response.Fail(w, http.StatusConflict, response.CodeConflict, "Could not create area (code may already exist)")
		return
	}
	response.Created(w, a)
}

func (h *Handlers) AdminUpdateArea(w http.ResponseWriter, r *http.Request) {
	var b areaBody
	if !decodeJSON(w, r, &b) {
		return
	}
	if fields := b.validate(); fields != nil {
		response.FailFields(w, fields)
		return
	}
	a, err := h.areas.Update(r.Context(), chi.URLParam(r, "id"), b.toInput())
	if errors.Is(err, services.ErrNotFound) {
		response.Fail(w, http.StatusNotFound, response.CodeNotFound, "Area not found")
		return
	}
	if err != nil {
		response.Fail(w, http.StatusConflict, response.CodeConflict, "Could not update area")
		return
	}
	response.OK(w, a)
}

func (h *Handlers) AdminDeleteArea(w http.ResponseWriter, r *http.Request) {
	err := h.areas.Delete(r.Context(), chi.URLParam(r, "id"))
	if errors.Is(err, services.ErrNotFound) {
		response.Fail(w, http.StatusNotFound, response.CodeNotFound, "Area not found")
		return
	}
	if err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not delete area")
		return
	}
	response.OK(w, map[string]bool{"deleted": true})
}
