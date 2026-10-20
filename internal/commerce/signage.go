package commerce

import (
	"context"
	"fmt"
)

// EnsureSignageCatalog initializes the preview catalog once. Application startup
// calls it explicitly; opening a store never inserts business catalog records.
// Existing catalogs, including incomplete ones, belong to their administrator.
func (s *Store) EnsureSignageCatalog(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin signage catalog initialization: %w", err)
	}
	defer tx.Rollback()
	now := nowUnix(s)
	// Claim the seed before inspecting products, acquiring the SQLite write
	// lock and retaining a tombstone if an administrator later deletes it.
	result, err := tx.ExecContext(ctx, `INSERT INTO commerce_catalog_seeds(name, created_at) VALUES('vending-signage', ?) ON CONFLICT(name) DO NOTHING`, now)
	if err != nil {
		return fmt.Errorf("claim signage catalog initialization: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return tx.Commit()
	}
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM commerce_products WHERE slug='vending-signage' COLLATE NOCASE)`).Scan(&exists); err != nil {
		return fmt.Errorf("inspect signage catalog: %w", err)
	}
	if exists {
		return tx.Commit()
	}
	result, err = tx.ExecContext(ctx, `INSERT INTO commerce_products(slug, master_sku, name, description, product_type, active, created_at, updated_at) VALUES('vending-signage', 'SLATE-SIGNAGE', 'Slate signage', 'Preview slate signage options and contact us to inquire. Saved photos are previews, not generated artwork.', 'one_time', 1, ?, ?)`, now, now)
	if err != nil {
		return fmt.Errorf("initialize signage product: %w", err)
	}
	productID, err := result.LastInsertId()
	if err != nil {
		return err
	}
	variants := []struct {
		code, name, options string
		kind                SKUKind
		active              bool
	}{
		{"SLATE-4X4", "4x4 Slate — Tabletop display", `{"size":"4x4"}`, SKUKindPrimary, true},
		{"SLATE-12X8", "12x8 Slate", `{"size":"12x8"}`, SKUKindPrimary, false},
		{"SLATE-8X4", "8x4 Slate", `{"size":"8x4"}`, SKUKindPrimary, false},
		{"SLATE-9X6", "9x6 Slate", `{"size":"9x6"}`, SKUKindPrimary, false},
		{"SLATE-STAND", "Wooden stand", `{}`, SKUKindAddon, true},
	}
	for _, variant := range variants {
		// These are inquiry/preview options, not purchasable stock. Zero
		// inventory prevents existing cart routes from selling unpriced SKUs.
		if _, err := tx.ExecContext(ctx, `INSERT INTO commerce_skus(product_id, sku, name, kind, options_json, price_cents, inventory_count, active, created_at, updated_at) VALUES(?, ?, ?, ?, ?, 0, 0, ?, ?, ?)`, productID, variant.code, variant.name, variant.kind, variant.options, variant.active, now, now); err != nil {
			return fmt.Errorf("initialize signage SKU: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit signage catalog initialization: %w", err)
	}
	return nil
}
