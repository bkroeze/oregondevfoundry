package commerce

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/bkroeze/oregon-dev-foundry/internal/auth"
)

func TestDeliveryClaimsAreLeased(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	if _, err := store.db.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
		t.Fatal(err)
	}
	const id int64 = 41
	if _, err := store.db.ExecContext(ctx, `INSERT INTO commerce_orders(id,checkout_id,stripe_session_id,buyer_email,buyer_name,shipping_name,shipping_amount_cents,subtotal_cents,total_cents,created_at) VALUES(?,1,'cs_order','buyer@example.com','Buyer','',0,100,100,1)`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `INSERT INTO commerce_subscriptions(id,checkout_id,stripe_session_id,stripe_subscription_id,product_id,sku_id,buyer_email,buyer_name,status,created_at,updated_at) VALUES(?,2,'cs_subscription','sub_1',1,1,'buyer@example.com','Buyer','active',1,1)`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `INSERT INTO commerce_invitations(id,user_id,email,token_hash,created_at) VALUES(?,?, 'buyer@example.com',?,1)`, id, id, []byte("token")); err != nil {
		t.Fatal(err)
	}

	type deliveryCase struct {
		name          string
		claim         func(context.Context, int64) (bool, error)
		release       func(context.Context, int64) error
		markSent      func(context.Context, int64) error
		table         string
		statusColumn  string
		claimedColumn string
	}
	cases := []deliveryCase{
		{"order", store.ClaimOrderEmail, store.ReleaseOrderEmail, store.MarkOrderEmailSent, "commerce_orders", "email_delivery_status", "email_delivery_claimed_at"},
		{"subscription", store.ClaimSubscriptionEmail, store.ReleaseSubscriptionEmail, store.MarkSubscriptionEmailSent, "commerce_subscriptions", "email_delivery_status", "email_delivery_claimed_at"},
		{"invitation", store.ClaimInvitationDelivery, store.ReleaseInvitationDelivery, store.MarkInvitationSent, "commerce_invitations", "delivery_status", "delivery_claimed_at"},
	}

	base := time.Date(2026, time.July, 21, 12, 0, 0, 0, time.UTC)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			now := base
			store.now = func() time.Time { return now }

			claimed, err := tc.claim(ctx, id)
			if err != nil || !claimed {
				t.Fatalf("initial claim = %v, %v", claimed, err)
			}
			assertDeliveryState(t, store.db, tc.table, tc.statusColumn, tc.claimedColumn, id, "sending", base.Unix())

			claimed, err = tc.claim(ctx, id)
			if !errors.Is(err, ErrDeliveryInProgress) || claimed {
				t.Fatalf("immediate duplicate claim = %v, %v", claimed, err)
			}
			now = base.Add(deliveryClaimLease - time.Second)
			claimed, err = tc.claim(ctx, id)
			if !errors.Is(err, ErrDeliveryInProgress) || claimed {
				t.Fatalf("non-stale claim = %v, %v", claimed, err)
			}

			now = base.Add(deliveryClaimLease)
			claimed, err = tc.claim(ctx, id)
			if err != nil || !claimed {
				t.Fatalf("stale claim = %v, %v", claimed, err)
			}
			assertDeliveryState(t, store.db, tc.table, tc.statusColumn, tc.claimedColumn, id, "sending", now.Unix())

			if err := tc.release(ctx, id); err != nil {
				t.Fatalf("release: %v", err)
			}
			assertDeliveryState(t, store.db, tc.table, tc.statusColumn, tc.claimedColumn, id, "pending", 0)
			claimed, err = tc.claim(ctx, id)
			if err != nil || !claimed {
				t.Fatalf("retry after release = %v, %v", claimed, err)
			}

			if err := tc.markSent(ctx, id); err != nil {
				t.Fatalf("mark sent: %v", err)
			}
			assertDeliveryState(t, store.db, tc.table, tc.statusColumn, tc.claimedColumn, id, "sent", 0)
			now = now.Add(2 * deliveryClaimLease)
			claimed, err = tc.claim(ctx, id)
			if err != nil || claimed {
				t.Fatalf("sent claim = %v, %v", claimed, err)
			}
		})
	}
}

func assertDeliveryState(t *testing.T, db *sql.DB, table, statusColumn, claimedColumn string, id int64, wantStatus string, wantClaimed int64) {
	t.Helper()
	var status string
	var claimed sql.NullInt64
	query := fmt.Sprintf(`SELECT %s,%s FROM %s WHERE id=?`, statusColumn, claimedColumn, table)
	if err := db.QueryRow(query, id).Scan(&status, &claimed); err != nil {
		t.Fatal(err)
	}
	if status != wantStatus {
		t.Fatalf("status = %q, want %q", status, wantStatus)
	}
	if wantClaimed == 0 {
		if claimed.Valid {
			t.Fatalf("claimed timestamp = %d, want NULL", claimed.Int64)
		}
	} else if !claimed.Valid || claimed.Int64 != wantClaimed {
		t.Fatalf("claimed timestamp = %v, want %d", claimed, wantClaimed)
	}
}

func TestDeliveryClaimMigrationUpgradesExistingSchemaAndRepeats(t *testing.T) {
	authStore, err := auth.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer authStore.Close()
	ctx := context.Background()
	const oldDeliveryTables = `
CREATE TABLE commerce_orders (
    id INTEGER PRIMARY KEY,
    checkout_id INTEGER NOT NULL UNIQUE,
    stripe_session_id TEXT NOT NULL UNIQUE,
    user_id INTEGER,
    referral_id INTEGER,
    buyer_email TEXT NOT NULL,
    buyer_name TEXT NOT NULL,
    shipping_name TEXT NOT NULL DEFAULT '',
    shipping_amount_cents INTEGER NOT NULL,
    subtotal_cents INTEGER NOT NULL,
    total_cents INTEGER NOT NULL,
    email_delivery_status TEXT NOT NULL DEFAULT 'pending',
    email_sent_at INTEGER,
    created_at INTEGER NOT NULL
);
CREATE TABLE commerce_subscriptions (
    id INTEGER PRIMARY KEY,
    checkout_id INTEGER NOT NULL UNIQUE,
    stripe_session_id TEXT NOT NULL UNIQUE,
    stripe_subscription_id TEXT NOT NULL UNIQUE,
    user_id INTEGER,
    referral_id INTEGER,
    product_id INTEGER NOT NULL,
    sku_id INTEGER NOT NULL,
    buyer_email TEXT NOT NULL,
    buyer_name TEXT NOT NULL,
    status TEXT NOT NULL,
    current_period_end INTEGER,
    email_delivery_status TEXT NOT NULL DEFAULT 'pending',
    email_sent_at INTEGER,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
CREATE TABLE commerce_invitations (
    id INTEGER PRIMARY KEY,
    user_id INTEGER NOT NULL UNIQUE,
    email TEXT NOT NULL COLLATE NOCASE,
    token_hash BLOB NOT NULL UNIQUE,
    referral_id INTEGER,
    delivery_status TEXT NOT NULL DEFAULT 'pending',
    sent_at INTEGER,
    used_at INTEGER,
    created_at INTEGER NOT NULL
);`
	if _, err := authStore.DB().ExecContext(ctx, oldDeliveryTables); err != nil {
		t.Fatal(err)
	}
	if _, err := authStore.DB().ExecContext(ctx, `INSERT INTO commerce_orders(id,checkout_id,stripe_session_id,buyer_email,buyer_name,shipping_name,shipping_amount_cents,subtotal_cents,total_cents,email_delivery_status,created_at) VALUES(99,99,'cs_legacy','legacy@example.com','Legacy','',0,100,100,'sending',1)`); err != nil {
		t.Fatal(err)
	}
	store, err := New(authStore.DB())
	if err != nil {
		t.Fatalf("upgrade migration: %v", err)
	}
	if _, err := New(authStore.DB()); err != nil {
		t.Fatalf("repeated migration: %v", err)
	}

	for _, column := range []struct{ table, name string }{
		{"commerce_orders", "email_delivery_claimed_at"},
		{"commerce_subscriptions", "email_delivery_claimed_at"},
		{"commerce_invitations", "delivery_claimed_at"},
	} {
		store := &Store{db: authStore.DB()}
		found, err := store.columnExists(ctx, column.table, column.name)
		if err != nil {
			t.Fatal(err)
		}
		if !found {
			t.Errorf("missing migrated column %s.%s", column.table, column.name)
		}
	}
	claimedAt := time.Date(2026, time.July, 21, 14, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return claimedAt }
	claimed, err := store.ClaimOrderEmail(ctx, 99)
	if err != nil || !claimed {
		t.Fatalf("claim legacy sending delivery = %v, %v", claimed, err)
	}
	assertDeliveryState(t, store.db, "commerce_orders", "email_delivery_status", "email_delivery_claimed_at", 99, "sending", claimedAt.Unix())
}
