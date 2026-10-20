package commerce

import (
	"context"
	"fmt"

	"github.com/bkroeze/oregon-dev-foundry/internal/media"
)

func (s *Store) ListProductImages(ctx context.Context, productID int64) ([]ProductImage, error) {
	if productID <= 0 {
		return nil, fmt.Errorf("%w: product id is required", ErrInvalidInput)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, product_id, COALESCE(sku_id, 0), COALESCE(addon_sku_id, 0), filename, width, height FROM commerce_product_images WHERE product_id=? ORDER BY COALESCE(sku_id, 0), COALESCE(addon_sku_id, 0)`, productID)
	if err != nil {
		return nil, fmt.Errorf("list product images: %w", err)
	}
	defer rows.Close()
	images := []ProductImage{}
	for rows.Next() {
		var image ProductImage
		if err := rows.Scan(&image.ID, &image.ProductID, &image.SKUID, &image.AddonSKUID, &image.Filename, &image.Width, &image.Height); err != nil {
			return nil, fmt.Errorf("read product image: %w", err)
		}
		images = append(images, image)
	}
	return images, rows.Err()
}

func (s *Store) SetProductImage(ctx context.Context, image ProductImage) error {
	if image.ProductID <= 0 || image.SKUID < 0 || image.AddonSKUID < 0 || (image.SKUID == 0 && image.AddonSKUID != 0) || !media.ValidFilename(image.Filename) || image.Width <= 0 || image.Height <= 0 || image.Width > media.MaxEdge || image.Height > media.MaxEdge {
		return fmt.Errorf("%w: invalid product image", ErrInvalidInput)
	}
	// Validate ownership and kinds in the same statement as the upsert. NULL
	// foreign keys represent the product default and the no-add-on selection.
	result, err := s.db.ExecContext(ctx, `INSERT INTO commerce_product_images(product_id, sku_id, addon_sku_id, filename, width, height)
		SELECT id, NULLIF(?, 0), NULLIF(?, 0), ?, ?, ? FROM commerce_products
		WHERE id=?
		AND (?=0 OR EXISTS (SELECT 1 FROM commerce_skus WHERE id=? AND product_id=commerce_products.id AND kind='primary'))
		AND (?=0 OR EXISTS (SELECT 1 FROM commerce_skus WHERE id=? AND product_id=commerce_products.id AND kind='addon'))
		ON CONFLICT DO UPDATE SET filename=excluded.filename, width=excluded.width, height=excluded.height`,
		image.SKUID, image.AddonSKUID, image.Filename, image.Width, image.Height, image.ProductID,
		image.SKUID, image.SKUID, image.AddonSKUID, image.AddonSKUID)
	if err != nil {
		return fmt.Errorf("save product image: %w", err)
	}
	if count, err := result.RowsAffected(); err != nil {
		return fmt.Errorf("save product image: %w", err)
	} else if count == 0 {
		return fmt.Errorf("%w: image selection does not belong to this product", ErrInvalidInput)
	}
	return nil
}
