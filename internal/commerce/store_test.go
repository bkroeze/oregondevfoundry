package commerce

import (
	"context"
	"errors"
	"testing"

	"github.com/bkroeze/oregon-dev-foundry/internal/auth"
)

func newTestStore(t *testing.T) (*Store, *auth.Store) {
	t.Helper()
	authStore, err := auth.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = authStore.Close() })
	store, err := New(authStore.DB())
	if err != nil {
		t.Fatal(err)
	}
	store, err = New(authStore.DB())
	if err != nil {
		t.Fatalf("repeat migration: %v", err)
	}
	return store, authStore
}

func TestOneTimeFulfillmentIsIdempotentAndSnapshotsInventory(t *testing.T) {
	store, authStore := newTestStore(t)
	ctx := context.Background()
	inventory := int64(2)
	product, err := store.CreateProduct(ctx, ProductParams{Slug: "workshop", MasterSKU: "WORKSHOP", Name: "Workshop", Type: ProductTypeOneTime, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	sku, err := store.CreateSKU(ctx, SKUParams{ProductID: product.ID, SKU: "WORKSHOP-GENERAL", Name: "General", Kind: SKUKindPrimary, OptionsJSON: `{"level":"general"}`, PriceCents: 2500, Inventory: &inventory, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	shipping, err := store.CreateShippingOption(ctx, ShippingOptionParams{ProductID: product.ID, Name: "Fixed", AmountCents: 500, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	cart, err := store.CreateCart(ctx, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.AddCartItem(ctx, cart.Token, sku.ID, 2); err != nil {
		t.Fatal(err)
	}
	if _, err = store.CreateCheckout(ctx, CreateCheckoutParams{CartToken: cart.Token, BuyerEmail: "buyer@example.com", BuyerName: "Buyer"}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("missing shipping selection error = %v", err)
	}
	checkout, err := store.CreateCheckout(ctx, CreateCheckoutParams{CartToken: cart.Token, BuyerEmail: "buyer@example.com", BuyerName: "Buyer", CreateAccount: true, ShippingOptionIDs: map[int64]int64{product.ID: shipping.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.CreateCheckout(ctx, CreateCheckoutParams{CartToken: cart.Token, BuyerEmail: "other@example.com", BuyerName: "Other"}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("duplicate cart checkout error = %v", err)
	}
	if checkout.ShippingAmountCents != 500 || len(checkout.ShippingOptions) != 1 {
		t.Fatalf("shipping snapshot = %#v", checkout)
	}
	if _, err = store.AttachStripeSession(ctx, checkout.ID, "cs_test_one"); err != nil {
		t.Fatal(err)
	}
	first, err := store.Fulfill(ctx, FulfillParams{StripeSessionID: "cs_test_one"})
	if err != nil {
		t.Fatal(err)
	}
	if !first.NewlyCreated || first.Order == nil || first.InvitationToken == "" || first.InvitedUserID == nil {
		t.Fatalf("first fulfillment = %#v", first)
	}
	if first.Order.TotalCents != 5500 {
		t.Fatalf("total = %d", first.Order.TotalCents)
	}
	second, err := store.Fulfill(ctx, FulfillParams{StripeSessionID: "cs_test_one"})
	if err != nil {
		t.Fatal(err)
	}
	if second.NewlyCreated || second.InvitationToken != "" || second.Order == nil || second.Order.ID != first.Order.ID {
		t.Fatalf("second fulfillment = %#v", second)
	}
	remaining, err := store.SKUByID(ctx, sku.ID)
	if err != nil {
		t.Fatal(err)
	}
	if remaining.Inventory == nil || *remaining.Inventory != 0 {
		t.Fatalf("inventory = %v", remaining.Inventory)
	}
	claimed, err := store.ClaimInvitationDelivery(ctx, *first.InvitedUserID)
	if err != nil || !claimed {
		t.Fatalf("claim invitation = %v, %v", claimed, err)
	}
	claimedToken, err := store.IssueClaimedInvitationToken(ctx, *first.InvitedUserID)
	if err != nil || claimedToken == first.InvitationToken {
		t.Fatalf("claimed invitation token = %q, %v", claimedToken, err)
	}
	if err = store.ReleaseInvitationDelivery(ctx, *first.InvitedUserID); err != nil {
		t.Fatal(err)
	}
	reissued, err := store.IssueInvitationToken(ctx, *first.InvitedUserID)
	if err != nil || reissued == claimedToken {
		t.Fatalf("reissued invitation = %q, %v", reissued, err)
	}
	if _, err = authStore.CompleteInvitation(ctx, first.InvitationToken, []byte("a-secure-password")); !errors.Is(err, auth.ErrInvitationNotFound) {
		t.Fatalf("rotated invitation error = %v", err)
	}
	if _, err = authStore.CompleteInvitation(ctx, reissued, []byte("a-secure-password")); err != nil {
		t.Fatal(err)
	}
	if _, err = authStore.CompleteInvitation(ctx, reissued, []byte("a-secure-password")); !errors.Is(err, auth.ErrInvitationUsed) {
		t.Fatalf("reused invitation error = %v", err)
	}
}

func TestPurchaseAccountMatchesEmailBeforeLegacyUsername(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	legacyResult, err := store.db.ExecContext(ctx, `INSERT INTO users(username,email,display_name,role,created_at,updated_at) VALUES('buyer@example.com','changed@example.com','Legacy Username','user',1,1)`)
	if err != nil {
		t.Fatal(err)
	}
	legacyID, _ := legacyResult.LastInsertId()
	ownerResult, err := store.db.ExecContext(ctx, `INSERT INTO users(username,email,display_name,role,created_at,updated_at) VALUES('email.owner','buyer@example.com','Email Owner','user',1,1)`)
	if err != nil {
		t.Fatal(err)
	}
	ownerID, _ := ownerResult.LastInsertId()
	product, err := store.CreateProduct(ctx, ProductParams{Slug: "identity-match", MasterSKU: "IDENTITY-MATCH", Name: "Identity match", Type: ProductTypeOneTime, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	sku, err := store.CreateSKU(ctx, SKUParams{ProductID: product.ID, SKU: "IDENTITY-MATCH-SKU", Name: "General", Kind: SKUKindPrimary, PriceCents: 1000, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	cart, err := store.CreateCart(ctx, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.AddCartItem(ctx, cart.Token, sku.ID, 1); err != nil {
		t.Fatal(err)
	}
	checkout, err := store.CreateCheckout(ctx, CreateCheckoutParams{CartToken: cart.Token, BuyerEmail: "buyer@example.com", BuyerName: "Buyer", CreateAccount: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.AttachStripeSession(ctx, checkout.ID, "cs_identity_match"); err != nil {
		t.Fatal(err)
	}
	result, err := store.Fulfill(ctx, FulfillParams{StripeSessionID: "cs_identity_match"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Order == nil || result.Order.UserID == nil || *result.Order.UserID != ownerID || *result.Order.UserID == legacyID {
		t.Fatalf("fulfilled order user = %#v, legacy=%d owner=%d", result.Order, legacyID, ownerID)
	}
}

func TestSubscriptionsNeverEnterCartOrMutateOne(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	product, err := store.CreateProduct(ctx, ProductParams{Slug: "membership", MasterSKU: "MEMBERSHIP", Name: "Membership", Type: ProductTypeSubscription, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	inventory := int64(1)
	sku, err := store.CreateSKU(ctx, SKUParams{ProductID: product.ID, SKU: "MEMBERSHIP-MONTHLY", Name: "Monthly", Kind: SKUKindPrimary, PriceCents: 1000, Inventory: &inventory, Active: true, StripePriceID: "price_monthly"})
	if err != nil {
		t.Fatal(err)
	}
	cart, err := store.CreateCart(ctx, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.AddCartItem(ctx, cart.Token, sku.ID, 1); !errors.Is(err, ErrSubscriptionInCart) {
		t.Fatalf("subscription cart error = %v", err)
	}
	checkout, err := store.CreateCheckout(ctx, CreateCheckoutParams{StandaloneSKUID: sku.ID, BuyerEmail: "member@example.com", BuyerName: "Member"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.AttachStripeSession(ctx, checkout.ID, "cs_test_sub"); err != nil {
		t.Fatal(err)
	}
	result, err := store.Fulfill(ctx, FulfillParams{StripeSessionID: "cs_test_sub", StripeSubscriptionID: "sub_test", SubscriptionStatus: "active"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Subscription == nil || result.Order != nil {
		t.Fatalf("subscription fulfillment = %#v", result)
	}
	unchanged, err := store.CartByToken(ctx, cart.Token)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Status != "open" {
		t.Fatalf("cart status = %s", unchanged.Status)
	}
	remaining, err := store.SKUByID(ctx, sku.ID)
	if err != nil {
		t.Fatal(err)
	}
	if remaining.Inventory == nil || *remaining.Inventory != 0 {
		t.Fatalf("subscription inventory = %v", remaining.Inventory)
	}
}

func TestWebhookClaimCanRetryAfterFailure(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	claimed, err := store.ClaimWebhookEvent(ctx, "evt_1", "checkout.session.completed")
	if err != nil || !claimed {
		t.Fatalf("first claim = %v, %v", claimed, err)
	}
	claimed, err = store.ClaimWebhookEvent(ctx, "evt_1", "checkout.session.completed")
	if !errors.Is(err, ErrWebhookInProgress) || claimed {
		t.Fatalf("concurrent claim = %v, %v", claimed, err)
	}
	if err = store.ReleaseWebhookEvent(ctx, "evt_1", errors.New("mail failed")); err != nil {
		t.Fatal(err)
	}
	claimed, err = store.ClaimWebhookEvent(ctx, "evt_1", "checkout.session.completed")
	if err != nil || !claimed {
		t.Fatalf("retry claim = %v, %v", claimed, err)
	}
	if err = store.CompleteWebhookEvent(ctx, "evt_1"); err != nil {
		t.Fatal(err)
	}
	claimed, err = store.ClaimWebhookEvent(ctx, "evt_1", "checkout.session.completed")
	if err != nil || claimed {
		t.Fatalf("completed claim = %v, %v", claimed, err)
	}
}
