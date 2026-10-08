package handlers

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/innzout/ootybites/internal/middleware"
	"github.com/innzout/ootybites/internal/services"
	"github.com/innzout/ootybites/internal/validators"
	"github.com/innzout/ootybites/pkg/response"
)

type addressBody struct {
	Name      string   `json:"name"`
	Phone     string   `json:"phone"`
	Line1     string   `json:"line1"`
	Line2     *string  `json:"line2"`
	City      string   `json:"city"`
	State     string   `json:"state"`
	Pincode   string   `json:"pincode"`
	Lat       *float64 `json:"lat"`
	Lng       *float64 `json:"lng"`
	IsDefault bool     `json:"is_default"`
}

func (b addressBody) validate() map[string]string {
	return validators.Fields(map[string]string{
		"name":    validators.Required(b.Name),
		"phone":   validators.PhoneIN(b.Phone),
		"line1":   validators.Required(b.Line1),
		"city":    validators.Required(b.City),
		"state":   validators.Required(b.State),
		"pincode": validators.Pincode(b.Pincode),
	})
}

func (b addressBody) toInput() services.AddressInput {
	return services.AddressInput{
		Name: b.Name, Phone: b.Phone, Line1: b.Line1, Line2: trimPtr(b.Line2),
		City: b.City, State: b.State, Pincode: b.Pincode, Lat: b.Lat, Lng: b.Lng, IsDefault: b.IsDefault,
	}
}

// ListAddresses returns the signed-in customer's saved addresses.
func (h *Handlers) ListAddresses(w http.ResponseWriter, r *http.Request) {
	addrs, err := h.addresses.List(r.Context(), middleware.SubjectFrom(r.Context()))
	if err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not load addresses")
		return
	}
	response.OK(w, map[string]any{"addresses": addrs})
}

// CreateAddress adds an address to the customer's book.
func (h *Handlers) CreateAddress(w http.ResponseWriter, r *http.Request) {
	var b addressBody
	if !decodeJSON(w, r, &b) {
		return
	}
	if fields := b.validate(); fields != nil {
		response.FailFields(w, fields)
		return
	}
	a, err := h.addresses.Create(r.Context(), middleware.SubjectFrom(r.Context()), b.toInput())
	if err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not save address")
		return
	}
	response.Created(w, a)
}

// UpdateAddress edits one of the customer's addresses.
func (h *Handlers) UpdateAddress(w http.ResponseWriter, r *http.Request) {
	var b addressBody
	if !decodeJSON(w, r, &b) {
		return
	}
	if fields := b.validate(); fields != nil {
		response.FailFields(w, fields)
		return
	}
	a, err := h.addresses.Update(r.Context(), middleware.SubjectFrom(r.Context()), chi.URLParam(r, "id"), b.toInput())
	if errors.Is(err, services.ErrNotFound) {
		response.Fail(w, http.StatusNotFound, response.CodeNotFound, "Address not found")
		return
	}
	if err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not update address")
		return
	}
	response.OK(w, a)
}

// DeleteAddress removes one of the customer's addresses.
func (h *Handlers) DeleteAddress(w http.ResponseWriter, r *http.Request) {
	err := h.addresses.Delete(r.Context(), middleware.SubjectFrom(r.Context()), chi.URLParam(r, "id"))
	if errors.Is(err, services.ErrNotFound) {
		response.Fail(w, http.StatusNotFound, response.CodeNotFound, "Address not found")
		return
	}
	if err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not delete address")
		return
	}
	response.OK(w, map[string]bool{"deleted": true})
}
