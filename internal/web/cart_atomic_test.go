package web

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/bkroeze/oregon-dev-foundry/internal/commerce"
)

func TestAddCartItemValidatesSelectionBeforeAtomicMutation(t *testing.T) {
	handler, _, store, _, _ := commerceHandler(t)
	product, primary, addon, shipping := seedOneTimeProduct(t, store)
	cookies := csrfCookies(t, handler, "/shop/workshop")

	initial := serveForm(handler, http.MethodPost, "/cart/items", url.Values{
		"csrf_token":    {cookies[csrfCookie].Value},
		"sku":           {product.MasterSKU},
		"addon_sku":     {addon.SKU},
		"shipping_code": {shippingID(shipping)},
		"quantity":      {"1"},
	}, cookies)
	if initial.Code != http.StatusSeeOther {
		t.Fatalf("initial add: %d %s", initial.Code, initial.Body.String())
	}
	mergeCookies(cookies, initial.Result().Cookies())
	assertWebCartQuantities(t, store, cookies[cartCookie].Value, map[int64]int64{primary.ID: 1, addon.ID: 1})

	soldOutStock := int64(0)
	soldOut, err := store.CreateAddon(t.Context(), commerce.SKUParams{
		ProductID: product.ID, SKU: "WORKSHOP-SOLD-OUT", Name: "Sold out", PriceCents: 100,
		Inventory: &soldOutStock, Active: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	inactive, err := store.CreateAddon(t.Context(), commerce.SKUParams{
		ProductID: product.ID, SKU: "WORKSHOP-INACTIVE", Name: "Inactive", PriceCents: 100, Active: false,
	})
	if err != nil {
		t.Fatal(err)
	}

	for name, addonCode := range map[string]string{
		"sold out": soldOut.SKU,
		"inactive": inactive.SKU,
	} {
		t.Run(name, func(t *testing.T) {
			response := serveForm(handler, http.MethodPost, "/cart/items", url.Values{
				"csrf_token":    {cookies[csrfCookie].Value},
				"sku":           {primary.SKU},
				"addon_sku":     {addonCode},
				"shipping_code": {shippingID(shipping)},
				"quantity":      {"2"},
			}, cookies)
			if response.Code != http.StatusUnprocessableEntity {
				t.Fatalf("response: %d %s", response.Code, response.Body.String())
			}
			assertWebCartQuantities(t, store, cookies[cartCookie].Value, map[int64]int64{primary.ID: 1, addon.ID: 1})
		})
	}

	invalidShipping := serveForm(handler, http.MethodPost, "/cart/items", url.Values{
		"csrf_token":    {cookies[csrfCookie].Value},
		"sku":           {primary.SKU},
		"addon_sku":     {addon.SKU},
		"shipping_code": {"999999999"},
		"quantity":      {"2"},
	}, cookies)
	if invalidShipping.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid shipping: %d %s", invalidShipping.Code, invalidShipping.Body.String())
	}
	assertWebCartQuantities(t, store, cookies[cartCookie].Value, map[int64]int64{primary.ID: 1, addon.ID: 1})

	retry := serveForm(handler, http.MethodPost, "/cart/items", url.Values{
		"csrf_token":    {cookies[csrfCookie].Value},
		"sku":           {primary.SKU},
		"addon_sku":     {addon.SKU},
		"shipping_code": {shippingID(shipping)},
		"quantity":      {"2"},
	}, cookies)
	if retry.Code != http.StatusSeeOther {
		t.Fatalf("retry: %d %s", retry.Code, retry.Body.String())
	}
	assertWebCartQuantities(t, store, cookies[cartCookie].Value, map[int64]int64{primary.ID: 3, addon.ID: 3})
}

func assertWebCartQuantities(t *testing.T, store *commerce.Store, token string, want map[int64]int64) {
	t.Helper()
	cart, err := store.CartByToken(t.Context(), token)
	if err != nil {
		t.Fatal(err)
	}
	got := make(map[int64]int64, len(cart.Items))
	for _, item := range cart.Items {
		got[item.SKU.ID] = item.Quantity
	}
	if len(got) != len(want) {
		t.Fatalf("cart quantities = %#v, want %#v", got, want)
	}
	for skuID, quantity := range want {
		if got[skuID] != quantity {
			t.Fatalf("cart quantities = %#v, want %#v", got, want)
		}
	}
}
