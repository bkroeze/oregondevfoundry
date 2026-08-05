package commerce

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

func (s *Store) Fulfill(ctx context.Context, p FulfillParams) (FulfillmentResult, error) {
	p.StripeSessionID = strings.TrimSpace(p.StripeSessionID)
	p.StripeSubscriptionID = strings.TrimSpace(p.StripeSubscriptionID)
	p.SubscriptionStatus = strings.TrimSpace(p.SubscriptionStatus)
	if p.StripeSessionID == "" {
		return FulfillmentResult{}, fmt.Errorf("%w: Stripe session is required", ErrInvalidInput)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return FulfillmentResult{}, fmt.Errorf("begin fulfillment: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	checkout, err := s.checkout(ctx, tx, `SELECT id FROM commerce_checkouts WHERE stripe_session_id=?`, p.StripeSessionID)
	if err != nil {
		return FulfillmentResult{}, err
	}
	if existing, found, err := s.existingFulfillment(ctx, tx, checkout); err != nil {
		return FulfillmentResult{}, err
	} else if found {
		if err := tx.Commit(); err != nil {
			return FulfillmentResult{}, err
		}
		return existing, nil
	}
	if checkout.Status != "pending" {
		return FulfillmentResult{}, fmt.Errorf("%w: checkout is not pending", ErrInvalidInput)
	}
	if checkout.CartID == nil && p.StripeSubscriptionID == "" {
		return FulfillmentResult{}, fmt.Errorf("%w: Stripe subscription is required", ErrInvalidInput)
	}
	if checkout.CartID != nil {
		claim, err := tx.ExecContext(ctx, `UPDATE commerce_carts SET status='checked_out',updated_at=? WHERE id=? AND status='open'`, nowUnix(s), *checkout.CartID)
		if err != nil {
			return FulfillmentResult{}, fmt.Errorf("claim cart fulfillment: %w", err)
		}
		if changed, _ := claim.RowsAffected(); changed != 1 {
			return FulfillmentResult{}, fmt.Errorf("%w: cart was already fulfilled", ErrInvalidInput)
		}
	}
	userID, invitedID, invitationToken, err := s.fulfillmentUser(ctx, tx, checkout)
	if err != nil {
		return FulfillmentResult{}, err
	}
	now := nowUnix(s)
	confirmationCode, err := randomToken()
	if err != nil {
		return FulfillmentResult{}, err
	}
	result := FulfillmentResult{
		NewlyCreated:    true,
		BuyerEmail:      checkout.BuyerEmail,
		BuyerName:       checkout.BuyerName,
		InvitationToken: invitationToken,
		InvitedUserID:   invitedID,
	}

	if checkout.CartID != nil {
		var subtotal int64
		for _, item := range checkout.Items {
			if item.Quantity <= 0 || item.UnitPriceCents < 0 || item.UnitPriceCents > (int64(^uint64(0)>>1)-subtotal)/item.Quantity {
				return FulfillmentResult{}, fmt.Errorf("%w: checkout total overflow", ErrInvalidInput)
			}
			subtotal += item.UnitPriceCents * item.Quantity
		}
		if checkout.ShippingAmountCents > int64(^uint64(0)>>1)-subtotal {
			return FulfillmentResult{}, fmt.Errorf("%w: checkout total overflow", ErrInvalidInput)
		}
		total := subtotal + checkout.ShippingAmountCents
		orderResult, err := tx.ExecContext(ctx, `INSERT INTO commerce_orders(checkout_id,stripe_session_id,user_id,referral_id,buyer_email,buyer_name,shipping_name,shipping_amount_cents,subtotal_cents,total_cents,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, checkout.ID, p.StripeSessionID, nullableInt64(userID), nullableInt64(checkout.ReferralID), checkout.BuyerEmail, checkout.BuyerName, checkout.ShippingName, checkout.ShippingAmountCents, subtotal, total, now)
		if err != nil {
			return FulfillmentResult{}, fmt.Errorf("create order: %w", err)
		}
		orderID, err := orderResult.LastInsertId()
		if err != nil {
			return FulfillmentResult{}, err
		}
		for _, item := range checkout.Items {
			if _, err := tx.ExecContext(ctx, `INSERT INTO commerce_order_items(order_id,product_id,sku_id,product_name,sku,sku_name,options_json,quantity,unit_price_cents,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, orderID, item.ProductID, item.SKUID, item.ProductName, item.SKU, item.SKUName, item.OptionsJSON, item.Quantity, item.UnitPriceCents, now); err != nil {
				return FulfillmentResult{}, fmt.Errorf("create order item: %w", err)
			}
		}
		confirmationResult, err := tx.ExecContext(ctx, `INSERT INTO commerce_booking_confirmations(checkout_id,order_id,kind,code,created_at) VALUES(?,?,?,?,?)`, checkout.ID, orderID, ProductTypeOneTime, confirmationCode, now)
		if err != nil {
			return FulfillmentResult{}, fmt.Errorf("create confirmation: %w", err)
		}
		confirmationID, err := confirmationResult.LastInsertId()
		if err != nil {
			return FulfillmentResult{}, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE commerce_checkouts SET status='fulfilled',user_id=COALESCE(user_id,?),invited_user_id=?,updated_at=? WHERE id=?`, nullableInt64(userID), nullableInt64(invitedID), now, checkout.ID); err != nil {
			return FulfillmentResult{}, err
		}
		order, err := s.orderByID(ctx, tx, orderID)
		if err != nil {
			return FulfillmentResult{}, err
		}
		confirmation, err := s.confirmationByID(ctx, tx, confirmationID)
		if err != nil {
			return FulfillmentResult{}, err
		}
		result.Order = &order
		result.Confirmation = confirmation
	} else {
		if p.SubscriptionStatus == "" {
			p.SubscriptionStatus = "active"
		}
		if len(checkout.Items) != 1 {
			return FulfillmentResult{}, fmt.Errorf("%w: subscription checkout must contain one item", ErrInvalidInput)
		}
		item := checkout.Items[0]
		subscriptionResult, err := tx.ExecContext(ctx, `INSERT INTO commerce_subscriptions(checkout_id,stripe_session_id,stripe_subscription_id,user_id,referral_id,product_id,sku_id,buyer_email,buyer_name,status,current_period_end,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, checkout.ID, p.StripeSessionID, p.StripeSubscriptionID, nullableInt64(userID), nullableInt64(checkout.ReferralID), item.ProductID, item.SKUID, checkout.BuyerEmail, checkout.BuyerName, p.SubscriptionStatus, nullableTime(p.CurrentPeriodEnd), now, now)
		if err != nil {
			return FulfillmentResult{}, fmt.Errorf("create subscription: %w", err)
		}
		subscriptionID, err := subscriptionResult.LastInsertId()
		if err != nil {
			return FulfillmentResult{}, err
		}
		confirmationResult, err := tx.ExecContext(ctx, `INSERT INTO commerce_booking_confirmations(checkout_id,subscription_id,kind,code,created_at) VALUES(?,?,?,?,?)`, checkout.ID, subscriptionID, ProductTypeSubscription, confirmationCode, now)
		if err != nil {
			return FulfillmentResult{}, fmt.Errorf("create confirmation: %w", err)
		}
		confirmationID, err := confirmationResult.LastInsertId()
		if err != nil {
			return FulfillmentResult{}, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE commerce_checkouts SET status='fulfilled',user_id=COALESCE(user_id,?),invited_user_id=?,updated_at=? WHERE id=?`, nullableInt64(userID), nullableInt64(invitedID), now, checkout.ID); err != nil {
			return FulfillmentResult{}, err
		}
		subscription, err := s.subscriptionByID(ctx, tx, subscriptionID)
		if err != nil {
			return FulfillmentResult{}, err
		}
		confirmation, err := s.confirmationByID(ctx, tx, confirmationID)
		if err != nil {
			return FulfillmentResult{}, err
		}
		result.Subscription = &subscription
		result.Confirmation = confirmation
	}
	if err := tx.Commit(); err != nil {
		return FulfillmentResult{}, fmt.Errorf("commit fulfillment: %w", err)
	}
	return result, nil
}

func nullableTime(v *time.Time) any {
	if v == nil {
		return nil
	}
	return v.UTC().Unix()
}

func (s *Store) fulfillmentUser(ctx context.Context, tx *sql.Tx, c Checkout) (userID, invitedID *int64, rawToken string, err error) {
	userID = c.UserID
	if userID == nil && c.CreateAccount {
		var id int64
		lookupErr := tx.QueryRowContext(ctx, `SELECT id FROM users WHERE email=? COLLATE NOCASE`, c.BuyerEmail).Scan(&id)
		if lookupErr == sql.ErrNoRows {
			lookupErr = tx.QueryRowContext(ctx, `SELECT id FROM users WHERE username=? COLLATE NOCASE`, c.BuyerEmail).Scan(&id)
		}
		if lookupErr == nil {
			userID = &id
		} else if lookupErr != sql.ErrNoRows {
			return nil, nil, "", fmt.Errorf("find buyer user: %w", lookupErr)
		} else {
			now := nowUnix(s)
			result, insertErr := tx.ExecContext(ctx, `INSERT INTO users(username,email,display_name,role,has_purchases,created_at,updated_at,version) VALUES(?, ?,?,'user',1,?,?,1)`, c.BuyerEmail, c.BuyerEmail, c.BuyerName, now, now)
			if insertErr != nil {
				return nil, nil, "", fmt.Errorf("create invited user: %w", insertErr)
			}
			id, _ = result.LastInsertId()
			userID = &id
			invitedID = &id
			rawToken, err = s.issueInvitationTokenTx(ctx, tx, id, c.BuyerEmail, c.ReferralID)
			if err != nil {
				return nil, nil, "", err
			}
		}
	}
	if userID != nil {
		if _, err = tx.ExecContext(ctx, `UPDATE users SET has_purchases=1,updated_at=?,version=version+1 WHERE id=?`, nowUnix(s), *userID); err != nil {
			return nil, nil, "", fmt.Errorf("mark user purchased: %w", err)
		}
		if c.ReferralID != nil {
			if _, err = tx.ExecContext(ctx, `INSERT INTO commerce_user_referrals(user_id,referral_id,created_at) VALUES(?,?,?) ON CONFLICT(user_id) DO NOTHING`, *userID, *c.ReferralID, nowUnix(s)); err != nil {
				return nil, nil, "", fmt.Errorf("associate user referral: %w", err)
			}
		}
	}
	return userID, invitedID, rawToken, nil
}

func (s *Store) existingFulfillment(ctx context.Context, q queryer, c Checkout) (FulfillmentResult, bool, error) {
	result := FulfillmentResult{BuyerEmail: c.BuyerEmail, BuyerName: c.BuyerName, InvitedUserID: c.InvitedUserID}
	if c.InvitedUserID != nil {
		var status string
		if err := q.QueryRowContext(ctx, `SELECT delivery_status FROM commerce_invitations WHERE user_id=?`, *c.InvitedUserID).Scan(&status); err == nil {
			result.InvitationSent = status == "sent"
		} else if err != sql.ErrNoRows {
			return result, false, err
		}
	}
	if c.CartID != nil {
		var id int64
		if err := q.QueryRowContext(ctx, `SELECT id FROM commerce_orders WHERE checkout_id=?`, c.ID).Scan(&id); err == sql.ErrNoRows {
			return result, false, nil
		} else if err != nil {
			return result, false, err
		}
		order, err := s.orderByID(ctx, q, id)
		if err != nil {
			return result, false, err
		}
		confirmation, err := s.confirmationByCheckout(ctx, q, c.ID)
		if err != nil {
			return result, false, err
		}
		result.Order = &order
		result.Confirmation = confirmation
		return result, true, nil
	}
	var id int64
	if err := q.QueryRowContext(ctx, `SELECT id FROM commerce_subscriptions WHERE checkout_id=?`, c.ID).Scan(&id); err == sql.ErrNoRows {
		return result, false, nil
	} else if err != nil {
		return result, false, err
	}
	subscription, err := s.subscriptionByID(ctx, q, id)
	if err != nil {
		return result, false, err
	}
	confirmation, err := s.confirmationByCheckout(ctx, q, c.ID)
	if err != nil {
		return result, false, err
	}
	result.Subscription = &subscription
	result.Confirmation = confirmation
	return result, true, nil
}

func (s *Store) orderByID(ctx context.Context, q queryer, id int64) (Order, error) {
	var o Order
	var user, ref sql.NullInt64
	var sent sql.NullInt64
	var created int64
	if err := q.QueryRowContext(ctx, `SELECT id,checkout_id,stripe_session_id,user_id,referral_id,buyer_email,buyer_name,shipping_name,shipping_amount_cents,subtotal_cents,total_cents,email_sent_at,created_at FROM commerce_orders WHERE id=?`, id).Scan(&o.ID, &o.CheckoutID, &o.StripeSessionID, &user, &ref, &o.BuyerEmail, &o.BuyerName, &o.ShippingName, &o.ShippingAmountCents, &o.SubtotalCents, &o.TotalCents, &sent, &created); err != nil {
		return Order{}, mapNotFound(err)
	}
	o.UserID = ptrInt64(user)
	o.ReferralID = ptrInt64(ref)
	o.EmailSentAt = ptrTime(sent)
	o.CreatedAt = time.Unix(created, 0).UTC()
	rows, err := q.QueryContext(ctx, `SELECT id,order_id,product_id,sku_id,product_name,sku,sku_name,options_json,quantity,unit_price_cents,created_at FROM commerce_order_items WHERE order_id=? ORDER BY id`, id)
	if err != nil {
		return Order{}, err
	}
	o.Items = []OrderItem{}
	for rows.Next() {
		var item OrderItem
		var at int64
		if err := rows.Scan(&item.ID, &item.OrderID, &item.ProductID, &item.SKUID, &item.ProductName, &item.SKU, &item.SKUName, &item.OptionsJSON, &item.Quantity, &item.UnitPriceCents, &at); err != nil {
			_ = rows.Close()
			return Order{}, err
		}
		item.CreatedAt = time.Unix(at, 0).UTC()
		o.Items = append(o.Items, item)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return Order{}, err
	}
	return o, rows.Close()
}
func (s *Store) OrderByID(ctx context.Context, id int64) (Order, error) {
	if id <= 0 {
		return Order{}, fmt.Errorf("%w: order id required", ErrInvalidInput)
	}
	return s.orderByID(ctx, s.db, id)
}
func (s *Store) OrderByStripeSession(ctx context.Context, session string) (Order, error) {
	session = strings.TrimSpace(session)
	if session == "" {
		return Order{}, fmt.Errorf("%w: session required", ErrInvalidInput)
	}
	var id int64
	if err := s.db.QueryRowContext(ctx, `SELECT id FROM commerce_orders WHERE stripe_session_id=?`, session).Scan(&id); err != nil {
		return Order{}, mapNotFound(err)
	}
	return s.orderByID(ctx, s.db, id)
}

func (s *Store) subscriptionByID(ctx context.Context, q queryer, id int64) (Subscription, error) {
	var sub Subscription
	var user, ref, period, sent sql.NullInt64
	var created, updated int64
	if err := q.QueryRowContext(ctx, `SELECT id,checkout_id,stripe_session_id,stripe_subscription_id,user_id,referral_id,product_id,sku_id,buyer_email,buyer_name,status,current_period_end,email_sent_at,created_at,updated_at FROM commerce_subscriptions WHERE id=?`, id).Scan(&sub.ID, &sub.CheckoutID, &sub.StripeSessionID, &sub.StripeSubscriptionID, &user, &ref, &sub.ProductID, &sub.SKUID, &sub.BuyerEmail, &sub.BuyerName, &sub.Status, &period, &sent, &created, &updated); err != nil {
		return Subscription{}, mapNotFound(err)
	}
	sub.UserID = ptrInt64(user)
	sub.ReferralID = ptrInt64(ref)
	sub.CurrentPeriodEnd = ptrTime(period)
	sub.EmailSentAt = ptrTime(sent)
	sub.CreatedAt = time.Unix(created, 0).UTC()
	sub.UpdatedAt = time.Unix(updated, 0).UTC()
	return sub, nil
}
func (s *Store) SubscriptionByID(ctx context.Context, id int64) (Subscription, error) {
	if id <= 0 {
		return Subscription{}, fmt.Errorf("%w: subscription id required", ErrInvalidInput)
	}
	return s.subscriptionByID(ctx, s.db, id)
}
func (s *Store) SubscriptionByStripeID(ctx context.Context, stripeID string) (Subscription, error) {
	stripeID = strings.TrimSpace(stripeID)
	if stripeID == "" {
		return Subscription{}, fmt.Errorf("%w: subscription id required", ErrInvalidInput)
	}
	var id int64
	if err := s.db.QueryRowContext(ctx, `SELECT id FROM commerce_subscriptions WHERE stripe_subscription_id=?`, stripeID).Scan(&id); err != nil {
		return Subscription{}, mapNotFound(err)
	}
	return s.subscriptionByID(ctx, s.db, id)
}

func (s *Store) confirmationByID(ctx context.Context, q queryer, id int64) (BookingConfirmation, error) {
	var c BookingConfirmation
	var orderID, subID, sent sql.NullInt64
	var created int64
	if err := q.QueryRowContext(ctx, `SELECT id,checkout_id,order_id,subscription_id,kind,code,email_sent_at,created_at FROM commerce_booking_confirmations WHERE id=?`, id).Scan(&c.ID, &c.CheckoutID, &orderID, &subID, &c.Kind, &c.Code, &sent, &created); err != nil {
		return BookingConfirmation{}, mapNotFound(err)
	}
	c.OrderID = ptrInt64(orderID)
	c.SubscriptionID = ptrInt64(subID)
	c.EmailSentAt = ptrTime(sent)
	c.CreatedAt = time.Unix(created, 0).UTC()
	return c, nil
}
func (s *Store) confirmationByCheckout(ctx context.Context, q queryer, id int64) (BookingConfirmation, error) {
	var confirmationID int64
	if err := q.QueryRowContext(ctx, `SELECT id FROM commerce_booking_confirmations WHERE checkout_id=?`, id).Scan(&confirmationID); err != nil {
		return BookingConfirmation{}, mapNotFound(err)
	}
	return s.confirmationByID(ctx, q, confirmationID)
}
func (s *Store) ConfirmationByCode(ctx context.Context, code string) (BookingConfirmation, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return BookingConfirmation{}, fmt.Errorf("%w: confirmation code required", ErrInvalidInput)
	}
	var id int64
	if err := s.db.QueryRowContext(ctx, `SELECT id FROM commerce_booking_confirmations WHERE code=?`, code).Scan(&id); err != nil {
		return BookingConfirmation{}, mapNotFound(err)
	}
	return s.confirmationByID(ctx, s.db, id)
}
