package commerce

import (
	"errors"
	"reflect"
	"testing"
)

func TestEnsureSignageCatalogSeedsExplicitlyAndPreservesChanges(t *testing.T) {
	store, _ := newTestStore(t)
	if _, err := store.ProductBySlug(t.Context(), "vending-signage"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("New unexpectedly seeded signage: %v", err)
	}
	if err := store.EnsureSignageCatalog(t.Context()); err != nil {
		t.Fatal(err)
	}
	product, err := store.ProductBySlug(t.Context(), "vending-signage")
	if err != nil || !product.Active || len(product.SKUs) != 4 || len(product.Addons) != 1 {
		t.Fatalf("seeded product = %#v, %v", product, err)
	}
	for _, want := range []struct {
		code, name, options string
		active              bool
	}{
		{"SLATE-4X4", "4x4 Slate — Tabletop display", `{"size":"4x4"}`, true},
		{"SLATE-12X8", "12x8 Slate", `{"size":"12x8"}`, false},
		{"SLATE-8X4", "8x4 Slate", `{"size":"8x4"}`, false},
		{"SLATE-9X6", "9x6 Slate", `{"size":"9x6"}`, false},
		{"SLATE-STAND", "Wooden stand", `{}`, true},
	} {
		sku, err := store.SKUByCode(t.Context(), want.code)
		if err != nil || sku.Name != want.name || sku.OptionsJSON != want.options || sku.Active != want.active || sku.Inventory == nil || *sku.Inventory != 0 {
			t.Fatalf("seeded %s = %#v, %v", want.code, sku, err)
		}
	}
	cart, err := store.CreateCart(t.Context(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	primary, err := store.SKUByCode(t.Context(), "SLATE-4X4")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddCartItem(t.Context(), cart.Token, primary.ID, 1); !errors.Is(err, ErrSoldOut) {
		t.Fatalf("preview-only SKU added to cart: %v", err)
	}
	if err := store.SetProductActive(t.Context(), product.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := store.SetSKUActive(t.Context(), product.ID, primary.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(t.Context(), `UPDATE commerce_skus SET name='Admin name', price_cents=999 WHERE id=?`, primary.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(t.Context(), `DELETE FROM commerce_skus WHERE sku='SLATE-12X8'`); err != nil {
		t.Fatal(err)
	}
	before, err := store.ProductByID(t.Context(), product.ID)
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := New(store.DB())
	if err != nil {
		t.Fatal(err)
	}
	if err := reopened.EnsureSignageCatalog(t.Context()); err != nil {
		t.Fatal(err)
	}
	after, err := store.ProductByID(t.Context(), product.ID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("reinitialization overwrote changes: %#v, %v", after, err)
	}
}

func TestEnsureSignageCatalogDoesNotRecreateDeletedProduct(t *testing.T) {
	store, _ := newTestStore(t)
	if err := store.EnsureSignageCatalog(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(t.Context(), `DELETE FROM commerce_products WHERE slug='vending-signage'`); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(store.DB())
	if err != nil {
		t.Fatal(err)
	}
	if err := reopened.EnsureSignageCatalog(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.ProductBySlug(t.Context(), "vending-signage"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted product recreated: %v", err)
	}
}

func TestEnsureSignageCatalogLeavesExistingCatalogUntouched(t *testing.T) {
	store, _ := newTestStore(t)
	existing := imageTestProduct(t, store, "vending-signage")
	if err := store.EnsureSignageCatalog(t.Context()); err != nil {
		t.Fatal(err)
	}
	after, err := store.ProductByID(t.Context(), existing.ID)
	if err != nil || !reflect.DeepEqual(existing, after) {
		t.Fatalf("existing catalog changed: %#v, %v", after, err)
	}
	if _, err := store.SKUByCode(t.Context(), "SLATE-4X4"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("existing catalog was backfilled: %v", err)
	}
}

func TestEnsureSignageCatalogLeavesExistingMasterSKUUntouched(t *testing.T) {
	store, _ := newTestStore(t)
	existing, err := store.CreateProduct(t.Context(), ProductParams{
		Slug: "slate-signage", MasterSKU: "SLATE-SIGNAGE", Name: "Slate Vendor Signage",
		Type: ProductTypeOneTime, Active: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureSignageCatalog(t.Context()); err != nil {
		t.Fatal(err)
	}
	after, err := store.ProductByID(t.Context(), existing.ID)
	if err != nil || !reflect.DeepEqual(existing, after) {
		t.Fatalf("existing master SKU catalog changed: %#v, %v", after, err)
	}
	if _, err := store.ProductBySlug(t.Context(), "vending-signage"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("seeded duplicate catalog: %v", err)
	}
}

func TestEnsureSignageCatalogRollsBackPartialSeed(t *testing.T) {
	store, _ := newTestStore(t)
	other := imageTestProduct(t, store, "conflict")
	conflict, err := store.CreateSKU(t.Context(), SKUParams{ProductID: other.ID, SKU: "SLATE-8X4", Name: "Existing unrelated SKU", Kind: SKUKindAddon, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureSignageCatalog(t.Context()); err == nil {
		t.Fatal("expected conflicting SKU to reject seed")
	}
	if _, err := store.ProductBySlug(t.Context(), "vending-signage"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("partial product persisted: %v", err)
	}
	if _, err := store.SKUByCode(t.Context(), "SLATE-4X4"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("partial SKU persisted: %v", err)
	}
	if _, err := store.DB().ExecContext(t.Context(), `DELETE FROM commerce_skus WHERE id=?`, conflict.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureSignageCatalog(t.Context()); err != nil {
		t.Fatalf("rollback did not release seed marker: %v", err)
	}
}
