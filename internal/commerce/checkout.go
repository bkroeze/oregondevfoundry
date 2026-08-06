package commerce

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

const unattachedCheckoutLease = 10 * time.Minute

func (s *Store) CreateCheckout(ctx context.Context, p CreateCheckoutParams) (Checkout, error) {
	p.CartToken = strings.TrimSpace(p.CartToken)
	p.StripeSessionID = strings.TrimSpace(p.StripeSessionID)
	p.BuyerName = strings.TrimSpace(p.BuyerName)
	email, err := validateEmail(p.BuyerEmail)
	if err != nil {
		return Checkout{}, err
	}
	p.BuyerEmail = email
	if p.BuyerName == "" || len(p.BuyerName) > 120 {
		return Checkout{}, fmt.Errorf("%w: buyer name is required", ErrInvalidInput)
	}
	if (p.CartToken == "") == (p.StandaloneSKUID == 0) {
		return Checkout{}, fmt.Errorf("%w: provide exactly one cart or standalone SKU", ErrInvalidInput)
	}
	if p.UserID != nil && *p.UserID <= 0 || p.ReferralID != nil && *p.ReferralID <= 0 {
		return Checkout{}, fmt.Errorf("%w: invalid user or referral id", ErrInvalidInput)
	}
	if err := s.CancelStalePendingCheckouts(ctx, unattachedCheckoutLease); err != nil {
		return Checkout{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Checkout{}, fmt.Errorf("begin checkout: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	now := nowUnix(s)
	var cartID, standalone, legacyShippingID sql.NullInt64
	userID, referralID := p.UserID, p.ReferralID
	items := make([]CheckoutItem, 0)
	shipping := make([]CheckoutShipping, 0)
	shippingNames := make([]string, 0)
	var shippingAmount int64

	if p.CartToken != "" {
		hash := tokenHash(p.CartToken)
		cart, err := s.cartByHash(ctx, tx, hash[:])
		if err != nil {
			return Checkout{}, err
		}
		if cart.Status != "open" {
			return Checkout{}, ErrNotFound
		}
		var existingCheckout int64
		err = tx.QueryRowContext(ctx, `SELECT id FROM commerce_checkouts WHERE cart_id=?`, cart.ID).Scan(&existingCheckout)
		if err == nil {
			return Checkout{}, fmt.Errorf("%w: cart already has a checkout", ErrInvalidInput)
		}
		if err != sql.ErrNoRows {
			return Checkout{}, fmt.Errorf("check existing cart checkout: %w", err)
		}
		if len(cart.Items) == 0 {
			return Checkout{}, fmt.Errorf("%w: cart is empty", ErrInvalidInput)
		}
		cartID = sql.NullInt64{Int64: cart.ID, Valid: true}
		if userID == nil {
			userID = cart.UserID
		}
		if referralID == nil {
			referralID = cart.ReferralID
		}
		primaryProducts := make(map[int64]string)
		addonProducts := make(map[int64]bool)
		for _, cartItem := range cart.Items {
			sku, productType, productActive, err := skuForCart(ctx, tx, cartItem.SKU.ID)
			if err != nil {
				return Checkout{}, err
			}
			if productType == ProductTypeSubscription {
				return Checkout{}, ErrSubscriptionInCart
			}
			if !sku.Active || !productActive {
				return Checkout{}, ErrNotFound
			}
			if sku.Inventory != nil && cartItem.Quantity > *sku.Inventory {
				return Checkout{}, ErrSoldOut
			}
			var productName string
			if err := tx.QueryRowContext(ctx, `SELECT name FROM commerce_products WHERE id=?`, sku.ProductID).Scan(&productName); err != nil {
				return Checkout{}, mapNotFound(err)
			}
			if sku.Kind == SKUKindPrimary {
				primaryProducts[sku.ProductID] = productName
			} else {
				addonProducts[sku.ProductID] = true
			}
			items = append(items, CheckoutItem{ProductID: sku.ProductID, SKUID: sku.ID, ProductName: productName, SKU: sku.SKU, SKUName: sku.Name, OptionsJSON: sku.OptionsJSON, Quantity: cartItem.Quantity, UnitPriceCents: sku.PriceCents})
		}
		for productID := range addonProducts {
			if _, ok := primaryProducts[productID]; !ok {
				return Checkout{}, fmt.Errorf("%w: add-on requires its primary product", ErrInvalidInput)
			}
		}
		selections := make(map[int64]int64, len(p.ShippingOptionIDs)+1)
		for productID, optionID := range p.ShippingOptionIDs {
			selections[productID] = optionID
		}
		if p.ShippingOptionID != nil {
			if len(primaryProducts) > 1 {
				return Checkout{}, ErrMixedProductShipping
			}
			for productID := range primaryProducts {
				selections[productID] = *p.ShippingOptionID
			}
		}
		for productID := range primaryProducts {
			var activeShipping int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM commerce_product_shipping_options WHERE product_id=? AND active=1`, productID).Scan(&activeShipping); err != nil {
				return Checkout{}, fmt.Errorf("load product shipping options: %w", err)
			}
			if activeShipping > 0 && selections[productID] == 0 {
				return Checkout{}, fmt.Errorf("%w: select a shipping option for each shipped product", ErrInvalidInput)
			}
		}
		for productID, optionID := range selections {
			productName, exists := primaryProducts[productID]
			if !exists || optionID <= 0 {
				return Checkout{}, fmt.Errorf("%w: shipping option does not belong to a primary cart product", ErrInvalidInput)
			}
			var optionProductID, amount int64
			var name string
			var active bool
			if err := tx.QueryRowContext(ctx, `SELECT product_id,name,amount_cents,active FROM commerce_product_shipping_options WHERE id=?`, optionID).Scan(&optionProductID, &name, &amount, &active); err != nil {
				return Checkout{}, mapNotFound(err)
			}
			if !active || optionProductID != productID {
				return Checkout{}, fmt.Errorf("%w: shipping option does not belong to product", ErrInvalidInput)
			}
			if shippingAmount > int64(^uint64(0)>>1)-amount {
				return Checkout{}, fmt.Errorf("%w: shipping total overflow", ErrInvalidInput)
			}
			shippingAmount += amount
			shippingNames = append(shippingNames, name)
			shipping = append(shipping, CheckoutShipping{ProductID: productID, ShippingOptionID: optionID, ProductName: productName, Name: name, AmountCents: amount})
			if !legacyShippingID.Valid {
				legacyShippingID = sql.NullInt64{Int64: optionID, Valid: true}
			}
		}
	} else {
		if p.ShippingOptionID != nil || len(p.ShippingOptionIDs) != 0 {
			return Checkout{}, fmt.Errorf("%w: subscriptions do not ship", ErrInvalidInput)
		}
		sku, productType, productActive, err := skuForCart(ctx, tx, p.StandaloneSKUID)
		if err != nil {
			return Checkout{}, err
		}
		if productType != ProductTypeSubscription {
			return Checkout{}, ErrCartRequired
		}
		if sku.Kind != SKUKindPrimary || !sku.Active || !productActive {
			return Checkout{}, ErrNotFound
		}
		if sku.Inventory != nil && *sku.Inventory < 1 {
			return Checkout{}, ErrSoldOut
		}
		var productName string
		if err := tx.QueryRowContext(ctx, `SELECT name FROM commerce_products WHERE id=?`, sku.ProductID).Scan(&productName); err != nil {
			return Checkout{}, mapNotFound(err)
		}
		standalone = sql.NullInt64{Int64: sku.ID, Valid: true}
		items = append(items, CheckoutItem{ProductID: sku.ProductID, SKUID: sku.ID, ProductName: productName, SKU: sku.SKU, SKUName: sku.Name, OptionsJSON: sku.OptionsJSON, Quantity: 1, UnitPriceCents: sku.PriceCents})
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO commerce_checkouts(stripe_session_id,cart_id,standalone_sku_id,user_id,referral_id,buyer_email,buyer_name,create_account,shipping_option_id,shipping_name,shipping_amount_cents,status,created_at,updated_at) VALUES(NULLIF(?,''),?,?,?,?,?,?,?,?,?,?,'pending',?,?)`, p.StripeSessionID, nullableNullInt(cartID), nullableNullInt(standalone), nullableInt64(userID), nullableInt64(referralID), p.BuyerEmail, p.BuyerName, p.CreateAccount, nullableNullInt(legacyShippingID), strings.Join(shippingNames, "; "), shippingAmount, now, now)
	if err != nil {
		return Checkout{}, fmt.Errorf("create checkout: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return Checkout{}, err
	}
	for i := range items {
		item := &items[i]
		if _, err := tx.ExecContext(ctx, `INSERT INTO commerce_checkout_items(checkout_id,product_id,sku_id,product_name,sku,sku_name,options_json,quantity,unit_price_cents,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, id, item.ProductID, item.SKUID, item.ProductName, item.SKU, item.SKUName, item.OptionsJSON, item.Quantity, item.UnitPriceCents, now); err != nil {
			return Checkout{}, fmt.Errorf("snapshot checkout item: %w", err)
		}
	}
	if err := reserveInventory(ctx, tx, id, items, now); err != nil {
		return Checkout{}, err
	}
	for _, option := range shipping {
		if _, err := tx.ExecContext(ctx, `INSERT INTO commerce_checkout_shipping(checkout_id,product_id,shipping_option_id,product_name,name,amount_cents,created_at) VALUES(?,?,?,?,?,?,?)`, id, option.ProductID, option.ShippingOptionID, option.ProductName, option.Name, option.AmountCents, now); err != nil {
			return Checkout{}, fmt.Errorf("snapshot checkout shipping: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return Checkout{}, fmt.Errorf("commit checkout: %w", err)
	}
	return s.CheckoutByID(ctx, id)
}

func nullableNullInt(v sql.NullInt64) any {
	if !v.Valid {
		return nil
	}
	return v.Int64
}

func reserveInventory(ctx context.Context, tx *sql.Tx, checkoutID int64, items []CheckoutItem, now int64) error {
	for _, item := range items {
		result, err := tx.ExecContext(ctx, `UPDATE commerce_skus SET inventory_count=inventory_count-?,updated_at=? WHERE id=? AND inventory_count IS NOT NULL AND inventory_count>=?`, item.Quantity, now, item.SKUID, item.Quantity)
		if err != nil {
			return fmt.Errorf("reserve SKU inventory: %w", err)
		}
		if changed, _ := result.RowsAffected(); changed == 1 {
			reserved, err := tx.ExecContext(ctx, `UPDATE commerce_checkout_items SET reserved_quantity=? WHERE checkout_id=? AND sku_id=?`, item.Quantity, checkoutID, item.SKUID)
			if err != nil {
				return fmt.Errorf("snapshot reserved inventory: %w", err)
			}
			if rows, _ := reserved.RowsAffected(); rows != 1 {
				return fmt.Errorf("%w: checkout reservation item is missing", ErrInvalidInput)
			}
			continue
		}
		var inventory sql.NullInt64
		if err := tx.QueryRowContext(ctx, `SELECT inventory_count FROM commerce_skus WHERE id=?`, item.SKUID).Scan(&inventory); err != nil {
			return mapNotFound(err)
		}
		if inventory.Valid {
			return ErrSoldOut
		}
	}
	return nil
}

func (s *Store) AttachStripeSession(ctx context.Context, checkoutID int64, sessionID string) (Checkout, error) {
	if checkoutID <= 0 || strings.TrimSpace(sessionID) == "" {
		return Checkout{}, fmt.Errorf("%w: checkout and Stripe session are required", ErrInvalidInput)
	}
	sessionID = strings.TrimSpace(sessionID)
	result, err := s.db.ExecContext(ctx, `UPDATE commerce_checkouts SET stripe_session_id=?,updated_at=? WHERE id=? AND status='pending' AND (stripe_session_id IS NULL OR stripe_session_id=?)`, sessionID, nowUnix(s), checkoutID, sessionID)
	if err != nil {
		return Checkout{}, fmt.Errorf("attach Stripe session: %w", err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return Checkout{}, ErrNotFound
	}
	return s.CheckoutByID(ctx, checkoutID)
}

func (s *Store) CancelPendingCheckout(ctx context.Context, checkoutID int64) error {
	return s.cancelCheckoutReservation(ctx, checkoutID, "")
}

func (s *Store) CancelExpiredCheckout(ctx context.Context, checkoutID int64, sessionID string) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return fmt.Errorf("%w: Stripe session is required", ErrInvalidInput)
	}
	return s.cancelCheckoutReservation(ctx, checkoutID, sessionID)
}

func (s *Store) cancelCheckoutReservation(ctx context.Context, checkoutID int64, sessionID string) error {
	if checkoutID <= 0 {
		return fmt.Errorf("%w: checkout is required", ErrInvalidInput)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin checkout cancellation: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var result sql.Result
	if sessionID == "" {
		result, err = tx.ExecContext(ctx, `UPDATE commerce_checkouts SET status='expired',updated_at=? WHERE id=? AND status='pending' AND stripe_session_id IS NULL`, nowUnix(s), checkoutID)
	} else {
		result, err = tx.ExecContext(ctx, `UPDATE commerce_checkouts SET status='expired',updated_at=? WHERE id=? AND status='pending' AND (stripe_session_id IS NULL OR stripe_session_id=?)`, nowUnix(s), checkoutID, sessionID)
	}
	if err != nil {
		return fmt.Errorf("claim checkout cancellation: %w", err)
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return ErrNotFound
	}
	now := nowUnix(s)
	if _, err = tx.ExecContext(ctx, `UPDATE commerce_skus SET inventory_count=inventory_count+(SELECT COALESCE(SUM(reserved_quantity),0) FROM commerce_checkout_items WHERE checkout_id=? AND sku_id=commerce_skus.id),updated_at=? WHERE inventory_count IS NOT NULL AND id IN (SELECT sku_id FROM commerce_checkout_items WHERE checkout_id=? AND reserved_quantity>0)`, checkoutID, now, checkoutID); err != nil {
		return fmt.Errorf("release checkout inventory: %w", err)
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM commerce_checkouts WHERE id=? AND status='expired'`, checkoutID); err != nil {
		return fmt.Errorf("delete cancelled checkout: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit checkout cancellation: %w", err)
	}
	return nil
}

func (s *Store) CancelStalePendingCheckouts(ctx context.Context, maxAge time.Duration) error {
	if maxAge <= 0 {
		return fmt.Errorf("%w: checkout cleanup age must be positive", ErrInvalidInput)
	}
	cutoff := nowUnix(s) - int64(maxAge/time.Second)
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM commerce_checkouts WHERE status='pending' AND stripe_session_id IS NULL AND updated_at<=?`, cutoff)
	if err != nil {
		return fmt.Errorf("find stale pending checkouts: %w", err)
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan stale pending checkout: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close stale pending checkouts: %w", err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read stale pending checkouts: %w", err)
	}
	for _, id := range ids {
		if err := s.CancelPendingCheckout(ctx, id); err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
	}
	return nil
}

func (s *Store) SetCheckoutStripeSession(ctx context.Context, checkoutID int64, sessionID string) (Checkout, error) {
	return s.AttachStripeSession(ctx, checkoutID, sessionID)
}
func (s *Store) CheckoutByID(ctx context.Context, id int64) (Checkout, error) {
	if id <= 0 {
		return Checkout{}, fmt.Errorf("%w: checkout id required", ErrInvalidInput)
	}
	return s.checkout(ctx, s.db, `SELECT id FROM commerce_checkouts WHERE id=?`, id)
}
func (s *Store) CheckoutByCartID(ctx context.Context, cartID int64) (Checkout, error) {
	if cartID <= 0 {
		return Checkout{}, fmt.Errorf("%w: cart is required", ErrInvalidInput)
	}
	return s.checkout(ctx, s.db, `SELECT id FROM commerce_checkouts WHERE cart_id=?`, cartID)
}

func (s *Store) CheckoutByStripeSession(ctx context.Context, sessionID string) (Checkout, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return Checkout{}, fmt.Errorf("%w: Stripe session required", ErrInvalidInput)
	}
	return s.checkout(ctx, s.db, `SELECT id FROM commerce_checkouts WHERE stripe_session_id=?`, sessionID)
}
func (s *Store) CheckoutBySession(ctx context.Context, sessionID string) (Checkout, error) {
	return s.CheckoutByStripeSession(ctx, sessionID)
}

func (s *Store) checkout(ctx context.Context, q queryer, idQuery string, args ...any) (Checkout, error) {
	var id int64
	if err := q.QueryRowContext(ctx, idQuery, args...).Scan(&id); err != nil {
		return Checkout{}, mapNotFound(err)
	}
	var c Checkout
	var session sql.NullString
	var cartID, skuID, userID, invitedID, referralID, shippingID sql.NullInt64
	var created, updated int64
	err := q.QueryRowContext(ctx, `SELECT id,stripe_session_id,cart_id,standalone_sku_id,user_id,invited_user_id,referral_id,buyer_email,buyer_name,create_account,shipping_option_id,shipping_name,shipping_amount_cents,status,created_at,updated_at FROM commerce_checkouts WHERE id=?`, id).Scan(&c.ID, &session, &cartID, &skuID, &userID, &invitedID, &referralID, &c.BuyerEmail, &c.BuyerName, &c.CreateAccount, &shippingID, &c.ShippingName, &c.ShippingAmountCents, &c.Status, &created, &updated)
	if err != nil {
		return Checkout{}, mapNotFound(err)
	}
	if session.Valid {
		c.StripeSessionID = session.String
	}
	c.CartID = ptrInt64(cartID)
	c.StandaloneSKUID = ptrInt64(skuID)
	c.UserID = ptrInt64(userID)
	c.InvitedUserID = ptrInt64(invitedID)
	c.ReferralID = ptrInt64(referralID)
	c.ShippingOptionID = ptrInt64(shippingID)
	c.CreatedAt = time.Unix(created, 0).UTC()
	c.UpdatedAt = time.Unix(updated, 0).UTC()
	rows, err := q.QueryContext(ctx, `SELECT id,checkout_id,product_id,sku_id,product_name,sku,sku_name,options_json,quantity,unit_price_cents,created_at FROM commerce_checkout_items WHERE checkout_id=? ORDER BY id`, id)
	if err != nil {
		return Checkout{}, err
	}
	c.Items = make([]CheckoutItem, 0)
	for rows.Next() {
		var item CheckoutItem
		var at int64
		if err := rows.Scan(&item.ID, &item.CheckoutID, &item.ProductID, &item.SKUID, &item.ProductName, &item.SKU, &item.SKUName, &item.OptionsJSON, &item.Quantity, &item.UnitPriceCents, &at); err != nil {
			_ = rows.Close()
			return Checkout{}, err
		}
		item.CreatedAt = time.Unix(at, 0).UTC()
		c.Items = append(c.Items, item)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return Checkout{}, err
	}
	if err := rows.Close(); err != nil {
		return Checkout{}, err
	}
	shippingRows, err := q.QueryContext(ctx, `SELECT id,checkout_id,product_id,shipping_option_id,product_name,name,amount_cents,created_at FROM commerce_checkout_shipping WHERE checkout_id=? ORDER BY id`, id)
	if err != nil {
		return Checkout{}, err
	}
	c.ShippingOptions = make([]CheckoutShipping, 0)
	for shippingRows.Next() {
		var option CheckoutShipping
		var at int64
		if err := shippingRows.Scan(&option.ID, &option.CheckoutID, &option.ProductID, &option.ShippingOptionID, &option.ProductName, &option.Name, &option.AmountCents, &at); err != nil {
			_ = shippingRows.Close()
			return Checkout{}, err
		}
		option.CreatedAt = time.Unix(at, 0).UTC()
		c.ShippingOptions = append(c.ShippingOptions, option)
	}
	if err := shippingRows.Err(); err != nil {
		_ = shippingRows.Close()
		return Checkout{}, err
	}
	if err := shippingRows.Close(); err != nil {
		return Checkout{}, err
	}
	return c, nil
}
