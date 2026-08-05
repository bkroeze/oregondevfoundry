package commerce

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func (s *Store) CreateCart(ctx context.Context, userID, referralID *int64) (Cart, error) {
	if userID != nil && *userID <= 0 {
		return Cart{}, fmt.Errorf("%w: invalid user id", ErrInvalidInput)
	}
	if referralID != nil && *referralID <= 0 {
		return Cart{}, fmt.Errorf("%w: invalid referral id", ErrInvalidInput)
	}
	token, err := randomToken()
	if err != nil {
		return Cart{}, err
	}
	hash := tokenHash(token)
	now := nowUnix(s)
	result, err := s.db.ExecContext(ctx, `INSERT INTO commerce_carts(token_hash,user_id,referral_id,status,created_at,updated_at) VALUES(?,?,?,'open',?,?)`, hash[:], nullableInt64(userID), nullableInt64(referralID), now, now)
	if err != nil {
		return Cart{}, fmt.Errorf("create cart: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return Cart{}, fmt.Errorf("read cart id: %w", err)
	}
	cart, err := s.cartByID(ctx, s.db, id)
	if err != nil {
		return Cart{}, err
	}
	cart.Token = token
	return cart, nil
}

func (s *Store) CartByToken(ctx context.Context, token string) (Cart, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return Cart{}, fmt.Errorf("%w: cart token is required", ErrInvalidInput)
	}
	hash := tokenHash(token)
	cart, err := s.cartByHash(ctx, s.db, hash[:])
	if err != nil {
		return Cart{}, err
	}
	cart.Token = token
	return cart, nil
}

func (s *Store) SetCartReferral(ctx context.Context, token string, referralID *int64) (Cart, error) {
	if strings.TrimSpace(token) == "" {
		return Cart{}, fmt.Errorf("%w: cart token is required", ErrInvalidInput)
	}
	if referralID != nil && *referralID <= 0 {
		return Cart{}, fmt.Errorf("%w: invalid referral id", ErrInvalidInput)
	}
	hash := tokenHash(token)
	result, err := s.db.ExecContext(ctx, `UPDATE commerce_carts SET referral_id=?,updated_at=? WHERE token_hash=? AND status='open'`, nullableInt64(referralID), nowUnix(s), hash[:])
	if err != nil {
		return Cart{}, fmt.Errorf("set cart referral: %w", err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return Cart{}, ErrNotFound
	}
	return s.CartByToken(ctx, token)
}

func (s *Store) SetCartUser(ctx context.Context, token string, userID *int64) (Cart, error) {
	if strings.TrimSpace(token) == "" {
		return Cart{}, fmt.Errorf("%w: cart token is required", ErrInvalidInput)
	}
	if userID != nil && *userID <= 0 {
		return Cart{}, fmt.Errorf("%w: invalid user id", ErrInvalidInput)
	}
	hash := tokenHash(token)
	result, err := s.db.ExecContext(ctx, `UPDATE commerce_carts SET user_id=?,updated_at=? WHERE token_hash=? AND status='open'`, nullableInt64(userID), nowUnix(s), hash[:])
	if err != nil {
		return Cart{}, fmt.Errorf("set cart user: %w", err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return Cart{}, ErrNotFound
	}
	return s.CartByToken(ctx, token)
}

func (s *Store) AddCartItem(ctx context.Context, token string, skuID, quantity int64) (Cart, error) {
	if strings.TrimSpace(token) == "" || skuID <= 0 || quantity <= 0 {
		return Cart{}, fmt.Errorf("%w: cart token, SKU, and positive quantity are required", ErrInvalidInput)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Cart{}, fmt.Errorf("begin add cart item: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	hash := tokenHash(token)
	cart, err := s.cartByHash(ctx, tx, hash[:])
	if err != nil {
		return Cart{}, err
	}
	if cart.Status != "open" {
		return Cart{}, ErrNotFound
	}
	if err := ensureCartMutable(ctx, tx, cart.ID); err != nil {
		return Cart{}, err
	}
	sku, productType, productActive, err := skuForCart(ctx, tx, skuID)
	if err != nil {
		return Cart{}, err
	}
	if productType == ProductTypeSubscription {
		return Cart{}, ErrSubscriptionInCart
	}
	if !sku.Active || !productActive {
		return Cart{}, ErrNotFound
	}
	var existing int64
	scanErr := tx.QueryRowContext(ctx, `SELECT quantity FROM commerce_cart_items WHERE cart_id=? AND sku_id=?`, cart.ID, skuID).Scan(&existing)
	if scanErr != nil && scanErr != sql.ErrNoRows {
		return Cart{}, fmt.Errorf("load cart quantity: %w", scanErr)
	}
	requested := existing + quantity
	if sku.Inventory != nil && requested > *sku.Inventory {
		return Cart{}, ErrSoldOut
	}
	if sku.Kind == SKUKindAddon {
		var primary int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM commerce_cart_items ci JOIN commerce_skus s ON s.id=ci.sku_id WHERE ci.cart_id=? AND s.product_id=? AND s.kind='primary'`, cart.ID, sku.ProductID).Scan(&primary); err != nil {
			return Cart{}, err
		}
		if primary == 0 {
			return Cart{}, fmt.Errorf("%w: add-on requires its primary product", ErrInvalidInput)
		}
	}
	now := nowUnix(s)
	_, err = tx.ExecContext(ctx, `INSERT INTO commerce_cart_items(cart_id,sku_id,quantity,created_at,updated_at) VALUES(?,?,?,?,?) ON CONFLICT(cart_id,sku_id) DO UPDATE SET quantity=quantity+excluded.quantity,updated_at=excluded.updated_at`, cart.ID, skuID, quantity, now, now)
	if err != nil {
		return Cart{}, fmt.Errorf("add cart item: %w", err)
	}
	if _, err = tx.ExecContext(ctx, `UPDATE commerce_carts SET updated_at=? WHERE id=?`, now, cart.ID); err != nil {
		return Cart{}, err
	}
	if err = tx.Commit(); err != nil {
		return Cart{}, fmt.Errorf("commit add cart item: %w", err)
	}
	return s.CartByToken(ctx, token)
}

func (s *Store) AddCartSelection(ctx context.Context, token string, p CartSelectionParams) (Cart, error) {
	if strings.TrimSpace(token) == "" || p.PrimarySKUID <= 0 || p.Quantity <= 0 {
		return Cart{}, fmt.Errorf("%w: cart token, primary SKU, and positive quantity are required", ErrInvalidInput)
	}
	seen := make(map[int64]struct{}, len(p.AddonSKUIDs)+1)
	seen[p.PrimarySKUID] = struct{}{}
	for _, id := range p.AddonSKUIDs {
		if id <= 0 {
			return Cart{}, fmt.Errorf("%w: invalid add-on SKU", ErrInvalidInput)
		}
		if _, exists := seen[id]; exists {
			return Cart{}, fmt.Errorf("%w: duplicate SKU", ErrInvalidInput)
		}
		seen[id] = struct{}{}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Cart{}, fmt.Errorf("begin add cart selection: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	hash := tokenHash(token)
	cart, err := s.cartByHash(ctx, tx, hash[:])
	if err != nil {
		return Cart{}, err
	}
	if cart.Status != "open" {
		return Cart{}, ErrNotFound
	}
	if err := ensureCartMutable(ctx, tx, cart.ID); err != nil {
		return Cart{}, err
	}

	primary, productType, productActive, err := skuForCart(ctx, tx, p.PrimarySKUID)
	if err != nil {
		return Cart{}, err
	}
	if productType == ProductTypeSubscription {
		return Cart{}, ErrSubscriptionInCart
	}
	if productType != ProductTypeOneTime || primary.Kind != SKUKindPrimary {
		return Cart{}, fmt.Errorf("%w: selection requires a one-time primary SKU", ErrInvalidInput)
	}
	if !primary.Active || !productActive {
		return Cart{}, ErrNotFound
	}

	skus := make([]SKU, 0, len(p.AddonSKUIDs)+1)
	skus = append(skus, primary)
	for _, id := range p.AddonSKUIDs {
		addon, addonProductType, addonProductActive, loadErr := skuForCart(ctx, tx, id)
		if loadErr != nil {
			return Cart{}, loadErr
		}
		if addon.ProductID != primary.ProductID || addon.Kind != SKUKindAddon || addonProductType != ProductTypeOneTime {
			return Cart{}, fmt.Errorf("%w: add-on does not belong to the selected product", ErrInvalidInput)
		}
		if !addon.Active || !addonProductActive {
			return Cart{}, ErrNotFound
		}
		skus = append(skus, addon)
	}

	for _, sku := range skus {
		var existing int64
		scanErr := tx.QueryRowContext(ctx, `SELECT quantity FROM commerce_cart_items WHERE cart_id=? AND sku_id=?`, cart.ID, sku.ID).Scan(&existing)
		if scanErr != nil && scanErr != sql.ErrNoRows {
			return Cart{}, fmt.Errorf("load cart quantity: %w", scanErr)
		}
		if existing < 0 || p.Quantity > int64(^uint64(0)>>1)-existing {
			return Cart{}, fmt.Errorf("%w: invalid cart quantity", ErrInvalidInput)
		}
		if sku.Inventory != nil && existing+p.Quantity > *sku.Inventory {
			return Cart{}, ErrSoldOut
		}
	}

	now := nowUnix(s)
	for _, sku := range skus {
		if _, err = tx.ExecContext(ctx, `INSERT INTO commerce_cart_items(cart_id,sku_id,quantity,created_at,updated_at) VALUES(?,?,?,?,?) ON CONFLICT(cart_id,sku_id) DO UPDATE SET quantity=quantity+excluded.quantity,updated_at=excluded.updated_at`, cart.ID, sku.ID, p.Quantity, now, now); err != nil {
			return Cart{}, fmt.Errorf("add cart selection: %w", err)
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE commerce_carts SET updated_at=? WHERE id=?`, now, cart.ID); err != nil {
		return Cart{}, fmt.Errorf("update cart: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return Cart{}, fmt.Errorf("commit add cart selection: %w", err)
	}
	return s.CartByToken(ctx, token)
}

func (s *Store) UpdateCartItem(ctx context.Context, token string, skuID, quantity int64) (Cart, error) {
	if quantity == 0 {
		return s.RemoveCartItem(ctx, token, skuID)
	}
	if strings.TrimSpace(token) == "" || skuID <= 0 || quantity < 0 {
		return Cart{}, fmt.Errorf("%w: invalid cart item", ErrInvalidInput)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Cart{}, err
	}
	defer func() { _ = tx.Rollback() }()
	hash := tokenHash(token)
	cart, err := s.cartByHash(ctx, tx, hash[:])
	if err != nil {
		return Cart{}, err
	}
	if cart.Status != "open" {
		return Cart{}, ErrNotFound
	}
	if err := ensureCartMutable(ctx, tx, cart.ID); err != nil {
		return Cart{}, err
	}
	sku, typ, active, err := skuForCart(ctx, tx, skuID)
	if err != nil {
		return Cart{}, err
	}
	if typ == ProductTypeSubscription {
		return Cart{}, ErrSubscriptionInCart
	}
	if !sku.Active || !active {
		return Cart{}, ErrNotFound
	}
	if sku.Inventory != nil && quantity > *sku.Inventory {
		return Cart{}, ErrSoldOut
	}
	result, err := tx.ExecContext(ctx, `UPDATE commerce_cart_items SET quantity=?,updated_at=? WHERE cart_id=? AND sku_id=?`, quantity, nowUnix(s), cart.ID, skuID)
	if err != nil {
		return Cart{}, fmt.Errorf("update cart item: %w", err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return Cart{}, ErrNotFound
	}
	if _, err = tx.ExecContext(ctx, `UPDATE commerce_carts SET updated_at=? WHERE id=?`, nowUnix(s), cart.ID); err != nil {
		return Cart{}, err
	}
	if err = tx.Commit(); err != nil {
		return Cart{}, err
	}
	return s.CartByToken(ctx, token)
}

func (s *Store) RemoveCartItem(ctx context.Context, token string, skuID int64) (Cart, error) {
	if strings.TrimSpace(token) == "" || skuID <= 0 {
		return Cart{}, fmt.Errorf("%w: invalid cart item", ErrInvalidInput)
	}
	hash := tokenHash(token)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Cart{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var cartID int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM commerce_carts WHERE token_hash=? AND status='open'`, hash[:]).Scan(&cartID); err != nil {
		return Cart{}, mapNotFound(err)
	}
	if err := ensureCartMutable(ctx, tx, cartID); err != nil {
		return Cart{}, err
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM commerce_cart_items WHERE cart_id=? AND sku_id=?`, cartID, skuID)
	if err != nil {
		return Cart{}, fmt.Errorf("remove cart item: %w", err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return Cart{}, ErrNotFound
	}
	if _, err = tx.ExecContext(ctx, `UPDATE commerce_carts SET updated_at=? WHERE id=?`, nowUnix(s), cartID); err != nil {
		return Cart{}, err
	}
	if err = tx.Commit(); err != nil {
		return Cart{}, err
	}
	return s.CartByToken(ctx, token)
}

func ensureCartMutable(ctx context.Context, q queryer, cartID int64) error {
	var pending bool
	if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM commerce_checkouts WHERE cart_id=? AND status='pending')`, cartID).Scan(&pending); err != nil {
		return fmt.Errorf("check pending cart checkout: %w", err)
	}
	if pending {
		return fmt.Errorf("%w: cart has an active checkout", ErrInvalidInput)
	}
	return nil
}

func skuForCart(ctx context.Context, q queryer, id int64) (SKU, ProductType, bool, error) {
	row := q.QueryRowContext(ctx, `SELECT s.id,s.product_id,s.sku,s.name,s.kind,s.options_json,s.price_cents,s.inventory_count,s.active,s.stripe_price_id,s.created_at,s.updated_at,p.product_type,p.active FROM commerce_skus s JOIN commerce_products p ON p.id=s.product_id WHERE s.id=?`, id)
	var sku SKU
	var inventory sql.NullInt64
	var created, updated int64
	var typ ProductType
	var productActive bool
	if err := row.Scan(&sku.ID, &sku.ProductID, &sku.SKU, &sku.Name, &sku.Kind, &sku.OptionsJSON, &sku.PriceCents, &inventory, &sku.Active, &sku.StripePriceID, &created, &updated, &typ, &productActive); err != nil {
		return SKU{}, "", false, mapNotFound(err)
	}
	sku.Inventory = ptrInt64(inventory)
	sku.CreatedAt = time.Unix(created, 0).UTC()
	sku.UpdatedAt = time.Unix(updated, 0).UTC()
	return sku, typ, productActive, nil
}

func (s *Store) cartByHash(ctx context.Context, q queryer, hash []byte) (Cart, error) {
	var id int64
	err := q.QueryRowContext(ctx, `SELECT id FROM commerce_carts WHERE token_hash=?`, hash).Scan(&id)
	if err != nil {
		return Cart{}, mapNotFound(err)
	}
	return s.cartByID(ctx, q, id)
}
func (s *Store) cartByID(ctx context.Context, q queryer, id int64) (Cart, error) {
	var cart Cart
	var userID, referralID sql.NullInt64
	var created, updated int64
	if err := q.QueryRowContext(ctx, `SELECT id,user_id,referral_id,status,created_at,updated_at FROM commerce_carts WHERE id=?`, id).Scan(&cart.ID, &userID, &referralID, &cart.Status, &created, &updated); err != nil {
		return Cart{}, mapNotFound(err)
	}
	cart.UserID = ptrInt64(userID)
	cart.ReferralID = ptrInt64(referralID)
	cart.CreatedAt = time.Unix(created, 0).UTC()
	cart.UpdatedAt = time.Unix(updated, 0).UTC()
	rows, err := q.QueryContext(ctx, `SELECT ci.id,ci.cart_id,ci.quantity,ci.created_at,ci.updated_at,s.id,s.product_id,s.sku,s.name,s.kind,s.options_json,s.price_cents,s.inventory_count,s.active,s.stripe_price_id,s.created_at,s.updated_at FROM commerce_cart_items ci JOIN commerce_skus s ON s.id=ci.sku_id WHERE ci.cart_id=? ORDER BY ci.id`, id)
	if err != nil {
		return Cart{}, err
	}
	cart.Items = make([]CartItem, 0)
	for rows.Next() {
		var item CartItem
		var inv sql.NullInt64
		var itemCreated, itemUpdated, skuCreated, skuUpdated int64
		if err := rows.Scan(&item.ID, &item.CartID, &item.Quantity, &itemCreated, &itemUpdated, &item.SKU.ID, &item.SKU.ProductID, &item.SKU.SKU, &item.SKU.Name, &item.SKU.Kind, &item.SKU.OptionsJSON, &item.SKU.PriceCents, &inv, &item.SKU.Active, &item.SKU.StripePriceID, &skuCreated, &skuUpdated); err != nil {
			_ = rows.Close()
			return Cart{}, err
		}
		item.SKU.Inventory = ptrInt64(inv)
		item.CreatedAt = time.Unix(itemCreated, 0).UTC()
		item.UpdatedAt = time.Unix(itemUpdated, 0).UTC()
		item.SKU.CreatedAt = time.Unix(skuCreated, 0).UTC()
		item.SKU.UpdatedAt = time.Unix(skuUpdated, 0).UTC()
		cart.Items = append(cart.Items, item)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return Cart{}, err
	}
	if err := rows.Close(); err != nil {
		return Cart{}, err
	}
	return cart, nil
}
