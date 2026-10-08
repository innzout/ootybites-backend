package handlers

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/innzout/ootybites/internal/middleware"
	"github.com/innzout/ootybites/internal/services"
	"github.com/innzout/ootybites/pkg/response"
)

// AdminMe returns the currently-authenticated admin (id, username, name, role),
// so the client can tailor the UI (e.g. show the Admins section to super-admins).
func (h *Handlers) AdminMe(w http.ResponseWriter, r *http.Request) {
	id := middleware.SubjectFrom(r.Context())
	m, err := h.auth.GetAdmin(r.Context(), id)
	if errors.Is(err, services.ErrNotFound) {
		response.Fail(w, http.StatusUnauthorized, response.CodeUnauthorized, "Your session has expired. Please sign in again.")
		return
	}
	if err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not load account")
		return
	}
	response.OK(w, m)
}

type adminUserBody struct {
	Username string  `json:"username"`
	Name     *string `json:"name"`
	Password string  `json:"password"`
	Role     string  `json:"role"`
}

func (b adminUserBody) toInput() services.AdminInput {
	return services.AdminInput{
		Username: b.Username,
		Name:     trimPtr(b.Name),
		Password: b.Password,
		Role:     b.Role,
	}
}

func (h *Handlers) AdminListAdmins(w http.ResponseWriter, r *http.Request) {
	admins, err := h.admins.List(r.Context())
	if err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not load admins")
		return
	}
	response.OK(w, map[string]any{"admins": admins})
}

func (h *Handlers) AdminGetAdmin(w http.ResponseWriter, r *http.Request) {
	m, err := h.admins.Get(r.Context(), chi.URLParam(r, "id"))
	if errors.Is(err, services.ErrNotFound) {
		response.Fail(w, http.StatusNotFound, response.CodeNotFound, "Admin not found")
		return
	}
	if err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not load admin")
		return
	}
	response.OK(w, m)
}

func (h *Handlers) AdminCreateAdmin(w http.ResponseWriter, r *http.Request) {
	var b adminUserBody
	if !decodeJSON(w, r, &b) {
		return
	}
	m, fields, err := h.admins.Create(r.Context(), b.toInput())
	if fields != nil {
		response.FailFields(w, fields)
		return
	}
	if h.writeAdminGuard(w, err) {
		return
	}
	response.Created(w, m)
}

func (h *Handlers) AdminUpdateAdmin(w http.ResponseWriter, r *http.Request) {
	var b adminUserBody
	if !decodeJSON(w, r, &b) {
		return
	}
	m, fields, err := h.admins.Update(r.Context(), chi.URLParam(r, "id"), b.toInput())
	if fields != nil {
		response.FailFields(w, fields)
		return
	}
	if h.writeAdminGuard(w, err) {
		return
	}
	response.OK(w, m)
}

func (h *Handlers) AdminDeleteAdmin(w http.ResponseWriter, r *http.Request) {
	current := middleware.SubjectFrom(r.Context())
	err := h.admins.Delete(r.Context(), chi.URLParam(r, "id"), current)
	if h.writeAdminGuard(w, err) {
		return
	}
	response.OK(w, map[string]bool{"deleted": true})
}

// writeAdminGuard maps admin-user errors to responses. Returns true once it has
// written one (including the nil-error success no-op returning false).
func (h *Handlers) writeAdminGuard(w http.ResponseWriter, err error) bool {
	var guard *services.AdminGuardError
	switch {
	case err == nil:
		return false
	case errors.As(err, &guard):
		response.Fail(w, http.StatusConflict, response.CodeConflict, guard.Message)
	case errors.Is(err, services.ErrNotFound):
		response.Fail(w, http.StatusNotFound, response.CodeNotFound, "Admin not found")
	default:
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not complete request")
	}
	return true
}
