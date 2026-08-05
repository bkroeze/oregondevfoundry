package commerce

import (
	"errors"
	"testing"
	"time"
)

func TestCheckoutReservesAndCancellationReleasesInventory(t *testing.T) {
	store, _ := newTestStore(t)
	stock := int64(1)
	product, err := store.CreateProduct(t.Context(), ProductParams{Slug: "limited", MasterSKU: "LIMITED", Name: "Limited", Type: ProductTypeOneTime, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	sku, err := store.CreateSKU(t.Context(), SKUParams{ProductID: product.ID, SKU: "LIMITED-ONE", Name: "One", Kind: SKUKindPrimary, PriceCents: 1000, Inventory: &stock, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	firstCart, err := store.CreateCart(t.Context(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	secondCart, err := store.CreateCart(t.Context(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.AddCartItem(t.Context(), firstCart.Token, sku.ID, 1); err != nil {
		t.Fatal(err)
	}
	if _, err = store.AddCartItem(t.Context(), secondCart.Token, sku.ID, 1); err != nil {
		t.Fatal(err)
	}
	firstCheckout, err := store.CreateCheckout(t.Context(), CreateCheckoutParams{CartToken: firstCart.Token, BuyerEmail: "first@example.com", BuyerName: "First"})
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func() error{
		"add": func() error {
			_, mutationErr := store.AddCartSelection(t.Context(), firstCart.Token, CartSelectionParams{PrimarySKUID: sku.ID, Quantity: 1})
			return mutationErr
		},
		"update": func() error {
			_, mutationErr := store.UpdateCartItem(t.Context(), firstCart.Token, sku.ID, 2)
			return mutationErr
		},
		"remove": func() error {
			_, mutationErr := store.RemoveCartItem(t.Context(), firstCart.Token, sku.ID)
			return mutationErr
		},
	} {
		if err := mutate(); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("%s pending cart error = %v", name, err)
		}
	}
	frozen, err := store.CartByToken(t.Context(), firstCart.Token)
	if err != nil || len(frozen.Items) != 1 || frozen.Items[0].Quantity != 1 {
		t.Fatalf("frozen cart = %#v, %v", frozen, err)
	}
	if _, err = store.CreateCheckout(t.Context(), CreateCheckoutParams{CartToken: secondCart.Token, BuyerEmail: "second@example.com", BuyerName: "Second"}); !errors.Is(err, ErrSoldOut) {
		t.Fatalf("unreserved checkout error = %v", err)
	}
	reserved, err := store.SKUByID(t.Context(), sku.ID)
	if err != nil || reserved.Inventory == nil || *reserved.Inventory != 0 {
		t.Fatalf("reserved inventory = %#v, %v", reserved.Inventory, err)
	}
	if err = store.CancelPendingCheckout(t.Context(), firstCheckout.ID); err != nil {
		t.Fatal(err)
	}
	available, err := store.SKUByID(t.Context(), sku.ID)
	if err != nil || available.Inventory == nil || *available.Inventory != 1 {
		t.Fatalf("released inventory = %#v, %v", available.Inventory, err)
	}
	secondCheckout, err := store.CreateCheckout(t.Context(), CreateCheckoutParams{CartToken: secondCart.Token, BuyerEmail: "second@example.com", BuyerName: "Second"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.AttachStripeSession(t.Context(), secondCheckout.ID, "cs_expired"); err != nil {
		t.Fatal(err)
	}
	if err = store.CancelExpiredCheckout(t.Context(), secondCheckout.ID, "cs_expired"); err != nil {
		t.Fatal(err)
	}
	available, err = store.SKUByID(t.Context(), sku.ID)
	if err != nil || available.Inventory == nil || *available.Inventory != 1 {
		t.Fatalf("expired checkout inventory = %#v, %v", available.Inventory, err)
	}
}

func TestConcurrentCheckoutsCannotReserveTheSameInventory(t *testing.T) {
	store, _ := newTestStore(t)
	stock := int64(1)
	product, err := store.CreateProduct(t.Context(), ProductParams{Slug: "last-one", MasterSKU: "LAST-ONE", Name: "Last One", Type: ProductTypeOneTime, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	sku, err := store.CreateSKU(t.Context(), SKUParams{ProductID: product.ID, SKU: "LAST-ONE-SKU", Name: "Last One", Kind: SKUKindPrimary, PriceCents: 1000, Inventory: &stock, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	carts := make([]Cart, 2)
	for i := range carts {
		carts[i], err = store.CreateCart(t.Context(), nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = store.AddCartItem(t.Context(), carts[i].Token, sku.ID, 1); err != nil {
			t.Fatal(err)
		}
	}
	start := make(chan struct{})
	results := make(chan error, len(carts))
	for _, cart := range carts {
		go func() {
			<-start
			_, checkoutErr := store.CreateCheckout(t.Context(), CreateCheckoutParams{CartToken: cart.Token, BuyerEmail: "buyer@example.com", BuyerName: "Buyer"})
			results <- checkoutErr
		}()
	}
	close(start)
	var succeeded, soldOut int
	for range carts {
		switch err := <-results; {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrSoldOut):
			soldOut++
		default:
			t.Fatalf("concurrent checkout error = %v", err)
		}
	}
	if succeeded != 1 || soldOut != 1 {
		t.Fatalf("concurrent results: succeeded=%d sold_out=%d", succeeded, soldOut)
	}
	remaining, err := store.SKUByID(t.Context(), sku.ID)
	if err != nil || remaining.Inventory == nil || *remaining.Inventory != 0 {
		t.Fatalf("remaining inventory = %#v, %v", remaining.Inventory, err)
	}
}

func TestCheckoutCancellationRestoresOnlyReservedInventory(t *testing.T) {
	store, _ := newTestStore(t)
	product, err := store.CreateProduct(t.Context(), ProductParams{Slug: "untracked", MasterSKU: "UNTRACKED", Name: "Untracked", Type: ProductTypeOneTime, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	skuParams := SKUParams{ProductID: product.ID, SKU: "UNTRACKED-SKU", Name: "Untracked", Kind: SKUKindPrimary, PriceCents: 1000, Active: true}
	sku, err := store.CreateSKU(t.Context(), skuParams)
	if err != nil {
		t.Fatal(err)
	}
	cart, err := store.CreateCart(t.Context(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.AddCartItem(t.Context(), cart.Token, sku.ID, 1); err != nil {
		t.Fatal(err)
	}
	checkout, err := store.CreateCheckout(t.Context(), CreateCheckoutParams{CartToken: cart.Token, BuyerEmail: "buyer@example.com", BuyerName: "Buyer"})
	if err != nil {
		t.Fatal(err)
	}
	tracked := int64(10)
	skuParams.Inventory = &tracked
	if _, err = store.UpdateSKU(t.Context(), sku.ID, skuParams); err != nil {
		t.Fatal(err)
	}
	if err = store.CancelPendingCheckout(t.Context(), checkout.ID); err != nil {
		t.Fatal(err)
	}
	remaining, err := store.SKUByID(t.Context(), sku.ID)
	if err != nil || remaining.Inventory == nil || *remaining.Inventory != tracked {
		t.Fatalf("inventory after cancellation = %#v, %v", remaining.Inventory, err)
	}
}

func TestStaleUnattachedCheckoutCleanupReleasesInventory(t *testing.T) {
	store, _ := newTestStore(t)
	now := time.Date(2026, time.July, 21, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	stock := int64(1)
	product, err := store.CreateProduct(t.Context(), ProductParams{Slug: "stale", MasterSKU: "STALE", Name: "Stale", Type: ProductTypeSubscription, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	sku, err := store.CreateSKU(t.Context(), SKUParams{ProductID: product.ID, SKU: "STALE-MONTHLY", Name: "Monthly", Kind: SKUKindPrimary, PriceCents: 1000, Inventory: &stock, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	checkout, err := store.CreateCheckout(t.Context(), CreateCheckoutParams{StandaloneSKUID: sku.ID, BuyerEmail: "buyer@example.com", BuyerName: "Buyer"})
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(unattachedCheckoutLease + time.Second)
	if err = store.CancelStalePendingCheckouts(t.Context(), unattachedCheckoutLease); err != nil {
		t.Fatal(err)
	}
	if _, err = store.CheckoutByID(t.Context(), checkout.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stale checkout error = %v", err)
	}
	remaining, err := store.SKUByID(t.Context(), sku.ID)
	if err != nil || remaining.Inventory == nil || *remaining.Inventory != 1 {
		t.Fatalf("stale checkout inventory = %#v, %v", remaining.Inventory, err)
	}
}
