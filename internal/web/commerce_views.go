package web

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/bkroeze/oregon-dev-foundry/internal/commerce"
	"github.com/bkroeze/oregon-dev-foundry/internal/templates"
)

func productView(product commerce.Product, admin bool) templates.ProductView {
	view := templates.ProductView{
		Slug:        product.Slug,
		Name:        product.Name,
		Summary:     product.Description,
		Description: product.Description,
		Type:        string(product.Type),
		Currency:    "USD",
		MasterSKU:   product.MasterSKU,
		Active:      product.Active,
	}
	if product.ID > 0 {
		view.ID = strconv.FormatInt(product.ID, 10)
	}
	var firstAvailable bool
	for _, sku := range product.SKUs {
		available := sku.Active && (sku.Inventory == nil || *sku.Inventory > 0)
		option := templates.OptionView{
			SKU:         sku.SKU,
			Label:       sku.Name,
			Description: optionSummary(sku.OptionsJSON),
			Price:       money(sku.PriceCents, "USD"),
			PriceCents:  sku.PriceCents,
			Disabled:    !available,
		}
		if available && !firstAvailable {
			option.Selected = true
			firstAvailable = true
			view.Price = option.Price
			view.PriceCents = sku.PriceCents
		}
		view.PrimaryOptions = append(view.PrimaryOptions, option)
		view.SKUs = append(view.SKUs, skuView(sku, product.ID, admin))
	}
	for _, addon := range product.Addons {
		available := addon.Active && (addon.Inventory == nil || *addon.Inventory > 0)
		view.Addons = append(view.Addons, templates.OptionView{
			SKU: addon.SKU, Label: addon.Name, Description: optionSummary(addon.OptionsJSON),
			Price: money(addon.PriceCents, "USD"), PriceCents: addon.PriceCents, Disabled: !available,
		})
		view.SKUs = append(view.SKUs, skuView(addon, product.ID, admin))
	}
	for _, shipping := range product.ShippingOptions {
		if !admin && !shipping.Active {
			continue
		}
		entry := templates.ShippingView{
			ID:           strconv.FormatInt(shipping.ID, 10),
			Code:         strconv.FormatInt(shipping.ID, 10),
			Name:         shipping.Name,
			Price:        money(shipping.AmountCents, "USD"),
			PriceCents:   shipping.AmountCents,
			SortOrder:    shipping.SortOrder,
			Active:       shipping.Active,
			UpdateAction: fmt.Sprintf("/admin/products/%d/shipping/%d", product.ID, shipping.ID),
			DeleteAction: fmt.Sprintf("/admin/products/%d/shipping/%d/delete", product.ID, shipping.ID),
		}
		view.ShippingOptions = append(view.ShippingOptions, entry)
	}
	if admin {
		view.EditURL = fmt.Sprintf("/admin/products/%d", product.ID)
	}
	return view
}

func skuView(sku commerce.SKU, productID int64, admin bool) templates.SKUView {
	view := templates.SKUView{
		ID:            strconv.FormatInt(sku.ID, 10),
		SKU:           sku.SKU,
		Name:          sku.Name,
		Description:   optionSummary(sku.OptionsJSON),
		Kind:          string(sku.Kind),
		Price:         money(sku.PriceCents, "USD"),
		PriceCents:    sku.PriceCents,
		StripePriceID: sku.StripePriceID,
		Available:     sku.Active && (sku.Inventory == nil || *sku.Inventory > 0),
		Active:        sku.Active,
	}
	if sku.Inventory != nil {
		view.InventoryTracked = true
		view.InventoryQuantity = int(*sku.Inventory)
	}
	if admin {
		view.UpdateAction = fmt.Sprintf("/admin/products/%d/skus/%d", productID, sku.ID)
		view.DeleteAction = fmt.Sprintf("/admin/products/%d/skus/%d/delete", productID, sku.ID)
	}
	return view
}

func optionSummary(raw string) string {
	if strings.TrimSpace(raw) == "" || strings.TrimSpace(raw) == "{}" {
		return ""
	}
	var values map[string]any
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return raw
	}
	parts := make([]string, 0, len(values))
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s: %v", key, values[key]))
	}
	return strings.Join(parts, ", ")
}
