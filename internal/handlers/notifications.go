package handlers

import (
	"net/http"

	"github.com/innzout/ootybites/internal/middleware"
	"github.com/innzout/ootybites/pkg/response"
)

// ListNotifications returns the signed-in customer's notification feed.
func (h *Handlers) ListNotifications(w http.ResponseWriter, r *http.Request) {
	feed, err := h.notifications.ForCustomer(r.Context(), middleware.SubjectFrom(r.Context()))
	if err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not load notifications")
		return
	}
	response.OK(w, feed)
}

// MarkNotificationsRead marks the customer's notifications read.
func (h *Handlers) MarkNotificationsRead(w http.ResponseWriter, r *http.Request) {
	if err := h.notifications.MarkAllReadCustomer(r.Context(), middleware.SubjectFrom(r.Context())); err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not update notifications")
		return
	}
	response.OK(w, map[string]bool{"ok": true})
}

// AdminListNotifications returns the admin notification feed.
func (h *Handlers) AdminListNotifications(w http.ResponseWriter, r *http.Request) {
	feed, err := h.notifications.ForAdmin(r.Context())
	if err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not load notifications")
		return
	}
	response.OK(w, feed)
}

// AdminMarkNotificationsRead marks the admin feed read.
func (h *Handlers) AdminMarkNotificationsRead(w http.ResponseWriter, r *http.Request) {
	if err := h.notifications.MarkAllReadAdmin(r.Context()); err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not update notifications")
		return
	}
	response.OK(w, map[string]bool{"ok": true})
}
