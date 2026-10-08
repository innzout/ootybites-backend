package handlers

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/innzout/ootybites/internal/services"
	"github.com/innzout/ootybites/internal/validators"
	"github.com/innzout/ootybites/pkg/response"
)

// ---------------- Public catalog ----------------

// ListProducts returns a paginated, optionally-searched page of active products.
func (h *Handlers) ListProducts(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	limit, _ := strconv.Atoi(q.Get("limit"))

	products, total, err := h.catalog.ListProducts(r.Context(), services.StorefrontQuery{
		Q:          q.Get("q"),
		CategoryID: q.Get("category"),
		Sort:       q.Get("sort"),
		Page:       page,
		Limit:      limit,
	})
	if err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not load products")
		return
	}
	response.OK(w, map[string]any{"products": products, "total": total})
}

// GetProduct returns a single active product by slug.
func (h *Handlers) GetProduct(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	p, err := h.catalog.GetBySlug(r.Context(), slug)
	if errors.Is(err, services.ErrNotFound) {
		response.Fail(w, http.StatusNotFound, response.CodeNotFound, "Product not found")
		return
	}
	if err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not load product")
		return
	}
	response.OK(w, p)
}

// ---------------- Admin catalog ----------------

func (h *Handlers) AdminListProducts(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	limit, _ := strconv.Atoi(q.Get("limit"))
	products, total, err := h.catalog.AdminListProducts(r.Context(), services.AdminProductQuery{
		Q:      q.Get("q"),
		Active: q.Get("active"),
		Stock:  q.Get("stock"),
		Page:   page,
		Limit:  limit,
		Sort:   q.Get("sort"),
		Order:  q.Get("order"),
	})
	if err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not load products")
		return
	}
	response.OK(w, map[string]any{"products": products, "total": total})
}

func (h *Handlers) AdminGetProduct(w http.ResponseWriter, r *http.Request) {
	p, err := h.catalog.AdminGetProduct(r.Context(), chi.URLParam(r, "id"))
	if errors.Is(err, services.ErrNotFound) {
		response.Fail(w, http.StatusNotFound, response.CodeNotFound, "Product not found")
		return
	}
	if err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not load product")
		return
	}
	response.OK(w, p)
}

type productBody struct {
	Name        string  `json:"name"`
	Slug        string  `json:"slug"`
	Description *string `json:"description"`
	IsActive    *bool   `json:"is_active"`
	CategoryID  *string `json:"category_id"`
}

func (b productBody) validate() map[string]string {
	return validators.Fields(map[string]string{
		"name": validators.Required(b.Name),
		"slug": validators.Slug(b.Slug),
	})
}

func (h *Handlers) AdminCreateProduct(w http.ResponseWriter, r *http.Request) {
	var b productBody
	if !decodeJSON(w, r, &b) {
		return
	}
	if fields := b.validate(); fields != nil {
		response.FailFields(w, fields)
		return
	}
	p, err := h.catalog.CreateProduct(r.Context(), b.Name, b.Slug, b.Description, deref(b.IsActive, true), trimPtr(b.CategoryID))
	if err != nil {
		response.Fail(w, http.StatusConflict, response.CodeConflict, "Could not create product (slug may already exist)")
		return
	}
	response.Created(w, p)
}

func (h *Handlers) AdminUpdateProduct(w http.ResponseWriter, r *http.Request) {
	var b productBody
	if !decodeJSON(w, r, &b) {
		return
	}
	if fields := b.validate(); fields != nil {
		response.FailFields(w, fields)
		return
	}
	p, err := h.catalog.UpdateProduct(r.Context(), chi.URLParam(r, "id"), b.Name, b.Slug, b.Description, deref(b.IsActive, true), trimPtr(b.CategoryID))
	if errors.Is(err, services.ErrNotFound) {
		response.Fail(w, http.StatusNotFound, response.CodeNotFound, "Product not found")
		return
	}
	if err != nil {
		response.Fail(w, http.StatusConflict, response.CodeConflict, "Could not update product")
		return
	}
	response.OK(w, p)
}

func (h *Handlers) AdminDeleteProduct(w http.ResponseWriter, r *http.Request) {
	err := h.catalog.DeleteProduct(r.Context(), chi.URLParam(r, "id"))
	if errors.Is(err, services.ErrNotFound) {
		response.Fail(w, http.StatusNotFound, response.CodeNotFound, "Product not found")
		return
	}
	if err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not delete product")
		return
	}
	response.OK(w, map[string]bool{"deleted": true})
}

// ---------------- Admin variants ----------------

type variantBody struct {
	Label     string  `json:"label"`
	Unit      string  `json:"unit"`
	UnitValue float64 `json:"unit_value"`
	MRP       float64 `json:"mrp"`
	Price     float64 `json:"price"`
	StockQty  int     `json:"stock_qty"`
	SKU       *string `json:"sku"`
	IsActive  *bool   `json:"is_active"`
}

func (b variantBody) validate() map[string]string {
	return validators.Fields(map[string]string{
		"label":      validators.Required(b.Label),
		"unit":       validators.Unit(b.Unit),
		"unit_value": validators.PositiveNumber(b.UnitValue),
		"price":      validators.PositiveNumber(b.Price),
	})
}

func (b variantBody) toInput() services.VariantInput {
	return services.VariantInput{
		Label: b.Label, Unit: b.Unit, UnitValue: b.UnitValue, MRP: b.MRP,
		Price: b.Price, StockQty: b.StockQty, SKU: b.SKU, IsActive: deref(b.IsActive, true),
	}
}

func (h *Handlers) AdminCreateVariant(w http.ResponseWriter, r *http.Request) {
	var b variantBody
	if !decodeJSON(w, r, &b) {
		return
	}
	if fields := b.validate(); fields != nil {
		response.FailFields(w, fields)
		return
	}
	v, err := h.catalog.CreateVariant(r.Context(), chi.URLParam(r, "id"), b.toInput())
	if err != nil {
		response.Fail(w, http.StatusConflict, response.CodeConflict, "Could not create variant (SKU may already exist)")
		return
	}
	response.Created(w, v)
}

func (h *Handlers) AdminUpdateVariant(w http.ResponseWriter, r *http.Request) {
	var b variantBody
	if !decodeJSON(w, r, &b) {
		return
	}
	if fields := b.validate(); fields != nil {
		response.FailFields(w, fields)
		return
	}
	v, err := h.catalog.UpdateVariant(r.Context(), chi.URLParam(r, "id"), b.toInput())
	if errors.Is(err, services.ErrNotFound) {
		response.Fail(w, http.StatusNotFound, response.CodeNotFound, "Variant not found")
		return
	}
	if err != nil {
		response.Fail(w, http.StatusConflict, response.CodeConflict, "Could not update variant")
		return
	}
	response.OK(w, v)
}

func (h *Handlers) AdminDeleteVariant(w http.ResponseWriter, r *http.Request) {
	err := h.catalog.DeleteVariant(r.Context(), chi.URLParam(r, "id"))
	if errors.Is(err, services.ErrNotFound) {
		response.Fail(w, http.StatusNotFound, response.CodeNotFound, "Variant not found")
		return
	}
	if err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not delete variant")
		return
	}
	response.OK(w, map[string]bool{"deleted": true})
}

// ---------------- Admin images (Cloudinary bypassed) ----------------

type imageBody struct {
	URL       string `json:"url"`
	PublicID  string `json:"cloudinary_public_id"`
	SortOrder int    `json:"sort_order"`
	IsPrimary bool   `json:"is_primary"`
}

func (h *Handlers) AdminAddImage(w http.ResponseWriter, r *http.Request) {
	var b imageBody
	if !decodeJSON(w, r, &b) {
		return
	}
	if fields := validators.Fields(map[string]string{"url": validators.Required(b.URL)}); fields != nil {
		response.FailFields(w, fields)
		return
	}
	img, err := h.catalog.AddImage(r.Context(), chi.URLParam(r, "id"), b.URL, b.PublicID, b.SortOrder, b.IsPrimary)
	if err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not add image")
		return
	}
	response.Created(w, img)
}

func (h *Handlers) AdminDeleteImage(w http.ResponseWriter, r *http.Request) {
	err := h.catalog.DeleteImage(r.Context(), chi.URLParam(r, "id"))
	if errors.Is(err, services.ErrNotFound) {
		response.Fail(w, http.StatusNotFound, response.CodeNotFound, "Image not found")
		return
	}
	if err != nil {
		response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "Could not delete image")
		return
	}
	response.OK(w, map[string]bool{"deleted": true})
}

// deref returns *p or fallback when p is nil — used for optional bool fields.
func deref(p *bool, fallback bool) bool {
	if p == nil {
		return fallback
	}
	return *p
}
