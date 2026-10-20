package web

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/bkroeze/oregon-dev-foundry/internal/commerce"
	"github.com/bkroeze/oregon-dev-foundry/internal/media"
	"github.com/bkroeze/oregon-dev-foundry/internal/templates"
)

func (h Handler) adminCatalog(w http.ResponseWriter, r *http.Request) {
	account, ok := h.requireAdminCommerce(w, r)
	if !ok {
		return
	}
	products, err := h.commerce.ListProducts(r.Context())
	if err != nil {
		h.commerceError(w, "list catalog", err)
		return
	}
	entries := make([]templates.CatalogEntry, 0, len(products))
	for _, product := range products {
		images, err := h.commerce.ListProductImages(r.Context(), product.ID)
		if err != nil {
			h.commerceError(w, "list catalog images", err)
			return
		}
		entries = append(entries, templates.CatalogEntry{Product: product, Images: images})
	}
	h.renderComponent(w, r, http.StatusOK, templates.AdminCatalogPage(account, entries))
}

func (h Handler) adminCatalogStatus(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdminCommerce(w, r); !ok {
		return
	}
	if !h.parseCommerceForm(w, r) {
		return
	}
	id, err := pathID(r, "id")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	active, err := strconv.ParseBool(r.FormValue("active"))
	if err != nil {
		http.Error(w, "Choose an active status.", http.StatusUnprocessableEntity)
		return
	}
	if err := h.commerce.SetProductActive(r.Context(), id, active); err != nil {
		h.catalogMutationError(w, r, err)
		return
	}
	http.Redirect(w, r, "/admin/catalog", http.StatusSeeOther)
}

func (h Handler) adminCatalogSKUStatus(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdminCommerce(w, r); !ok {
		return
	}
	if !h.parseCommerceForm(w, r) {
		return
	}
	id, err := pathID(r, "id")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	skuID, err := pathID(r, "skuID")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	active, err := strconv.ParseBool(r.FormValue("active"))
	if err != nil {
		http.Error(w, "Choose an active status.", http.StatusUnprocessableEntity)
		return
	}
	if err := h.commerce.SetSKUActive(r.Context(), id, skuID, active); err != nil {
		h.catalogMutationError(w, r, err)
		return
	}
	http.Redirect(w, r, "/admin/catalog", http.StatusSeeOther)
}

func (h Handler) catalogMutationError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, commerce.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if errors.Is(err, commerce.ErrInvalidInput) {
		http.Error(w, "Invalid catalog selection.", http.StatusUnprocessableEntity)
		return
	}
	h.commerceError(w, "update catalog", err)
}

func (h Handler) adminCatalogImage(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdminCommerce(w, r); !ok {
		return
	}
	// Allow multipart framing in addition to the importer's 8 MiB file limit.
	r.Body = http.MaxBytesReader(w, r.Body, (8<<20)+(64<<10))
	err := r.ParseMultipartForm(1 << 20)
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	if err != nil {
		http.Error(w, "Upload a JPEG or PNG image no larger than 8 MiB.", http.StatusBadRequest)
		return
	}
	if !h.validCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	id, err := pathID(r, "id")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	product, err := h.commerce.ProductByID(r.Context(), id)
	if err != nil {
		h.catalogMutationError(w, r, err)
		return
	}
	skuID, err := strconv.ParseInt(r.FormValue("sku_id"), 10, 64)
	if err != nil || skuID < 0 {
		http.Error(w, "Choose a primary SKU or product default.", http.StatusUnprocessableEntity)
		return
	}
	addonID, err := strconv.ParseInt(r.FormValue("addon_sku_id"), 10, 64)
	if err != nil || addonID < 0 {
		http.Error(w, "Choose an optional add-on SKU.", http.StatusUnprocessableEntity)
		return
	}
	primaryOK, addonOK := skuID == 0, addonID == 0
	for _, sku := range product.SKUs {
		if sku.ID == skuID && sku.Kind == commerce.SKUKindPrimary {
			primaryOK = true
		}
	}
	for _, sku := range product.Addons {
		if sku.ID == addonID && sku.Kind == commerce.SKUKindAddon {
			addonOK = true
		}
	}
	if !primaryOK || !addonOK || (skuID == 0 && addonID != 0) {
		http.Error(w, "Image slot must use this product's primary SKU and optional add-on; defaults cannot have add-ons.", http.StatusUnprocessableEntity)
		return
	}
	file, _, err := r.FormFile("image")
	if err != nil {
		http.Error(w, "Choose an image file.", http.StatusUnprocessableEntity)
		return
	}
	defer file.Close()
	result, err := media.Import(h.catalogMediaDir(), file)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	err = h.commerce.SetProductImage(r.Context(), commerce.ProductImage{ProductID: id, SKUID: skuID, AddonSKUID: addonID, Filename: result.Filename, Width: result.Width, Height: result.Height})
	if err != nil {
		h.catalogMutationError(w, r, err)
		return
	}
	http.Redirect(w, r, "/admin/catalog", http.StatusSeeOther)
}

func (h Handler) catalogMediaDir() string {
	if h.config.MediaDir != "" {
		return h.config.MediaDir
	}
	if h.config.DatabasePath != "" {
		return filepath.Join(filepath.Dir(h.config.DatabasePath), "media")
	}
	return filepath.Join("data", "media")
}

func (h Handler) productImage(w http.ResponseWriter, r *http.Request) {
	filename := r.PathValue("filename")
	if !media.ValidFilename(filename) {
		http.NotFound(w, r)
		return
	}
	// Open relative to a confined root: symlinks cannot escape the media directory.
	root, err := os.OpenRoot(h.catalogMediaDir())
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer root.Close()
	file, err := root.Open(filename)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	if filepath.Ext(filename) == ".png" {
		w.Header().Set("Content-Type", "image/png")
	} else {
		w.Header().Set("Content-Type", "image/jpeg")
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	http.ServeContent(w, r, filename, info.ModTime(), file)
}
