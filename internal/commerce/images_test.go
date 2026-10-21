package commerce

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func imageTestProduct(t *testing.T, store *Store, code string) Product {
	t.Helper()
	product, err := store.ApplyProductFixture(t.Context(), ProductFixture{
		Slug: code, MasterSKU: code, Name: code, Description: "Keep this description", Type: ProductTypeOneTime, Active: true,
		SKUs: []SKUFixture{
			{SKU: code + "-PRIMARY", Name: "Primary", Kind: SKUKindPrimary, Options: []byte(`{"size":"4x4"}`), PriceCents: 3456, Active: true, StripePriceID: "price_preserved"},
			{SKU: code + "-ADDON", Name: "Stand", Kind: SKUKindAddon, PriceCents: 123, Active: false},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return product
}

func TestProductImageSlotsPersistAndReplaceIndependently(t *testing.T) {
	store, _ := newTestStore(t)
	product := imageTestProduct(t, store, "images")
	filename := strings.Repeat("a", 64) + ".png"
	slots := []ProductImage{
		{ProductID: product.ID, Filename: filename, Width: 600, Height: 400},
		{ProductID: product.ID, SKUID: product.SKUs[0].ID, Filename: filename, Width: 600, Height: 400},
		{ProductID: product.ID, SKUID: product.SKUs[0].ID, AddonSKUID: product.Addons[0].ID, Filename: filename, Width: 600, Height: 400},
	}
	for _, slot := range slots {
		if err := store.SetProductImage(t.Context(), slot); err != nil {
			t.Fatal(err)
		}
	}
	before, err := store.ListProductImages(t.Context(), product.ID)
	if err != nil || len(before) != 3 {
		t.Fatalf("slots = %#v, %v", before, err)
	}
	slots[2].Filename = strings.Repeat("b", 64) + ".jpg"
	slots[2].Width = 800
	if err := store.SetProductImage(t.Context(), slots[2]); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(store.DB())
	if err != nil {
		t.Fatal(err)
	}
	after, err := reopened.ListProductImages(t.Context(), product.ID)
	if err != nil || len(after) != 3 {
		t.Fatalf("persisted slots = %#v, %v", after, err)
	}
	if after[0] != before[0] || after[1] != before[1] || after[2].ID != before[2].ID || after[2].Filename != slots[2].Filename || after[2].Width != 800 {
		t.Fatalf("replacement changed wrong metadata: before=%#v after=%#v", before, after)
	}
}

func TestProductImageRejectsInvalidSlotsAndFilenames(t *testing.T) {
	store, _ := newTestStore(t)
	product := imageTestProduct(t, store, "owner")
	other := imageTestProduct(t, store, "other")
	valid := ProductImage{ProductID: product.ID, SKUID: product.SKUs[0].ID, Filename: strings.Repeat("c", 64) + ".jpg", Width: 1200, Height: 600}
	cases := map[string]func(*ProductImage){
		"foreign primary":       func(i *ProductImage) { i.SKUID = other.SKUs[0].ID },
		"foreign addon":         func(i *ProductImage) { i.AddonSKUID = other.Addons[0].ID },
		"addon as primary":      func(i *ProductImage) { i.SKUID = product.Addons[0].ID },
		"primary as addon":      func(i *ProductImage) { i.AddonSKUID = product.SKUs[0].ID },
		"addon without primary": func(i *ProductImage) { i.SKUID = 0; i.AddonSKUID = product.Addons[0].ID },
		"missing product":       func(i *ProductImage) { i.ProductID = 999999 },
		"missing primary":       func(i *ProductImage) { i.SKUID = 999999 },
		"negative primary":      func(i *ProductImage) { i.SKUID = -1 },
		"path traversal":        func(i *ProductImage) { i.Filename = "../" + i.Filename },
		"unnormalized name":     func(i *ProductImage) { i.Filename = "upload.jpg" },
		"unsupported format":    func(i *ProductImage) { i.Filename = strings.Repeat("c", 64) + ".svg" },
		"zero dimensions":       func(i *ProductImage) { i.Width = 0 },
		"oversized dimensions":  func(i *ProductImage) { i.Height = 1201 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			image := valid
			mutate(&image)
			if err := store.SetProductImage(t.Context(), image); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("invalid image error = %v", err)
			}
		})
	}
	images, err := store.ListProductImages(t.Context(), product.ID)
	if err != nil || len(images) != 0 {
		t.Fatalf("invalid requests persisted images: %#v, %v", images, err)
	}
}

func TestCatalogStatusUpdatesPreserveMetadataAndOwnership(t *testing.T) {
	store, _ := newTestStore(t)
	product := imageTestProduct(t, store, "status")
	other := imageTestProduct(t, store, "foreign")
	if err := store.SetProductActive(t.Context(), product.ID, false); err != nil {
		t.Fatal(err)
	}
	updated, err := store.ProductByID(t.Context(), product.ID)
	if err != nil {
		t.Fatal(err)
	}
	product.Active, product.UpdatedAt = false, updated.UpdatedAt
	if !reflect.DeepEqual(product, updated) {
		t.Fatalf("status changed other product fields: %#v", updated)
	}
	sku := product.SKUs[0]
	if err := store.SetSKUActive(t.Context(), other.ID, sku.ID, false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-product update error = %v", err)
	}
	unchanged, err := store.SKUByID(t.Context(), sku.ID)
	if err != nil || !reflect.DeepEqual(sku, unchanged) {
		t.Fatalf("cross-product update changed SKU: %#v, %v", unchanged, err)
	}
	for _, active := range []bool{false, true} {
		if err := store.SetSKUActive(t.Context(), product.ID, sku.ID, active); err != nil {
			t.Fatal(err)
		}
		updatedSKU, err := store.SKUByID(t.Context(), sku.ID)
		if err != nil {
			t.Fatal(err)
		}
		sku.Active, sku.UpdatedAt = active, updatedSKU.UpdatedAt
		if !reflect.DeepEqual(sku, updatedSKU) {
			t.Fatalf("status changed other SKU fields: %#v", updatedSKU)
		}
	}
	if err := store.SetProductActive(t.Context(), 999999, true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing product error = %v", err)
	}
}
