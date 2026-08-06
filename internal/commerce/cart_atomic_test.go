package commerce

import (
	"errors"
	"testing"
)

func TestAddCartSelectionIsAtomic(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := t.Context()
	product, err := store.CreateProduct(ctx, ProductParams{
		Slug: "atomic-cart", MasterSKU: "ATOMIC", Name: "Atomic", Type: ProductTypeOneTime, Active: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	primaryStock := int64(10)
	primary, err := store.CreateSKU(ctx, SKUParams{
		ProductID: product.ID, SKU: "ATOMIC-PRIMARY", Name: "Primary", Kind: SKUKindPrimary,
		PriceCents: 1000, Inventory: &primaryStock, Active: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	addonStock := int64(2)
	addon, err := store.CreateAddon(ctx, SKUParams{
		ProductID: product.ID, SKU: "ATOMIC-ADDON", Name: "Addon", PriceCents: 200,
		Inventory: &addonStock, Active: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	inactive, err := store.CreateAddon(ctx, SKUParams{
		ProductID: product.ID, SKU: "ATOMIC-INACTIVE", Name: "Inactive", PriceCents: 100, Active: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	otherProduct, err := store.CreateProduct(ctx, ProductParams{
		Slug: "other-cart", MasterSKU: "OTHER", Name: "Other", Type: ProductTypeOneTime, Active: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	otherAddon, err := store.CreateAddon(ctx, SKUParams{
		ProductID: otherProduct.ID, SKU: "OTHER-ADDON", Name: "Other add-on", PriceCents: 100, Active: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	cart, err := store.CreateCart(ctx, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	params := CartSelectionParams{PrimarySKUID: primary.ID, AddonSKUIDs: []int64{addon.ID}, Quantity: 3}
	if _, err = store.AddCartSelection(ctx, cart.Token, params); !errors.Is(err, ErrSoldOut) {
		t.Fatalf("sold-out selection error = %v", err)
	}
	assertCartQuantities(t, store, cart.Token, nil)

	params.Quantity = 2
	updated, err := store.AddCartSelection(ctx, cart.Token, params)
	if err != nil {
		t.Fatal(err)
	}
	assertItemQuantities(t, updated, map[int64]int64{primary.ID: 2, addon.ID: 2})

	if _, err = store.AddCartSelection(ctx, cart.Token, CartSelectionParams{
		PrimarySKUID: primary.ID, AddonSKUIDs: []int64{inactive.ID}, Quantity: 1,
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("inactive add-on error = %v", err)
	}
	if _, err = store.AddCartSelection(ctx, cart.Token, CartSelectionParams{
		PrimarySKUID: primary.ID, AddonSKUIDs: []int64{otherAddon.ID}, Quantity: 1,
	}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("cross-product add-on error = %v", err)
	}
	assertCartQuantities(t, store, cart.Token, map[int64]int64{primary.ID: 2, addon.ID: 2})
}

func assertCartQuantities(t *testing.T, store *Store, token string, want map[int64]int64) {
	t.Helper()
	cart, err := store.CartByToken(t.Context(), token)
	if err != nil {
		t.Fatal(err)
	}
	assertItemQuantities(t, cart, want)
}

func assertItemQuantities(t *testing.T, cart Cart, want map[int64]int64) {
	t.Helper()
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
