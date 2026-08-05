package commerce

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// ErrCancellationRequired means Stripe-backed resources must be canceled before
// the local user record can be deleted.
var ErrCancellationRequired = errors.New("account cancellation is required")

// UserOrderHistory pairs an immutable order with its booking confirmation.
type UserOrderHistory struct {
	Order            Order
	ConfirmationCode string
}

// UserSubscriptionHistory pairs an immutable subscription with its booking
// confirmation and the current catalog names for the purchased product/SKU.
type UserSubscriptionHistory struct {
	Subscription     Subscription
	ProductName      string
	SKUName          string
	ConfirmationCode string
}

// UserPurchaseHistory contains the immutable commerce records associated with
// a user. Both slices are ordered newest-first.
type UserPurchaseHistory struct {
	Orders        []UserOrderHistory
	Subscriptions []UserSubscriptionHistory
}

// AccountCancellationTargets identifies Stripe resources which must be ended
// before the local account can be deleted.
type AccountCancellationTargets struct {
	CheckoutSessionIDs []string
	SubscriptionIDs    []string
}

// PurchaseHistoryForUser returns completed orders and subscriptions associated
// with userID, including their immutable buyer and payment identifiers.
func (s *Store) PurchaseHistoryForUser(ctx context.Context, userID int64) (UserPurchaseHistory, error) {
	if userID <= 0 {
		return UserPurchaseHistory{}, fmt.Errorf("%w: user is required", ErrInvalidInput)
	}

	orderIDs, err := queryIDs(ctx, s.db, `SELECT id FROM commerce_orders WHERE user_id=? ORDER BY created_at DESC,id DESC`, userID)
	if err != nil {
		return UserPurchaseHistory{}, fmt.Errorf("list user orders: %w", err)
	}
	subscriptionIDs, err := queryIDs(ctx, s.db, `SELECT id FROM commerce_subscriptions WHERE user_id=? ORDER BY created_at DESC,id DESC`, userID)
	if err != nil {
		return UserPurchaseHistory{}, fmt.Errorf("list user subscriptions: %w", err)
	}

	history := UserPurchaseHistory{
		Orders:        make([]UserOrderHistory, 0, len(orderIDs)),
		Subscriptions: make([]UserSubscriptionHistory, 0, len(subscriptionIDs)),
	}
	for _, id := range orderIDs {
		order, err := s.orderByID(ctx, s.db, id)
		if err != nil {
			return UserPurchaseHistory{}, fmt.Errorf("load user order %d: %w", id, err)
		}
		var confirmationCode string
		if err := s.db.QueryRowContext(ctx, `SELECT code FROM commerce_booking_confirmations WHERE order_id=?`, id).Scan(&confirmationCode); err != nil {
			return UserPurchaseHistory{}, fmt.Errorf("load user order %d confirmation: %w", id, err)
		}
		history.Orders = append(history.Orders, UserOrderHistory{Order: order, ConfirmationCode: confirmationCode})
	}
	for _, id := range subscriptionIDs {
		subscription, err := s.subscriptionByID(ctx, s.db, id)
		if err != nil {
			return UserPurchaseHistory{}, fmt.Errorf("load user subscription %d: %w", id, err)
		}
		var confirmationCode, productName, skuName string
		if err := s.db.QueryRowContext(ctx, `SELECT c.code,p.name,s.name FROM commerce_booking_confirmations c JOIN commerce_products p ON p.id=? JOIN commerce_skus s ON s.id=? WHERE c.subscription_id=?`, subscription.ProductID, subscription.SKUID, id).Scan(&confirmationCode, &productName, &skuName); err != nil {
			return UserPurchaseHistory{}, fmt.Errorf("load user subscription %d confirmation: %w", id, err)
		}
		history.Subscriptions = append(history.Subscriptions, UserSubscriptionHistory{
			Subscription:     subscription,
			ProductName:      productName,
			SKUName:          skuName,
			ConfirmationCode: confirmationCode,
		})
	}
	return history, nil
}

// CancellationTargetsForUser returns pending attached Checkout sessions and
// non-terminal subscriptions. Each slice is ordered newest-first.
func (s *Store) CancellationTargetsForUser(ctx context.Context, userID int64) (AccountCancellationTargets, error) {
	if userID <= 0 {
		return AccountCancellationTargets{}, fmt.Errorf("%w: user is required", ErrInvalidInput)
	}

	targets := AccountCancellationTargets{
		CheckoutSessionIDs: make([]string, 0),
		SubscriptionIDs:    make([]string, 0),
	}
	rows, err := s.db.QueryContext(ctx, `SELECT stripe_session_id FROM commerce_checkouts WHERE user_id=? AND status='pending' AND stripe_session_id IS NOT NULL AND stripe_session_id<>'' ORDER BY created_at DESC,id DESC`, userID)
	if err != nil {
		return AccountCancellationTargets{}, fmt.Errorf("list pending user checkouts: %w", err)
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return AccountCancellationTargets{}, fmt.Errorf("scan pending user checkout: %w", err)
		}
		targets.CheckoutSessionIDs = append(targets.CheckoutSessionIDs, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return AccountCancellationTargets{}, fmt.Errorf("read pending user checkouts: %w", err)
	}
	if err := rows.Close(); err != nil {
		return AccountCancellationTargets{}, fmt.Errorf("close pending user checkouts: %w", err)
	}

	rows, err = s.db.QueryContext(ctx, `SELECT stripe_subscription_id FROM commerce_subscriptions WHERE user_id=? AND status NOT IN ('canceled','incomplete_expired') ORDER BY created_at DESC,id DESC`, userID)
	if err != nil {
		return AccountCancellationTargets{}, fmt.Errorf("list active user subscriptions: %w", err)
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return AccountCancellationTargets{}, fmt.Errorf("scan active user subscription: %w", err)
		}
		targets.SubscriptionIDs = append(targets.SubscriptionIDs, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return AccountCancellationTargets{}, fmt.Errorf("read active user subscriptions: %w", err)
	}
	if err := rows.Close(); err != nil {
		return AccountCancellationTargets{}, fmt.Errorf("close active user subscriptions: %w", err)
	}
	return targets, nil
}

// CancelExpiredCheckoutForUser releases a checkout's reservation after its
// attached Stripe Checkout session has expired. Repeated calls are harmless.
func (s *Store) CancelExpiredCheckoutForUser(ctx context.Context, userID int64, sessionID string) error {
	sessionID = strings.TrimSpace(sessionID)
	if userID <= 0 || sessionID == "" {
		return fmt.Errorf("%w: user and Stripe session are required", ErrInvalidInput)
	}
	var checkoutID int64
	err := s.db.QueryRowContext(ctx, `SELECT id FROM commerce_checkouts WHERE user_id=? AND stripe_session_id=? AND status='pending'`, userID, sessionID).Scan(&checkoutID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("find expired user checkout: %w", err)
	}
	if err := s.CancelExpiredCheckout(ctx, checkoutID, sessionID); err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	return nil
}

// MarkSubscriptionCanceledForUser records Stripe's canceled state without
// deleting the immutable subscription record. Repeated calls are harmless.
func (s *Store) MarkSubscriptionCanceledForUser(ctx context.Context, userID int64, subscriptionID string) error {
	subscriptionID = strings.TrimSpace(subscriptionID)
	if userID <= 0 || subscriptionID == "" {
		return fmt.Errorf("%w: user and Stripe subscription are required", ErrInvalidInput)
	}
	result, err := s.db.ExecContext(ctx, `UPDATE commerce_subscriptions SET status='canceled',updated_at=? WHERE user_id=? AND stripe_subscription_id=?`, nowUnix(s), userID, subscriptionID)
	if err != nil {
		return fmt.Errorf("mark user subscription canceled: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read canceled subscription rows: %w", err)
	}
	if changed == 0 {
		return ErrNotFound
	}
	return nil
}

// AbandonOpenCartsForUser marks all of a user's open carts abandoned.
func (s *Store) AbandonOpenCartsForUser(ctx context.Context, userID int64) error {
	if userID <= 0 {
		return fmt.Errorf("%w: user is required", ErrInvalidInput)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE commerce_carts SET status='abandoned',updated_at=? WHERE user_id=? AND status='open'`, nowUnix(s), userID); err != nil {
		return fmt.Errorf("abandon user carts: %w", err)
	}
	return nil
}

// FinalizeUserDeletion deletes the shared user identity only after every
// Stripe-backed cancellation target has been cleared. Foreign-key SET NULL
// actions preserve completed commerce and payment records.
func (s *Store) FinalizeUserDeletion(ctx context.Context, userID int64) error {
	if userID <= 0 {
		return fmt.Errorf("%w: user is required", ErrInvalidInput)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin user deletion: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `UPDATE commerce_carts SET status='abandoned',updated_at=? WHERE user_id=? AND status='open'`, nowUnix(s), userID); err != nil {
		return fmt.Errorf("abandon user carts: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE commerce_skus
SET inventory_count=inventory_count+(
    SELECT COALESCE(SUM(ci.reserved_quantity),0)
    FROM commerce_checkout_items ci
    JOIN commerce_checkouts c ON c.id=ci.checkout_id
    WHERE c.user_id=? AND c.status='pending' AND c.stripe_session_id IS NULL
      AND ci.sku_id=commerce_skus.id
),updated_at=?
WHERE inventory_count IS NOT NULL AND EXISTS (
    SELECT 1
    FROM commerce_checkout_items ci
    JOIN commerce_checkouts c ON c.id=ci.checkout_id
    WHERE c.user_id=? AND c.status='pending' AND c.stripe_session_id IS NULL
      AND ci.sku_id=commerce_skus.id AND ci.reserved_quantity>0
)`, userID, nowUnix(s), userID); err != nil {
		return fmt.Errorf("release unattached user checkout inventory: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM commerce_checkouts WHERE user_id=? AND status='pending' AND stripe_session_id IS NULL`, userID); err != nil {
		return fmt.Errorf("delete unattached user checkouts: %w", err)
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM users
WHERE id=?
  AND NOT EXISTS (
    SELECT 1 FROM commerce_checkouts
    WHERE user_id=? AND status='pending' AND stripe_session_id IS NOT NULL AND stripe_session_id<>''
  )
  AND NOT EXISTS (
    SELECT 1 FROM commerce_subscriptions
    WHERE user_id=? AND status NOT IN ('canceled','incomplete_expired')
  )`, userID, userID, userID)
	if err != nil {
		return fmt.Errorf("delete user: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read deleted user rows: %w", err)
	}
	if changed == 0 {
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=?)`, userID).Scan(&exists); err != nil {
			return fmt.Errorf("check user deletion: %w", err)
		}
		if exists {
			return ErrCancellationRequired
		}
		return nil
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit user deletion: %w", err)
	}
	return nil
}

func queryIDs(ctx context.Context, q queryer, query string, args ...any) ([]int64, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, 0)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	return ids, nil
}
