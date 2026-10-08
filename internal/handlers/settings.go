package handlers

import (
	"net/http"

	"github.com/innzout/ootybites/internal/models"
	"github.com/innzout/ootybites/pkg/response"
)

// PublicSettings (public) exposes the store config the storefront needs
// (profile, contact, delivery copy). All fields are safe to expose.
func (h *Handlers) PublicSettings(w http.ResponseWriter, r *http.Request) {
	s, err := h.settings.Get(r.Context())
	if err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not load settings")
		return
	}
	response.OK(w, s)
}

// AdminGetSettings returns the settings for the admin editor (any admin role).
func (h *Handlers) AdminGetSettings(w http.ResponseWriter, r *http.Request) {
	s, err := h.settings.Get(r.Context())
	if err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not load settings")
		return
	}
	response.OK(w, s)
}

// AdminUpdateSettings writes the settings (super-admin only, route-gated).
func (h *Handlers) AdminUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var in models.Settings
	if !decodeJSON(w, r, &in) {
		return
	}
	s, err := h.settings.Update(r.Context(), &in)
	if err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not save settings")
		return
	}
	response.OK(w, s)
}
