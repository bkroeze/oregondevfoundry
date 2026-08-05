package commerce

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestUserPurchaseHistoryIncludesImmutableRecordsAndConfirmationsNewestFirst(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	userID := insertAccountTestUser(t, store, "history-user")
	now := time.Unix(1_700_000_000, 0).UTC()
	store.now = func() time.Time { return now }

	oneTimeSKU := createAccountTestSKU(t, store, ProductTypeOneTime, "history-ticket", nil)
	oldOrder := fulfillAccountTestOrder(t, store, userID, oneTimeSKU.ID, "cs_order_old", "Old Buyer", "old@example.com")
	now = now.Add(time.Minute)
	newOrder := fulfillAccountTestOrder(t, store, userID, oneTimeSKU.ID, "cs_order_new", "New Buyer", "new@example.com")
	now = now.Add(time.Minute)
	subscriptionSKU := createAccountTestSKU(t, store, ProductTypeSubscription, "history-membership", nil)
	subscription := fulfillAccountTestSubscription(t, store, userID, subscriptionSKU.ID, "cs_sub_history", "sub_history", "active", "Subscriber", "subscriber@example.com")

	history, err := store.PurchaseHistoryForUser(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if len(history.Orders) != 2 || history.Orders[0].Order.ID != newOrder.ID || history.Orders[1].Order.ID != oldOrder.ID {
		t.Fatalf("order history = %#v", history.Orders)
	}
	if got := history.Orders[0]; got.Order.StripeSessionID != "cs_order_new" || got.Order.BuyerEmail != "new@example.com" || got.Order.BuyerName != "New Buyer" || got.Order.TotalCents != 2500 || len(got.Order.Items) != 1 || got.ConfirmationCode == "" {
		t.Fatalf("new order snapshot = %#v", got)
	}
	if len(history.Subscriptions) != 1 {
		t.Fatalf("subscription history = %#v", history.Subscriptions)
	}
	if got := history.Subscriptions[0]; got.Subscription.ID != subscription.ID || got.Subscription.StripeSessionID != "cs_sub_history" || got.Subscription.StripeSubscriptionID != "sub_history" || got.Subscription.BuyerEmail != "subscriber@example.com" || got.Subscription.BuyerName != "Subscriber" || got.ProductName != "history-membership" || got.SKUName != "General" || got.ConfirmationCode == "" {
		t.Fatalf("subscription snapshot = %#v", got)
	}
}

func TestAccountCancellationTargetsAndMutations(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	userID := insertAccountTestUser(t, store, "cancel-user")
	otherUserID := insertAccountTestUser(t, store, "other-user")
	inventory := int64(5)
	oneTimeSKU := createAccountTestSKU(t, store, ProductTypeOneTime, "cancel-ticket", &inventory)

	cart, err := store.CreateCart(ctx, &userID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.AddCartItem(ctx, cart.Token, oneTimeSKU.ID, 2); err != nil {
		t.Fatal(err)
	}
	pending, err := store.CreateCheckout(ctx, CreateCheckoutParams{CartToken: cart.Token, UserID: &userID, BuyerEmail: "cancel@example.com", BuyerName: "Cancel Buyer"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.AttachStripeSession(ctx, pending.ID, "cs_pending_user"); err != nil {
		t.Fatal(err)
	}
	remaining, err := store.SKUByID(ctx, oneTimeSKU.ID)
	if err != nil || remaining.Inventory == nil || *remaining.Inventory != 3 {
		t.Fatalf("reserved inventory = %#v, %v", remaining.Inventory, err)
	}

	unattachedCart, err := store.CreateCart(ctx, &userID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.AddCartItem(ctx, unattachedCart.Token, oneTimeSKU.ID, 1); err != nil {
		t.Fatal(err)
	}
	unattached, err := store.CreateCheckout(ctx, CreateCheckoutParams{CartToken: unattachedCart.Token, UserID: &userID, BuyerEmail: "cancel@example.com", BuyerName: "Cancel Buyer"})
	if err != nil {
		t.Fatal(err)
	}
	otherCart, err := store.CreateCart(ctx, &otherUserID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.AddCartItem(ctx, otherCart.Token, oneTimeSKU.ID, 1); err != nil {
		t.Fatal(err)
	}
	otherCheckout, err := store.CreateCheckout(ctx, CreateCheckoutParams{CartToken: otherCart.Token, UserID: &otherUserID, BuyerEmail: "other@example.com", BuyerName: "Other Buyer"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.AttachStripeSession(ctx, otherCheckout.ID, "cs_pending_other"); err != nil {
		t.Fatal(err)
	}

	subscriptionSKU := createAccountTestSKU(t, store, ProductTypeSubscription, "cancel-membership", nil)
	active := fulfillAccountTestSubscription(t, store, userID, subscriptionSKU.ID, "cs_active", "sub_active", "active", "Cancel Buyer", "cancel@example.com")
	_ = fulfillAccountTestSubscription(t, store, userID, subscriptionSKU.ID, "cs_ended", "sub_ended", "incomplete_expired", "Cancel Buyer", "cancel@example.com")
	_ = fulfillAccountTestSubscription(t, store, otherUserID, subscriptionSKU.ID, "cs_other_sub", "sub_other", "active", "Other Buyer", "other@example.com")

	targets, err := store.CancellationTargetsForUser(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets.CheckoutSessionIDs) != 1 || targets.CheckoutSessionIDs[0] != "cs_pending_user" {
		t.Fatalf("checkout targets = %#v", targets.CheckoutSessionIDs)
	}
	if len(targets.SubscriptionIDs) != 1 || targets.SubscriptionIDs[0] != "sub_active" {
		t.Fatalf("subscription targets = %#v", targets.SubscriptionIDs)
	}

	if err = store.CancelExpiredCheckoutForUser(ctx, userID, "cs_pending_other"); err != nil {
		t.Fatalf("unowned checkout cancellation = %v", err)
	}
	if _, err = store.CheckoutByID(ctx, otherCheckout.ID); err != nil {
		t.Fatalf("unowned checkout changed: %v", err)
	}
	if err = store.CancelExpiredCheckoutForUser(ctx, userID, "cs_pending_user"); err != nil {
		t.Fatal(err)
	}
	if err = store.CancelExpiredCheckoutForUser(ctx, userID, "cs_pending_user"); err != nil {
		t.Fatalf("repeat checkout cancellation = %v", err)
	}
	if _, err = store.CheckoutByID(ctx, pending.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("canceled checkout error = %v", err)
	}
	remaining, err = store.SKUByID(ctx, oneTimeSKU.ID)
	if err != nil || remaining.Inventory == nil || *remaining.Inventory != 3 {
		t.Fatalf("released inventory = %#v, %v", remaining.Inventory, err)
	}
	if _, err = store.CheckoutByID(ctx, unattached.ID); err != nil {
		t.Fatalf("unattached checkout changed: %v", err)
	}

	if err = store.MarkSubscriptionCanceledForUser(ctx, userID, "sub_active"); err != nil {
		t.Fatal(err)
	}
	if err = store.MarkSubscriptionCanceledForUser(ctx, userID, "sub_active"); err != nil {
		t.Fatalf("repeat subscription cancellation = %v", err)
	}
	stored, err := store.SubscriptionByStripeID(ctx, "sub_active")
	if err != nil || stored.ID != active.ID || stored.Status != "canceled" || stored.BuyerEmail != "cancel@example.com" || stored.StripeSessionID != "cs_active" {
		t.Fatalf("stored canceled subscription = %#v, %v", stored, err)
	}
}

func TestFinalizeUserDeletionPreservesCommerceAndDeletesIdentity(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	userID := insertAccountTestUser(t, store, "delete-user")
	oneTimeSKU := createAccountTestSKU(t, store, ProductTypeOneTime, "delete-ticket", nil)
	order := fulfillAccountTestOrder(t, store, userID, oneTimeSKU.ID, "cs_delete_order", "Permanent Buyer", "permanent@example.com")
	subscriptionSKU := createAccountTestSKU(t, store, ProductTypeSubscription, "delete-membership", nil)
	subscription := fulfillAccountTestSubscription(t, store, userID, subscriptionSKU.ID, "cs_delete_sub", "sub_delete", "active", "Permanent Buyer", "permanent@example.com")
	if err := store.MarkSubscriptionCanceledForUser(ctx, userID, subscription.StripeSubscriptionID); err != nil {
		t.Fatal(err)
	}
	openCart, err := store.CreateCart(ctx, &userID, nil)
	if err != nil {
		t.Fatal(err)
	}
	trackedInventory := int64(1)
	trackedSKU := createAccountTestSKU(t, store, ProductTypeOneTime, "delete-reserved", &trackedInventory)
	reservedCart, err := store.CreateCart(ctx, &userID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.AddCartItem(ctx, reservedCart.Token, trackedSKU.ID, 1); err != nil {
		t.Fatal(err)
	}
	reservedCheckout, err := store.CreateCheckout(ctx, CreateCheckoutParams{CartToken: reservedCart.Token, UserID: &userID, BuyerEmail: "permanent@example.com", BuyerName: "Permanent Buyer"})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.AbandonOpenCartsForUser(ctx, userID); err != nil {
		t.Fatal(err)
	}
	if err = store.AbandonOpenCartsForUser(ctx, userID); err != nil {
		t.Fatalf("repeat cart abandonment = %v", err)
	}

	if err = store.FinalizeUserDeletion(ctx, userID); err != nil {
		t.Fatal(err)
	}
	if err = store.FinalizeUserDeletion(ctx, userID); err != nil {
		t.Fatalf("repeat finalization = %v", err)
	}
	if _, err = store.CheckoutByID(ctx, reservedCheckout.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unattached checkout error = %v", err)
	}
	restored, err := store.SKUByID(ctx, trackedSKU.ID)
	if err != nil || restored.Inventory == nil || *restored.Inventory != 1 {
		t.Fatalf("restored account-deletion inventory = %#v, %v", restored.Inventory, err)
	}
	for _, table := range []string{"users", "auth_identities", "sessions"} {
		var count int
		if err = store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table+` WHERE `+map[string]string{"users": "id", "auth_identities": "user_id", "sessions": "user_id"}[table]+`=?`, userID).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s rows = %d, %v", table, count, err)
		}
	}
	preservedOrder, err := store.OrderByID(ctx, order.ID)
	if err != nil || preservedOrder.UserID != nil || preservedOrder.StripeSessionID != "cs_delete_order" || preservedOrder.BuyerEmail != "permanent@example.com" || preservedOrder.BuyerName != "Permanent Buyer" || preservedOrder.TotalCents != 2500 || len(preservedOrder.Items) != 1 {
		t.Fatalf("preserved order = %#v, %v", preservedOrder, err)
	}
	preservedSubscription, err := store.SubscriptionByID(ctx, subscription.ID)
	if err != nil || preservedSubscription.UserID != nil || preservedSubscription.Status != "canceled" || preservedSubscription.StripeSubscriptionID != "sub_delete" || preservedSubscription.BuyerEmail != "permanent@example.com" || preservedSubscription.BuyerName != "Permanent Buyer" {
		t.Fatalf("preserved subscription = %#v, %v", preservedSubscription, err)
	}
	var cartStatus string
	var cartUserID *int64
	if err = store.db.QueryRowContext(ctx, `SELECT status,user_id FROM commerce_carts WHERE id=?`, openCart.ID).Scan(&cartStatus, &cartUserID); err != nil || cartStatus != "abandoned" || cartUserID != nil {
		t.Fatalf("preserved cart = status %q user %v, %v", cartStatus, cartUserID, err)
	}
}

func TestFinalizeUserDeletionRefusesActiveTargets(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	userID := insertAccountTestUser(t, store, "blocked-user")
	oneTimeSKU := createAccountTestSKU(t, store, ProductTypeOneTime, "blocked-ticket", nil)
	cart, err := store.CreateCart(ctx, &userID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.AddCartItem(ctx, cart.Token, oneTimeSKU.ID, 1); err != nil {
		t.Fatal(err)
	}
	checkout, err := store.CreateCheckout(ctx, CreateCheckoutParams{CartToken: cart.Token, UserID: &userID, BuyerEmail: "blocked@example.com", BuyerName: "Blocked Buyer"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.AttachStripeSession(ctx, checkout.ID, "cs_blocked"); err != nil {
		t.Fatal(err)
	}
	subscriptionSKU := createAccountTestSKU(t, store, ProductTypeSubscription, "blocked-membership", nil)
	_ = fulfillAccountTestSubscription(t, store, userID, subscriptionSKU.ID, "cs_blocked_sub", "sub_blocked", "past_due", "Blocked Buyer", "blocked@example.com")

	if err = store.FinalizeUserDeletion(ctx, userID); !errors.Is(err, ErrCancellationRequired) {
		t.Fatalf("finalization error = %v", err)
	}
	if err = store.CancelExpiredCheckoutForUser(ctx, userID, "cs_blocked"); err != nil {
		t.Fatal(err)
	}
	if err = store.FinalizeUserDeletion(ctx, userID); !errors.Is(err, ErrCancellationRequired) {
		t.Fatalf("active-subscription finalization error = %v", err)
	}
	var userCount int
	if err = store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE id=?`, userID).Scan(&userCount); err != nil || userCount != 1 {
		t.Fatalf("user rows = %d, %v", userCount, err)
	}
	preservedCart, err := store.CartByToken(ctx, cart.Token)
	if err != nil || preservedCart.Status != "open" {
		t.Fatalf("rolled-back cart = %#v, %v", preservedCart, err)
	}
}

func insertAccountTestUser(t *testing.T, store *Store, username string) int64 {
	t.Helper()
	result, err := store.db.Exec(`INSERT INTO users(username,email,display_name,role,created_at,updated_at) VALUES(?,?,'Account User','user',1,1)`, username, username+"@example.com")
	if err != nil {
		t.Fatal(err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.db.Exec(`INSERT INTO auth_identities(user_id,provider,subject,credential_hash) VALUES(?,'password',?,'hash')`, id, username); err != nil {
		t.Fatal(err)
	}
	if _, err = store.db.Exec(`INSERT INTO sessions(token_hash,user_id,expires_at,created_at) VALUES(?,?,9999999999,1)`, []byte("session-"+username), id); err != nil {
		t.Fatal(err)
	}
	return id
}

func createAccountTestSKU(t *testing.T, store *Store, productType ProductType, slug string, inventory *int64) SKU {
	t.Helper()
	ctx := context.Background()
	product, err := store.CreateProduct(ctx, ProductParams{Slug: slug, MasterSKU: slug, Name: slug, Type: productType, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	sku, err := store.CreateSKU(ctx, SKUParams{ProductID: product.ID, SKU: slug + "-sku", Name: "General", Kind: SKUKindPrimary, PriceCents: 2500, Inventory: inventory, Active: true, StripePriceID: "price_" + slug})
	if err != nil {
		t.Fatal(err)
	}
	return sku
}

func fulfillAccountTestOrder(t *testing.T, store *Store, userID, skuID int64, sessionID, buyerName, buyerEmail string) Order {
	t.Helper()
	ctx := context.Background()
	cart, err := store.CreateCart(ctx, &userID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.AddCartItem(ctx, cart.Token, skuID, 1); err != nil {
		t.Fatal(err)
	}
	checkout, err := store.CreateCheckout(ctx, CreateCheckoutParams{CartToken: cart.Token, UserID: &userID, BuyerEmail: buyerEmail, BuyerName: buyerName})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.AttachStripeSession(ctx, checkout.ID, sessionID); err != nil {
		t.Fatal(err)
	}
	result, err := store.Fulfill(ctx, FulfillParams{StripeSessionID: sessionID})
	if err != nil {
		t.Fatal(err)
	}
	return *result.Order
}

func fulfillAccountTestSubscription(t *testing.T, store *Store, userID, skuID int64, sessionID, subscriptionID, status, buyerName, buyerEmail string) Subscription {
	t.Helper()
	ctx := context.Background()
	checkout, err := store.CreateCheckout(ctx, CreateCheckoutParams{StandaloneSKUID: skuID, UserID: &userID, BuyerEmail: buyerEmail, BuyerName: buyerName})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.AttachStripeSession(ctx, checkout.ID, sessionID); err != nil {
		t.Fatal(err)
	}
	result, err := store.Fulfill(ctx, FulfillParams{StripeSessionID: sessionID, StripeSubscriptionID: subscriptionID, SubscriptionStatus: status})
	if err != nil {
		t.Fatal(err)
	}
	return *result.Subscription
}
