package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/innzout/ootybites/internal/middleware"
	"github.com/innzout/ootybites/internal/services"
	"github.com/innzout/ootybites/pkg/response"
)

// Reviews are readable by signed-in customers and admins only. Every handler
// here lives inside an authenticated route group - there is deliberately no
// anonymous read path, which is also why the storefront emits no public
// AggregateRating structured data for them.

type reviewBody struct {
	ProductID string `json:"product_id"`
	Rating    int    `json:"rating"`
	Body      string `json:"body"`
}

// ListProductReviews returns a product's reviews plus the average and count,
// and - for the caller - whether they may review it and what they already said.
func (h *Handlers) ListProductReviews(w http.ResponseWriter, r *http.Request) {
	productID := r.URL.Query().Get("product_id")
	if productID == "" {
		response.Fail(w, http.StatusBadRequest, response.CodeValidation, "product_id is required")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	customerID := middleware.SubjectFrom(r.Context())

	list, avg, count, err := h.reviews.ListForProduct(r.Context(), productID, limit)
	if err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not load reviews")
		return
	}
	canReview, err := h.reviews.CanReview(r.Context(), customerID, productID)
	if err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not load reviews")
		return
	}
	mine, err := h.reviews.MineFor(r.Context(), customerID, productID)
	if err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not load reviews")
		return
	}

	response.OK(w, map[string]any{
		"reviews":    list,
		"average":    avg,
		"count":      count,
		"can_review": canReview,
		"mine":       mine,
	})
}

// SaveReview creates or updates the caller's review of a product.
func (h *Handlers) SaveReview(w http.ResponseWriter, r *http.Request) {
	var b reviewBody
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		response.Fail(w, http.StatusBadRequest, response.CodeValidation, "Invalid request body")
		return
	}
	fields := map[string]string{}
	if b.ProductID == "" {
		fields["product_id"] = "This field is required"
	}
	if b.Rating < 1 || b.Rating > 5 {
		fields["rating"] = "Choose a rating from 1 to 5"
	}
	if len(fields) > 0 {
		response.FailFields(w, fields)
		return
	}

	customerID := middleware.SubjectFrom(r.Context())
	review, err := h.reviews.Upsert(r.Context(), customerID, b.ProductID, b.Rating, b.Body)
	if errors.Is(err, services.ErrNotPurchased) {
		// 403, not 404: the product exists, the caller just has not earned the
		// right to review it.
		response.Fail(w, http.StatusForbidden, response.CodeForbidden,
			"You can review this only after an order containing it has been delivered")
		return
	}
	if err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not save your review")
		return
	}
	response.OK(w, review)
}

// DeleteReview removes the caller's own review.
func (h *Handlers) DeleteReview(w http.ResponseWriter, r *http.Request) {
	customerID := middleware.SubjectFrom(r.Context())
	err := h.reviews.DeleteOwn(r.Context(), customerID, chi.URLParam(r, "id"))
	if errors.Is(err, services.ErrNotFound) {
		// Covers both "no such review" and "not yours" - deliberately
		// indistinguishable, so this cannot be used to probe other customers'
		// review ids.
		response.Fail(w, http.StatusNotFound, response.CodeNotFound, "Review not found")
		return
	}
	if err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not delete your review")
		return
	}
	response.OK(w, map[string]any{"deleted": true})
}

// ---------------- Admin ----------------

// AdminListReviews returns every review for the moderation screen.
func (h *Handlers) AdminListReviews(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	list, err := h.reviews.ListAll(r.Context(), limit)
	if err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not load reviews")
		return
	}
	response.OK(w, map[string]any{"reviews": list})
}

// AdminDeleteReview removes any review.
func (h *Handlers) AdminDeleteReview(w http.ResponseWriter, r *http.Request) {
	err := h.reviews.AdminDelete(r.Context(), chi.URLParam(r, "id"))
	if errors.Is(err, services.ErrNotFound) {
		response.Fail(w, http.StatusNotFound, response.CodeNotFound, "Review not found")
		return
	}
	if err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not delete review")
		return
	}
	response.OK(w, map[string]any{"deleted": true})
}
