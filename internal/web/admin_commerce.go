package web

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/bkroeze/oregon-dev-foundry/internal/auth"
	"github.com/bkroeze/oregon-dev-foundry/internal/commerce"
	"github.com/bkroeze/oregon-dev-foundry/internal/templates"
)

func (h Handler) adminProducts(w http.ResponseWriter, r *http.Request) {
	account, ok := h.requireAdminCommerce(w, r)
	if !ok {
		return
	}
	products, err := h.commerce.ListProducts(r.Context())
	if err != nil {
		h.commerceError(w, "list products", err)
		return
	}
	views := make([]templates.ProductView, 0, len(products))
	for _, product := range products {
		views = append(views, productView(product, true))
	}
	h.renderComponent(w, r, http.StatusOK, templates.AdminProductsPage(templates.AdminProductsData{Account: account, Products: views, NewURL: "/admin/products/new", Status: r.URL.Query().Get("status")}))
}

func (h Handler) newAdminProduct(w http.ResponseWriter, r *http.Request) {
	account, ok := h.requireAdminCommerce(w, r)
	if !ok {
		return
	}
	product := commerce.Product{Type: commerce.ProductTypeOneTime, Active: true}
	h.renderComponent(w, r, http.StatusOK, templates.AdminProductPage(h.adminProductData(account, product)))
}

func (h Handler) adminProduct(w http.ResponseWriter, r *http.Request) {
	account, ok := h.requireAdminCommerce(w, r)
	if !ok {
		return
	}
	id, err := pathID(r, "id")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	product, err := h.commerce.ProductByID(r.Context(), id)
	if errors.Is(err, commerce.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		h.commerceError(w, "load admin product", err)
		return
	}
	h.renderComponent(w, r, http.StatusOK, templates.AdminProductPage(h.adminProductData(account, product)))
}

func (h Handler) createAdminProduct(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdminCommerce(w, r); !ok {
		return
	}
	if !h.parseCommerceForm(w, r) {
		return
	}
	product, err := h.commerce.CreateProduct(r.Context(), productParams(r))
	if err != nil {
		h.adminInputError(w, err)
		return
	}
	http.Redirect(w, r, "/admin/products/"+strconv.FormatInt(product.ID, 10)+"?status=Product+created", http.StatusSeeOther)
}

func (h Handler) updateAdminProduct(w http.ResponseWriter, r *http.Request) {
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
	if _, err = h.commerce.UpdateProduct(r.Context(), id, productParams(r)); err != nil {
		h.adminInputError(w, err)
		return
	}
	http.Redirect(w, r, "/admin/products/"+strconv.FormatInt(id, 10)+"?status=Product+saved", http.StatusSeeOther)
}

func (h Handler) createAdminSKU(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdminCommerce(w, r); !ok {
		return
	}
	if !h.parseCommerceForm(w, r) {
		return
	}
	productID, err := pathID(r, "id")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	params, err := skuParams(r, productID)
	if err != nil {
		h.adminInputError(w, err)
		return
	}
	if _, err = h.commerce.CreateSKU(r.Context(), params); err != nil {
		h.adminInputError(w, err)
		return
	}
	h.redirectAdminProduct(w, r, productID, "SKU added")
}

func (h Handler) updateAdminSKU(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdminCommerce(w, r); !ok {
		return
	}
	if !h.parseCommerceForm(w, r) {
		return
	}
	productID, err := pathID(r, "id")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	skuID, err := pathID(r, "skuID")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	params, err := skuParams(r, productID)
	if err != nil {
		h.adminInputError(w, err)
		return
	}
	if _, err = h.commerce.UpdateSKU(r.Context(), skuID, params); err != nil {
		h.adminInputError(w, err)
		return
	}
	h.redirectAdminProduct(w, r, productID, "SKU saved")
}

func (h Handler) deleteAdminSKU(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdminCommerce(w, r); !ok {
		return
	}
	if !h.parseCommerceForm(w, r) {
		return
	}
	productID, err := pathID(r, "id")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	skuID, err := pathID(r, "skuID")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err = h.commerce.DeleteSKU(r.Context(), skuID); err != nil {
		h.adminInputError(w, err)
		return
	}
	h.redirectAdminProduct(w, r, productID, "SKU deactivated")
}

func (h Handler) createAdminShipping(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdminCommerce(w, r); !ok {
		return
	}
	if !h.parseCommerceForm(w, r) {
		return
	}
	productID, err := pathID(r, "id")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	params, err := shippingParams(r, productID)
	if err != nil {
		h.adminInputError(w, err)
		return
	}
	if _, err = h.commerce.CreateShippingOption(r.Context(), params); err != nil {
		h.adminInputError(w, err)
		return
	}
	h.redirectAdminProduct(w, r, productID, "Shipping option added")
}

func (h Handler) updateAdminShipping(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdminCommerce(w, r); !ok {
		return
	}
	if !h.parseCommerceForm(w, r) {
		return
	}
	productID, err := pathID(r, "id")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	shippingID, err := pathID(r, "shippingID")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	params, err := shippingParams(r, productID)
	if err != nil {
		h.adminInputError(w, err)
		return
	}
	if _, err = h.commerce.UpdateShippingOption(r.Context(), shippingID, params); err != nil {
		h.adminInputError(w, err)
		return
	}
	h.redirectAdminProduct(w, r, productID, "Shipping option saved")
}

func (h Handler) deleteAdminShipping(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdminCommerce(w, r); !ok {
		return
	}
	if !h.parseCommerceForm(w, r) {
		return
	}
	productID, err := pathID(r, "id")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	shippingID, err := pathID(r, "shippingID")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err = h.commerce.DeleteShippingOption(r.Context(), shippingID); err != nil {
		h.adminInputError(w, err)
		return
	}
	h.redirectAdminProduct(w, r, productID, "Shipping option deactivated")
}

func (h Handler) adminProductData(account templates.AccountView, product commerce.Product) templates.AdminProductData {
	id := product.ID
	base := "/admin/products"
	if id > 0 {
		base += "/" + strconv.FormatInt(id, 10)
	}
	return templates.AdminProductData{Account: account, Product: productView(product, true), SaveAction: base, AddSKUAction: base + "/skus", AddShippingAction: base + "/shipping", CSRFToken: account.CSRFToken}
}

func (h Handler) requireAdminCommerce(w http.ResponseWriter, r *http.Request) (templates.AccountView, bool) {
	account, ok := h.requireAccount(w, r)
	if !ok {
		return templates.AccountView{}, false
	}
	if account.Role != string(auth.RoleAdmin) {
		h.renderComponent(w, r, http.StatusForbidden, templates.ForbiddenPage(account))
		return templates.AccountView{}, false
	}
	return account, true
}

func productParams(r *http.Request) commerce.ProductParams {
	return commerce.ProductParams{Slug: r.FormValue("slug"), MasterSKU: r.FormValue("master_sku"), Name: r.FormValue("name"), Description: r.FormValue("description"), Type: commerce.ProductType(r.FormValue("type")), Active: r.FormValue("active") == "true"}
}

func skuParams(r *http.Request, productID int64) (commerce.SKUParams, error) {
	price, err := parseNonnegativeInt(r.FormValue("price_cents"), 1_000_000_000)
	if err != nil {
		return commerce.SKUParams{}, err
	}
	var inventory *int64
	if r.FormValue("inventory_tracked") == "true" {
		value, parseErr := parseNonnegativeInt(r.FormValue("inventory_quantity"), 1_000_000_000)
		if parseErr != nil {
			return commerce.SKUParams{}, parseErr
		}
		inventory = &value
	}
	return commerce.SKUParams{ProductID: productID, SKU: r.FormValue("sku"), Name: r.FormValue("name"), Kind: commerce.SKUKind(r.FormValue("kind")), OptionsJSON: r.FormValue("options_json"), PriceCents: price, Inventory: inventory, Active: r.FormValue("active") == "true", StripePriceID: r.FormValue("stripe_price_id")}, nil
}

func shippingParams(r *http.Request, productID int64) (commerce.ShippingOptionParams, error) {
	amount, err := parseNonnegativeInt(r.FormValue("amount_cents"), 1_000_000_000)
	if err != nil {
		return commerce.ShippingOptionParams{}, err
	}
	sortOrder, err := parseNonnegativeInt(r.FormValue("sort_order"), 1_000_000)
	if err != nil {
		return commerce.ShippingOptionParams{}, err
	}
	return commerce.ShippingOptionParams{ProductID: productID, Name: r.FormValue("name"), AmountCents: amount, SortOrder: int(sortOrder), Active: r.FormValue("active") == "true"}, nil
}

func pathID(r *http.Request, name string) (int64, error) {
	return parsePositiveInt(r.PathValue(name), 1<<62)
}
func (h Handler) redirectAdminProduct(w http.ResponseWriter, r *http.Request, id int64, status string) {
	http.Redirect(w, r, "/admin/products/"+strconv.FormatInt(id, 10)+"?status="+strings.ReplaceAll(status, " ", "+"), http.StatusSeeOther)
}
func (h Handler) adminInputError(w http.ResponseWriter, _ error) {
	http.Error(w, "invalid product data", http.StatusUnprocessableEntity)
}
