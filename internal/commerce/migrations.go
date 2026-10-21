package commerce

import (
	"context"
	"database/sql"
	"fmt"
)

const schema = `
CREATE TABLE IF NOT EXISTS commerce_products (
    id INTEGER PRIMARY KEY,
    slug TEXT NOT NULL COLLATE NOCASE UNIQUE,
    master_sku TEXT NOT NULL COLLATE NOCASE UNIQUE,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    product_type TEXT NOT NULL CHECK (product_type IN ('one_time', 'subscription')),
    active INTEGER NOT NULL DEFAULT 1 CHECK (active IN (0, 1)),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS commerce_products_sellable ON commerce_products(active, product_type);

CREATE TABLE IF NOT EXISTS commerce_skus (
    id INTEGER PRIMARY KEY,
    product_id INTEGER NOT NULL REFERENCES commerce_products(id) ON DELETE CASCADE,
    sku TEXT NOT NULL COLLATE NOCASE UNIQUE,
    name TEXT NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('primary', 'addon')),
    options_json TEXT NOT NULL DEFAULT '{}',
    price_cents INTEGER NOT NULL CHECK (price_cents >= 0),
    inventory_count INTEGER CHECK (inventory_count IS NULL OR inventory_count >= 0),
    active INTEGER NOT NULL DEFAULT 1 CHECK (active IN (0, 1)),
    stripe_price_id TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS commerce_skus_product ON commerce_skus(product_id, kind, active);
CREATE INDEX IF NOT EXISTS commerce_skus_stripe_price ON commerce_skus(stripe_price_id) WHERE stripe_price_id <> '';
CREATE UNIQUE INDEX IF NOT EXISTS commerce_skus_primary_options ON commerce_skus(product_id, options_json) WHERE kind = 'primary';

CREATE TABLE IF NOT EXISTS commerce_product_images (
    id INTEGER PRIMARY KEY,
    product_id INTEGER NOT NULL REFERENCES commerce_products(id) ON DELETE CASCADE,
    sku_id INTEGER REFERENCES commerce_skus(id) ON DELETE CASCADE,
    addon_sku_id INTEGER REFERENCES commerce_skus(id) ON DELETE CASCADE,
    filename TEXT NOT NULL,
    width INTEGER NOT NULL CHECK (width > 0),
    height INTEGER NOT NULL CHECK (height > 0),
    CHECK (addon_sku_id IS NULL OR sku_id IS NOT NULL)
);
CREATE UNIQUE INDEX IF NOT EXISTS commerce_product_images_slot
    ON commerce_product_images(product_id, COALESCE(sku_id, 0), COALESCE(addon_sku_id, 0));

CREATE TABLE IF NOT EXISTS commerce_catalog_seeds (
    name TEXT PRIMARY KEY,
    created_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS commerce_product_shipping_options (
    id INTEGER PRIMARY KEY,
    product_id INTEGER NOT NULL REFERENCES commerce_products(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    amount_cents INTEGER NOT NULL CHECK (amount_cents >= 0),
    active INTEGER NOT NULL DEFAULT 1 CHECK (active IN (0, 1)),
    sort_order INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    UNIQUE(product_id, name)
);
CREATE INDEX IF NOT EXISTS commerce_shipping_product ON commerce_product_shipping_options(product_id, active, sort_order);

CREATE TABLE IF NOT EXISTS commerce_referrals (
    id INTEGER PRIMARY KEY,
    token TEXT NOT NULL COLLATE NOCASE UNIQUE,
    source_url TEXT NOT NULL DEFAULT '',
    host TEXT NOT NULL COLLATE NOCASE DEFAULT '',
    created_at INTEGER NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS commerce_referrals_url_host_unique ON commerce_referrals(source_url, host) WHERE source_url <> '' OR host <> '';

CREATE TABLE IF NOT EXISTS commerce_carts (
    id INTEGER PRIMARY KEY,
    token_hash BLOB NOT NULL UNIQUE,
    user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
    referral_id INTEGER REFERENCES commerce_referrals(id) ON DELETE SET NULL,
    status TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'checked_out', 'abandoned')),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS commerce_carts_user ON commerce_carts(user_id, status);
CREATE INDEX IF NOT EXISTS commerce_carts_referral ON commerce_carts(referral_id);

CREATE TABLE IF NOT EXISTS commerce_cart_items (
    id INTEGER PRIMARY KEY,
    cart_id INTEGER NOT NULL REFERENCES commerce_carts(id) ON DELETE CASCADE,
    sku_id INTEGER NOT NULL REFERENCES commerce_skus(id) ON DELETE RESTRICT,
    quantity INTEGER NOT NULL CHECK (quantity > 0),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    UNIQUE(cart_id, sku_id)
);
CREATE INDEX IF NOT EXISTS commerce_cart_items_sku ON commerce_cart_items(sku_id);

CREATE TABLE IF NOT EXISTS commerce_checkouts (
    id INTEGER PRIMARY KEY,
    stripe_session_id TEXT UNIQUE,
    cart_id INTEGER REFERENCES commerce_carts(id) ON DELETE RESTRICT,
    standalone_sku_id INTEGER REFERENCES commerce_skus(id) ON DELETE RESTRICT,
    user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
    invited_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
    referral_id INTEGER REFERENCES commerce_referrals(id) ON DELETE SET NULL,
    buyer_email TEXT NOT NULL COLLATE NOCASE,
    buyer_name TEXT NOT NULL,
    create_account INTEGER NOT NULL DEFAULT 0 CHECK (create_account IN (0, 1)),
    shipping_option_id INTEGER REFERENCES commerce_product_shipping_options(id) ON DELETE RESTRICT,
    shipping_name TEXT NOT NULL DEFAULT '',
    shipping_amount_cents INTEGER NOT NULL DEFAULT 0 CHECK (shipping_amount_cents >= 0),
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'fulfilled', 'expired')),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    CHECK ((cart_id IS NOT NULL AND standalone_sku_id IS NULL) OR (cart_id IS NULL AND standalone_sku_id IS NOT NULL))
);
CREATE UNIQUE INDEX IF NOT EXISTS commerce_checkouts_cart_unique ON commerce_checkouts(cart_id) WHERE cart_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS commerce_checkouts_user ON commerce_checkouts(user_id);
CREATE INDEX IF NOT EXISTS commerce_checkouts_referral ON commerce_checkouts(referral_id);

CREATE TABLE IF NOT EXISTS commerce_checkout_items (
    id INTEGER PRIMARY KEY,
    checkout_id INTEGER NOT NULL REFERENCES commerce_checkouts(id) ON DELETE CASCADE,
    product_id INTEGER NOT NULL REFERENCES commerce_products(id) ON DELETE RESTRICT,
    sku_id INTEGER NOT NULL REFERENCES commerce_skus(id) ON DELETE RESTRICT,
    product_name TEXT NOT NULL,
    sku TEXT NOT NULL,
    sku_name TEXT NOT NULL,
    options_json TEXT NOT NULL,
    quantity INTEGER NOT NULL CHECK (quantity > 0),
    unit_price_cents INTEGER NOT NULL CHECK (unit_price_cents >= 0),
    reserved_quantity INTEGER NOT NULL DEFAULT 0 CHECK (reserved_quantity >= 0 AND reserved_quantity <= quantity),
    created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS commerce_checkout_items_checkout ON commerce_checkout_items(checkout_id);
CREATE INDEX IF NOT EXISTS commerce_checkout_items_sku ON commerce_checkout_items(sku_id);

CREATE TABLE IF NOT EXISTS commerce_checkout_shipping (
    id INTEGER PRIMARY KEY,
    checkout_id INTEGER NOT NULL REFERENCES commerce_checkouts(id) ON DELETE CASCADE,
    product_id INTEGER NOT NULL REFERENCES commerce_products(id) ON DELETE RESTRICT,
    shipping_option_id INTEGER NOT NULL REFERENCES commerce_product_shipping_options(id) ON DELETE RESTRICT,
    product_name TEXT NOT NULL,
    name TEXT NOT NULL,
    amount_cents INTEGER NOT NULL CHECK (amount_cents >= 0),
    created_at INTEGER NOT NULL,
    UNIQUE(checkout_id, product_id)
);
CREATE INDEX IF NOT EXISTS commerce_checkout_shipping_checkout ON commerce_checkout_shipping(checkout_id);
CREATE INDEX IF NOT EXISTS commerce_checkout_shipping_option ON commerce_checkout_shipping(shipping_option_id);

CREATE TABLE IF NOT EXISTS commerce_orders (
    id INTEGER PRIMARY KEY,
    checkout_id INTEGER NOT NULL UNIQUE REFERENCES commerce_checkouts(id) ON DELETE RESTRICT,
    stripe_session_id TEXT NOT NULL UNIQUE,
    user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
    referral_id INTEGER REFERENCES commerce_referrals(id) ON DELETE SET NULL,
    buyer_email TEXT NOT NULL,
    buyer_name TEXT NOT NULL,
    shipping_name TEXT NOT NULL DEFAULT '',
    shipping_amount_cents INTEGER NOT NULL CHECK (shipping_amount_cents >= 0),
    subtotal_cents INTEGER NOT NULL CHECK (subtotal_cents >= 0),
    total_cents INTEGER NOT NULL CHECK (total_cents >= 0),
    email_delivery_status TEXT NOT NULL DEFAULT 'pending' CHECK (email_delivery_status IN ('pending', 'sending', 'sent')),
    email_delivery_claimed_at INTEGER,
    email_sent_at INTEGER,
    created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS commerce_orders_user ON commerce_orders(user_id, created_at);
CREATE INDEX IF NOT EXISTS commerce_orders_referral ON commerce_orders(referral_id);

CREATE TABLE IF NOT EXISTS commerce_order_items (
    id INTEGER PRIMARY KEY,
    order_id INTEGER NOT NULL REFERENCES commerce_orders(id) ON DELETE CASCADE,
    product_id INTEGER NOT NULL REFERENCES commerce_products(id) ON DELETE RESTRICT,
    sku_id INTEGER NOT NULL REFERENCES commerce_skus(id) ON DELETE RESTRICT,
    product_name TEXT NOT NULL,
    sku TEXT NOT NULL,
    sku_name TEXT NOT NULL,
    options_json TEXT NOT NULL,
    quantity INTEGER NOT NULL CHECK (quantity > 0),
    unit_price_cents INTEGER NOT NULL CHECK (unit_price_cents >= 0),
    created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS commerce_order_items_order ON commerce_order_items(order_id);
CREATE INDEX IF NOT EXISTS commerce_order_items_sku ON commerce_order_items(sku_id);

CREATE TABLE IF NOT EXISTS commerce_subscriptions (
    id INTEGER PRIMARY KEY,
    checkout_id INTEGER NOT NULL UNIQUE REFERENCES commerce_checkouts(id) ON DELETE RESTRICT,
    stripe_session_id TEXT NOT NULL UNIQUE,
    stripe_subscription_id TEXT NOT NULL UNIQUE,
    user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
    referral_id INTEGER REFERENCES commerce_referrals(id) ON DELETE SET NULL,
    product_id INTEGER NOT NULL REFERENCES commerce_products(id) ON DELETE RESTRICT,
    sku_id INTEGER NOT NULL REFERENCES commerce_skus(id) ON DELETE RESTRICT,
    buyer_email TEXT NOT NULL,
    buyer_name TEXT NOT NULL,
    status TEXT NOT NULL,
    current_period_end INTEGER,
    email_delivery_status TEXT NOT NULL DEFAULT 'pending' CHECK (email_delivery_status IN ('pending', 'sending', 'sent')),
    email_delivery_claimed_at INTEGER,
    email_sent_at INTEGER,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS commerce_subscriptions_user ON commerce_subscriptions(user_id, status);
CREATE INDEX IF NOT EXISTS commerce_subscriptions_referral ON commerce_subscriptions(referral_id);
CREATE INDEX IF NOT EXISTS commerce_subscriptions_sku ON commerce_subscriptions(sku_id, status);

CREATE TABLE IF NOT EXISTS commerce_booking_confirmations (
    id INTEGER PRIMARY KEY,
    checkout_id INTEGER NOT NULL UNIQUE REFERENCES commerce_checkouts(id) ON DELETE RESTRICT,
    order_id INTEGER UNIQUE REFERENCES commerce_orders(id) ON DELETE RESTRICT,
    subscription_id INTEGER UNIQUE REFERENCES commerce_subscriptions(id) ON DELETE RESTRICT,
    kind TEXT NOT NULL CHECK (kind IN ('one_time', 'subscription')),
    code TEXT NOT NULL UNIQUE,
    email_sent_at INTEGER,
    created_at INTEGER NOT NULL,
    CHECK ((order_id IS NOT NULL AND subscription_id IS NULL) OR (order_id IS NULL AND subscription_id IS NOT NULL))
);

CREATE TABLE IF NOT EXISTS commerce_invitations (
    id INTEGER PRIMARY KEY,
    user_id INTEGER NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
    email TEXT NOT NULL COLLATE NOCASE,
    token_hash BLOB NOT NULL UNIQUE,
    referral_id INTEGER REFERENCES commerce_referrals(id) ON DELETE SET NULL,
    delivery_status TEXT NOT NULL DEFAULT 'pending' CHECK (delivery_status IN ('pending', 'sending', 'sent')),
    delivery_claimed_at INTEGER,
    sent_at INTEGER,
    used_at INTEGER,
    created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS commerce_invitations_email ON commerce_invitations(email);
CREATE INDEX IF NOT EXISTS commerce_invitations_referral ON commerce_invitations(referral_id);

CREATE TABLE IF NOT EXISTS commerce_user_referrals (
    user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    referral_id INTEGER NOT NULL REFERENCES commerce_referrals(id) ON DELETE CASCADE,
    created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS commerce_user_referrals_referral ON commerce_user_referrals(referral_id);

CREATE TABLE IF NOT EXISTS commerce_webhook_events (
    stripe_event_id TEXT PRIMARY KEY,
    event_type TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('processing', 'failed', 'completed')),
    attempts INTEGER NOT NULL DEFAULT 1,
    last_error TEXT NOT NULL DEFAULT '',
    claimed_at INTEGER NOT NULL,
    completed_at INTEGER,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS commerce_webhook_events_status ON commerce_webhook_events(status, updated_at);
`

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("migrate commerce database: %w", err)
	}
	for _, migration := range []struct {
		table, column, definition string
	}{
		{"commerce_checkout_items", "reserved_quantity", "reserved_quantity INTEGER NOT NULL DEFAULT 0 CHECK (reserved_quantity >= 0 AND reserved_quantity <= quantity)"},
		{"commerce_orders", "email_delivery_claimed_at", "email_delivery_claimed_at INTEGER"},
		{"commerce_subscriptions", "email_delivery_claimed_at", "email_delivery_claimed_at INTEGER"},
		{"commerce_invitations", "delivery_claimed_at", "delivery_claimed_at INTEGER"},
	} {
		if err := s.ensureColumn(ctx, migration.table, migration.column, migration.definition); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) ensureColumn(ctx context.Context, table, column, definition string) error {
	found, err := s.columnExists(ctx, table, column)
	if err != nil {
		return err
	}
	if found {
		return nil
	}
	if _, err := s.db.ExecContext(ctx, `ALTER TABLE `+table+` ADD COLUMN `+definition); err != nil {
		// A concurrent process may have completed the same migration after inspection.
		found, inspectErr := s.columnExists(ctx, table, column)
		if inspectErr == nil && found {
			return nil
		}
		return fmt.Errorf("add %s.%s: %w", table, column, err)
	}
	return nil
}

func (s *Store) columnExists(ctx context.Context, table, column string) (bool, error) {
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		return false, fmt.Errorf("inspect %s schema: %w", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return false, fmt.Errorf("inspect %s column: %w", table, err)
		}
		if name == column {
			return true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("inspect %s schema rows: %w", table, err)
	}
	return false, nil
}
