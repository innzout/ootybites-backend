package handlers

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/innzout/ootybites/internal/services"
	"github.com/innzout/ootybites/pkg/response"
)

// ListCategories (public) returns active categories for the storefront filter.
func (h *Handlers) ListCategories(w http.ResponseWriter, r *http.Request) {
	cats, err := h.categories.ListActive(r.Context())
	if err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not load categories")
		return
	}
	response.OK(w, map[string]any{"categories": cats})
}

type categoryBody struct {
	Name      string `json:"name"`
	Slug      string `json:"slug"`
	SortOrder int    `json:"sort_order"`
	IsActive  *bool  `json:"is_active"`
}

func (b categoryBody) toInput() services.CategoryInput {
	return services.CategoryInput{
		Name:      b.Name,
		Slug:      b.Slug,
		SortOrder: b.SortOrder,
		IsActive:  deref(b.IsActive, true),
	}
}

func (h *Handlers) AdminListCategories(w http.ResponseWriter, r *http.Request) {
	cats, err := h.categories.List(r.Context())
	if err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not load categories")
		return
	}
	response.OK(w, map[string]any{"categories": cats})
}

func (h *Handlers) AdminGetCategory(w http.ResponseWriter, r *http.Request) {
	c, err := h.categories.Get(r.Context(), chi.URLParam(r, "id"))
	if errors.Is(err, services.ErrNotFound) {
		response.Fail(w, http.StatusNotFound, response.CodeNotFound, "Category not found")
		return
	}
	if err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not load category")
		return
	}
	response.OK(w, c)
}

func (h *Handlers) AdminCreateCategory(w http.ResponseWriter, r *http.Request) {
	var b categoryBody
	if !decodeJSON(w, r, &b) {
		return
	}
	c, fields, err := h.categories.Create(r.Context(), b.toInput())
	if fields != nil {
		response.FailFields(w, fields)
		return
	}
	if h.writeCategoryResult(w, err) {
		return
	}
	response.Created(w, c)
}

func (h *Handlers) AdminUpdateCategory(w http.ResponseWriter, r *http.Request) {
	var b categoryBody
	if !decodeJSON(w, r, &b) {
		return
	}
	c, fields, err := h.categories.Update(r.Context(), chi.URLParam(r, "id"), b.toInput())
	if fields != nil {
		response.FailFields(w, fields)
		return
	}
	if h.writeCategoryResult(w, err) {
		return
	}
	response.OK(w, c)
}

func (h *Handlers) AdminDeleteCategory(w http.ResponseWriter, r *http.Request) {
	err := h.categories.Delete(r.Context(), chi.URLParam(r, "id"))
	if h.writeCategoryResult(w, err) {
		return
	}
	response.OK(w, map[string]bool{"deleted": true})
}

func (h *Handlers) writeCategoryResult(w http.ResponseWriter, err error) bool {
	var conflict *services.CartError
	switch {
	case err == nil:
		return false
	case errors.As(err, &conflict):
		response.Fail(w, http.StatusConflict, response.CodeConflict, conflict.Message)
	case errors.Is(err, services.ErrNotFound):
		response.Fail(w, http.StatusNotFound, response.CodeNotFound, "Category not found")
	default:
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not complete request")
	}
	return true
}
