package web

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"github.com/bkroeze/oregon-dev-foundry/internal/commerce"
	"github.com/bkroeze/oregon-dev-foundry/internal/templates"
)

func (h Handler) vendingSignagePage(w http.ResponseWriter, r *http.Request) {
	product, err := h.commerce.ProductBySlug(r.Context(), "vending-signage")
	if errors.Is(err, commerce.ErrNotFound) {
		product, err = h.commerce.ProductByMasterSKU(r.Context(), "SLATE-SIGNAGE")
	}
	if errors.Is(err, commerce.ErrNotFound) || err == nil && !product.Active {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		h.commerceError(w, "load signage", err)
		return
	}
	images, err := h.commerce.ListProductImages(r.Context(), product.ID)
	if err != nil {
		h.commerceError(w, "load signage images", err)
		return
	}
	data := signagePageData(product, images)
	data.Account = h.accountView(w, r)
	h.renderComponent(w, r, http.StatusOK, templates.VendingSignagePage(data))
}

func signagePageData(product commerce.Product, images []commerce.ProductImage) templates.SignagePageData {
	data := templates.SignagePageData{}
	for _, addon := range product.Addons {
		if addon.SKU == "SLATE-STAND" || addon.SKU == "wooden-stand" {
			data.StandID = strconv.FormatInt(addon.ID, 10)
			data.StandActive = addon.Active
			break
		}
	}
	for _, sku := range product.SKUs {
		option := templates.SignageOption{ID: strconv.FormatInt(sku.ID, 10), Name: sku.Name, Active: sku.Active}
		option.Selected = sku.Active && !data.Available
		if option.Selected {
			data.Available = true
		}
		for _, image := range images {
			if image.SKUID != sku.ID || image.ProductID != product.ID {
				continue
			}
			src := "/media/products/" + url.PathEscape(image.Filename)
			if image.AddonSKUID == 0 {
				option.ImageURL = src
			} else if strconv.FormatInt(image.AddonSKUID, 10) == data.StandID {
				option.StandImageURL = src
			}
		}
		if option.Selected {
			data.ImageURL = option.ImageURL
			data.ImageAlt = "Saved photograph of " + option.Name + " without wooden stand"
		}
		data.Options = append(data.Options, option)
	}
	return data
}
