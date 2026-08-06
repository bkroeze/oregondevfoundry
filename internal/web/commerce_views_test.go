package web

import (
	"testing"

	"github.com/bkroeze/oregon-dev-foundry/internal/commerce"
)

func TestProductViewHidesInactiveShippingFromPublic(t *testing.T) {
	product := commerce.Product{
		ID: 1,
		ShippingOptions: []commerce.ShippingOption{
			{ID: 1, Name: "Retired", AmountCents: 100, Active: false},
			{ID: 2, Name: "Current", AmountCents: 200, Active: true},
		},
	}
	public := productView(product, false)
	if len(public.ShippingOptions) != 1 || public.ShippingOptions[0].Name != "Current" {
		t.Fatalf("public shipping options = %#v", public.ShippingOptions)
	}
	admin := productView(product, true)
	if len(admin.ShippingOptions) != 2 {
		t.Fatalf("admin shipping options = %#v", admin.ShippingOptions)
	}
}
