package commerce

import (
	"strings"
	"testing"
)

func TestApplyProductFixtureIsIdempotentAndUpdatesRecords(t *testing.T) {
	store, _ := newTestStore(t)
	fixture := ProductFixture{
		Slug: "test-product", MasterSKU: "TEST-PRODUCT", Name: "Test Product", Type: ProductTypeOneTime, Active: true,
		SKUs: []SKUFixture{
			{SKU: "TEST-PRODUCT-RED", Name: "Red", Kind: SKUKindPrimary, Options: []byte(`{"color":"red"}`), PriceCents: 1, Active: true},
			{SKU: "TEST-PRODUCT-BLUE", Name: "Blue", Kind: SKUKindPrimary, Options: []byte(`{"color":"blue"}`), PriceCents: 1, Active: true},
		},
		ShippingOptions: []ShippingOptionFixture{{Name: "Free shipping", Active: true}},
	}
	first, err := store.ApplyProductFixture(t.Context(), fixture)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.SKUs) != 2 || len(first.ShippingOptions) != 1 || first.ShippingOptions[0].AmountCents != 0 {
		t.Fatalf("first fixture result = %#v", first)
	}
	redID, err := store.SKUByCode(t.Context(), "TEST-PRODUCT-RED")
	if err != nil {
		t.Fatal(err)
	}

	fixture.Name = "Updated Test Product"
	fixture.SKUs[0].Name = "Crimson"
	fixture.SKUs[0].PriceCents = 2
	second, err := store.ApplyProductFixture(t.Context(), fixture)
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != first.ID || second.Name != fixture.Name || len(second.SKUs) != 2 || len(second.ShippingOptions) != 1 {
		t.Fatalf("second fixture result = %#v", second)
	}
	updatedRed, err := store.SKUByCode(t.Context(), "TEST-PRODUCT-RED")
	if err != nil {
		t.Fatal(err)
	}
	if updatedRed.ID != redID.ID || updatedRed.Name != "Crimson" || updatedRed.PriceCents != 2 {
		t.Fatalf("updated red SKU = %#v; original = %#v", updatedRed, redID)
	}
	var count int
	if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM commerce_products WHERE slug=?`, fixture.Slug).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("product count = %d", count)
	}
}

func TestDecodeProductFixtureRejectsTrailingJSON(t *testing.T) {
	_, err := DecodeProductFixture(strings.NewReader(`{"slug":"one"} {"slug":"two"}`))
	if err == nil || !strings.Contains(err.Error(), "multiple JSON values") {
		t.Fatalf("error = %v", err)
	}
}
