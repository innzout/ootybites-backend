package handlers

import (
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/innzout/ootybites/internal/services"
	"github.com/innzout/ootybites/internal/validators"
	"github.com/innzout/ootybites/pkg/response"
)

// ListBanners (public) returns the active storefront banners in display order.
func (h *Handlers) ListBanners(w http.ResponseWriter, r *http.Request) {
	banners, err := h.banners.ListActive(r.Context())
	if err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not load banners")
		return
	}
	response.OK(w, map[string]any{"banners": banners})
}

// AdminListBanners returns every banner for management.
func (h *Handlers) AdminListBanners(w http.ResponseWriter, r *http.Request) {
	banners, err := h.banners.ListAll(r.Context())
	if err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not load banners")
		return
	}
	response.OK(w, map[string]any{"banners": banners})
}

type bannerBody struct {
	Title     *string `json:"title"`
	ImageURL  string  `json:"image_url"`
	LinkURL   *string `json:"link_url"`
	SortOrder int     `json:"sort_order"`
	IsActive  *bool   `json:"is_active"`
}

func (b bannerBody) validate() map[string]string {
	link := ""
	if b.LinkURL != nil {
		link = *b.LinkURL
	}
	return validators.Fields(map[string]string{
		"image_url": validators.Required(b.ImageURL),
		"link_url":  validators.LinkURL(link),
	})
}

func (b bannerBody) toInput() services.BannerInput {
	return services.BannerInput{
		Title:     trimPtr(b.Title),
		ImageURL:  strings.TrimSpace(b.ImageURL),
		LinkURL:   trimPtr(b.LinkURL),
		SortOrder: b.SortOrder,
		IsActive:  deref(b.IsActive, true),
	}
}

func (h *Handlers) AdminGetBanner(w http.ResponseWriter, r *http.Request) {
	b, err := h.banners.Get(r.Context(), chi.URLParam(r, "id"))
	if errors.Is(err, services.ErrNotFound) {
		response.Fail(w, http.StatusNotFound, response.CodeNotFound, "Banner not found")
		return
	}
	if err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not load banner")
		return
	}
	response.OK(w, b)
}

func (h *Handlers) AdminCreateBanner(w http.ResponseWriter, r *http.Request) {
	var b bannerBody
	if !decodeJSON(w, r, &b) {
		return
	}
	if fields := b.validate(); fields != nil {
		response.FailFields(w, fields)
		return
	}
	banner, err := h.banners.Create(r.Context(), b.toInput())
	if err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not create banner")
		return
	}
	response.Created(w, banner)
}

func (h *Handlers) AdminUpdateBanner(w http.ResponseWriter, r *http.Request) {
	var b bannerBody
	if !decodeJSON(w, r, &b) {
		return
	}
	if fields := b.validate(); fields != nil {
		response.FailFields(w, fields)
		return
	}
	banner, err := h.banners.Update(r.Context(), chi.URLParam(r, "id"), b.toInput())
	if errors.Is(err, services.ErrNotFound) {
		response.Fail(w, http.StatusNotFound, response.CodeNotFound, "Banner not found")
		return
	}
	if err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not update banner")
		return
	}
	response.OK(w, banner)
}

func (h *Handlers) AdminDeleteBanner(w http.ResponseWriter, r *http.Request) {
	err := h.banners.Delete(r.Context(), chi.URLParam(r, "id"))
	if errors.Is(err, services.ErrNotFound) {
		response.Fail(w, http.StatusNotFound, response.CodeNotFound, "Banner not found")
		return
	}
	if err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not delete banner")
		return
	}
	response.OK(w, map[string]bool{"deleted": true})
}

// trimPtr trims a *string, returning nil for empty/blank values.
func trimPtr(p *string) *string {
	if p == nil {
		return nil
	}
	v := strings.TrimSpace(*p)
	if v == "" {
		return nil
	}
	return &v
}
