package handlers

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/innzout/ootybites/internal/services"
	"github.com/innzout/ootybites/pkg/response"
)

// PublicGetPage (public) returns a published content page by slug.
func (h *Handlers) PublicGetPage(w http.ResponseWriter, r *http.Request) {
	p, err := h.pages.GetPublished(r.Context(), chi.URLParam(r, "slug"))
	if errors.Is(err, services.ErrNotFound) {
		response.Fail(w, http.StatusNotFound, response.CodeNotFound, "Page not found")
		return
	}
	if err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not load page")
		return
	}
	response.OK(w, p)
}

type pageBody struct {
	Slug        string `json:"slug"`
	Title       string `json:"title"`
	Body        string `json:"body"`
	IsPublished *bool  `json:"is_published"`
}

func (b pageBody) toInput() services.PageInput {
	return services.PageInput{
		Slug:        b.Slug,
		Title:       b.Title,
		Body:        b.Body,
		IsPublished: deref(b.IsPublished, true),
	}
}

func (h *Handlers) AdminListPages(w http.ResponseWriter, r *http.Request) {
	pages, err := h.pages.List(r.Context())
	if err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not load pages")
		return
	}
	response.OK(w, map[string]any{"pages": pages})
}

func (h *Handlers) AdminGetPage(w http.ResponseWriter, r *http.Request) {
	p, err := h.pages.GetByID(r.Context(), chi.URLParam(r, "id"))
	if errors.Is(err, services.ErrNotFound) {
		response.Fail(w, http.StatusNotFound, response.CodeNotFound, "Page not found")
		return
	}
	if err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not load page")
		return
	}
	response.OK(w, p)
}

func (h *Handlers) AdminCreatePage(w http.ResponseWriter, r *http.Request) {
	var b pageBody
	if !decodeJSON(w, r, &b) {
		return
	}
	p, fields, err := h.pages.Create(r.Context(), b.toInput())
	if fields != nil {
		response.FailFields(w, fields)
		return
	}
	if h.writePageResult(w, err) {
		return
	}
	response.Created(w, p)
}

func (h *Handlers) AdminUpdatePage(w http.ResponseWriter, r *http.Request) {
	var b pageBody
	if !decodeJSON(w, r, &b) {
		return
	}
	p, fields, err := h.pages.Update(r.Context(), chi.URLParam(r, "id"), b.toInput())
	if fields != nil {
		response.FailFields(w, fields)
		return
	}
	if h.writePageResult(w, err) {
		return
	}
	response.OK(w, p)
}

func (h *Handlers) AdminDeletePage(w http.ResponseWriter, r *http.Request) {
	err := h.pages.Delete(r.Context(), chi.URLParam(r, "id"))
	if h.writePageResult(w, err) {
		return
	}
	response.OK(w, map[string]bool{"deleted": true})
}

// writePageResult maps page errors to responses. Returns true once written.
func (h *Handlers) writePageResult(w http.ResponseWriter, err error) bool {
	var conflict *services.CartError
	switch {
	case err == nil:
		return false
	case errors.As(err, &conflict):
		response.Fail(w, http.StatusConflict, response.CodeConflict, conflict.Message)
	case errors.Is(err, services.ErrNotFound):
		response.Fail(w, http.StatusNotFound, response.CodeNotFound, "Page not found")
	default:
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not complete request")
	}
	return true
}
