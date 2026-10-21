package web

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/bkroeze/oregon-dev-foundry/internal/auth"
	"github.com/bkroeze/oregon-dev-foundry/internal/commerce"
	"github.com/bkroeze/oregon-dev-foundry/internal/templates"
)

func (h Handler) productPage(w http.ResponseWriter, r *http.Request) {
	product, err := h.commerce.ProductBySlug(r.Context(), r.PathValue("slug"))
	if errors.Is(err, commerce.ErrNotFound) || err == nil && !product.Active {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		h.commerceError(w, "load product", err)
		return
	}
	data := h.productPageData(w, r, product)
	if !h.loadProductImages(w, r, product, &data.Product) {
		return
	}
	h.renderComponent(w, r, http.StatusOK, templates.ProductPage(data))
}

func (h Handler) productBlock(w http.ResponseWriter, r *http.Request) {
	product, err := h.commerce.FindProduct(r.Context(), r.PathValue("key"))
	if errors.Is(err, commerce.ErrNotFound) || err == nil && !product.Active {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		h.commerceError(w, "load product block", err)
		return
	}
	page := h.productPageData(w, r, product)
	if !h.loadProductImages(w, r, product, &page.Product) {
		return
	}
	h.renderComponent(w, r, http.StatusOK, templates.ProductBlock(templates.ProductBlockData{
		Product: page.Product, Action: page.Action, CSRFToken: page.CSRFToken,
		ReferralToken: page.ReferralToken, Name: page.Name, Email: page.Email,
		CreateAccount: page.CreateAccount, Errors: page.Errors, Error: page.Error, Status: page.Status,
	}))
}

func (h Handler) productPageData(w http.ResponseWriter, r *http.Request, product commerce.Product) templates.ProductPageData {
	account := h.accountView(w, r)
	data := templates.ProductPageData{
		Account: account, Product: productView(product, false), CSRFToken: account.CSRFToken,
		ReferralToken: referralToken(r), Errors: map[string]string{},
	}
	if product.Type == commerce.ProductTypeSubscription {
		data.Action = "/subscriptions/checkout"
	} else {
		data.Action = "/cart/items"
	}
	if user, ok := h.currentUser(r); ok {
		data.Name = user.DisplayName
		if strings.Contains(user.Username, "@") {
			data.Email = user.Username
		}
	}
	return data
}

func (h Handler) loadProductImages(w http.ResponseWriter, r *http.Request, product commerce.Product, view *templates.ProductView) bool {
	images, err := h.commerce.ListProductImages(r.Context(), product.ID)
	if err != nil {
		h.commerceError(w, "load product images", err)
		return false
	}
	for _, image := range images {
		label := product.Name
		if image.SKUID != 0 {
			primary, ok := activeImageSKU(product, image.SKUID)
			if !ok {
				continue
			}
			label += " — " + primary.Name
		}
		if image.AddonSKUID != 0 {
			addon, ok := activeImageSKU(product, image.AddonSKUID)
			if !ok {
				continue
			}
			label += " + " + addon.Name
		}
		view.Images = append(view.Images, templates.ProductImageView{URL: "/media/products/" + image.Filename, Label: label})
	}
	return true
}

func activeImageSKU(product commerce.Product, id int64) (commerce.SKU, bool) {
	for _, sku := range product.SKUs {
		if sku.ID == id && sku.Active {
			return sku, true
		}
	}
	for _, sku := range product.Addons {
		if sku.ID == id && sku.Active {
			return sku, true
		}
	}
	return commerce.SKU{}, false
}

func (h Handler) currentUser(r *http.Request) (auth.User, bool) {
	cookie, err := r.Cookie(sessionCookie)
	if err != nil {
		return auth.User{}, false
	}
	user, err := h.users.UserBySession(r.Context(), cookie.Value)
	return user, err == nil
}

func referralToken(r *http.Request) string {
	cookie, err := r.Cookie(referralCookie)
	if err != nil {
		return ""
	}
	return cookie.Value
}

func (h Handler) commerceError(w http.ResponseWriter, operation string, err error) {
	slog.Error(operation, "error", err)
	http.Error(w, "commerce is temporarily unavailable", http.StatusInternalServerError)
}
