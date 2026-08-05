package commerce

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

const productColumns = `id, slug, master_sku, name, description, product_type, active, created_at, updated_at`
const skuColumns = `id, product_id, sku, name, kind, options_json, price_cents, inventory_count, active, stripe_price_id, created_at, updated_at`
const shippingColumns = `id, product_id, name, amount_cents, active, sort_order, created_at, updated_at`

func (s *Store) CreateProduct(ctx context.Context, p ProductParams) (Product, error) {
	p.Slug = strings.ToLower(strings.TrimSpace(p.Slug))
	p.MasterSKU = strings.TrimSpace(p.MasterSKU)
	p.Name = strings.TrimSpace(p.Name)
	p.Description = strings.TrimSpace(p.Description)
	if p.Slug == "" || p.MasterSKU == "" || p.Name == "" {
		return Product{}, fmt.Errorf("%w: slug, master SKU, and name are required", ErrInvalidInput)
	}
	if !validProductType(p.Type) {
		return Product{}, ErrInvalidProductType
	}
	now := nowUnix(s)
	result, err := s.db.ExecContext(ctx, `INSERT INTO commerce_products (slug, master_sku, name, description, product_type, active, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, p.Slug, p.MasterSKU, p.Name, p.Description, p.Type, p.Active, now, now)
	if err != nil {
		return Product{}, fmt.Errorf("create product: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return Product{}, fmt.Errorf("read product id: %w", err)
	}
	return s.ProductByID(ctx, id)
}

func (s *Store) UpdateProduct(ctx context.Context, id int64, p ProductParams) (Product, error) {
	if id <= 0 {
		return Product{}, fmt.Errorf("%w: product id is required", ErrInvalidInput)
	}
	p.Slug = strings.ToLower(strings.TrimSpace(p.Slug))
	p.MasterSKU = strings.TrimSpace(p.MasterSKU)
	p.Name = strings.TrimSpace(p.Name)
	p.Description = strings.TrimSpace(p.Description)
	if p.Slug == "" || p.MasterSKU == "" || p.Name == "" {
		return Product{}, fmt.Errorf("%w: slug, master SKU, and name are required", ErrInvalidInput)
	}
	if !validProductType(p.Type) {
		return Product{}, ErrInvalidProductType
	}
	result, err := s.db.ExecContext(ctx, `UPDATE commerce_products SET slug=?, master_sku=?, name=?, description=?, product_type=?, active=?, updated_at=? WHERE id=?`, p.Slug, p.MasterSKU, p.Name, p.Description, p.Type, p.Active, nowUnix(s), id)
	if err != nil {
		return Product{}, fmt.Errorf("update product: %w", err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return Product{}, ErrNotFound
	}
	return s.ProductByID(ctx, id)
}

func (s *Store) ProductByID(ctx context.Context, id int64) (Product, error) {
	if id <= 0 {
		return Product{}, fmt.Errorf("%w: product id is required", ErrInvalidInput)
	}
	return s.product(ctx, `SELECT `+productColumns+` FROM commerce_products WHERE id=?`, id)
}

func (s *Store) ProductBySlug(ctx context.Context, slug string) (Product, error) {
	slug = strings.TrimSpace(slug)
	if slug == "" {
		return Product{}, fmt.Errorf("%w: slug is required", ErrInvalidInput)
	}
	return s.product(ctx, `SELECT `+productColumns+` FROM commerce_products WHERE slug=? COLLATE NOCASE`, slug)
}

func (s *Store) ProductByMasterSKU(ctx context.Context, masterSKU string) (Product, error) {
	masterSKU = strings.TrimSpace(masterSKU)
	if masterSKU == "" {
		return Product{}, fmt.Errorf("%w: master SKU is required", ErrInvalidInput)
	}
	return s.product(ctx, `SELECT `+productColumns+` FROM commerce_products WHERE master_sku=? COLLATE NOCASE`, masterSKU)
}

func (s *Store) ProductBySKU(ctx context.Context, sku string) (Product, error) {
	sku = strings.TrimSpace(sku)
	if sku == "" {
		return Product{}, fmt.Errorf("%w: SKU is required", ErrInvalidInput)
	}
	return s.product(ctx, `SELECT p.`+strings.ReplaceAll(productColumns, ", ", ", p.")+` FROM commerce_products p JOIN commerce_skus s ON s.product_id=p.id WHERE s.sku=? COLLATE NOCASE`, sku)
}

func (s *Store) FindProduct(ctx context.Context, value string) (Product, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return Product{}, fmt.Errorf("%w: product lookup is required", ErrInvalidInput)
	}
	p, err := s.product(ctx, `SELECT DISTINCT p.`+strings.ReplaceAll(productColumns, ", ", ", p.")+` FROM commerce_products p LEFT JOIN commerce_skus s ON s.product_id=p.id WHERE p.slug=? COLLATE NOCASE OR p.master_sku=? COLLATE NOCASE OR s.sku=? COLLATE NOCASE LIMIT 1`, value, value, value)
	return p, err
}

func (s *Store) SellableProductBySlug(ctx context.Context, slug string) (Product, error) {
	slug = strings.TrimSpace(slug)
	if slug == "" {
		return Product{}, fmt.Errorf("%w: slug is required", ErrInvalidInput)
	}
	return s.sellableProduct(ctx, `p.slug=? COLLATE NOCASE`, slug)
}

func (s *Store) SellableProductByMasterSKU(ctx context.Context, masterSKU string) (Product, error) {
	masterSKU = strings.TrimSpace(masterSKU)
	if masterSKU == "" {
		return Product{}, fmt.Errorf("%w: master SKU is required", ErrInvalidInput)
	}
	return s.sellableProduct(ctx, `p.master_sku=? COLLATE NOCASE`, masterSKU)
}

func (s *Store) SellableProductBySKU(ctx context.Context, sku string) (Product, error) {
	sku = strings.TrimSpace(sku)
	if sku == "" {
		return Product{}, fmt.Errorf("%w: SKU is required", ErrInvalidInput)
	}
	return s.sellableProduct(ctx, `EXISTS (SELECT 1 FROM commerce_skus lookup WHERE lookup.product_id=p.id AND lookup.sku=? COLLATE NOCASE AND lookup.active=1)`, sku)
}

func (s *Store) sellableProduct(ctx context.Context, predicate string, args ...any) (Product, error) {
	query := `SELECT p.` + strings.ReplaceAll(productColumns, ", ", ", p.") + ` FROM commerce_products p WHERE p.active=1 AND ` + predicate + ` AND EXISTS (SELECT 1 FROM commerce_skus primary_sku WHERE primary_sku.product_id=p.id AND primary_sku.kind='primary' AND primary_sku.active=1 AND (primary_sku.inventory_count IS NULL OR primary_sku.inventory_count>0)) LIMIT 1`
	product, err := scanProduct(s.db.QueryRowContext(ctx, query, args...))
	if err != nil {
		return Product{}, mapNotFound(err)
	}
	if err := s.loadProductRelations(ctx, &product, true); err != nil {
		return Product{}, err
	}
	return product, nil
}

func (s *Store) ListSellableProducts(ctx context.Context) ([]Product, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT p.`+strings.ReplaceAll(productColumns, ", ", ", p.")+` FROM commerce_products p JOIN commerce_skus s ON s.product_id=p.id WHERE p.active=1 AND s.active=1 AND s.kind='primary' AND (s.inventory_count IS NULL OR s.inventory_count > 0) ORDER BY p.name, p.id`)
	if err != nil {
		return nil, fmt.Errorf("list sellable products: %w", err)
	}
	products := make([]Product, 0)
	for rows.Next() {
		p, scanErr := scanProduct(rows)
		if scanErr != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("scan product: %w", scanErr)
		}
		products = append(products, p)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("list products rows: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close products rows: %w", err)
	}
	for i := range products {
		if err := s.loadProductRelations(ctx, &products[i], true); err != nil {
			return nil, err
		}
	}
	return products, nil
}

func (s *Store) ListProducts(ctx context.Context) ([]Product, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+productColumns+` FROM commerce_products ORDER BY name,id`)
	if err != nil {
		return nil, fmt.Errorf("list products: %w", err)
	}
	products := make([]Product, 0)
	for rows.Next() {
		product, scanErr := scanProduct(rows)
		if scanErr != nil {
			_ = rows.Close()
			return nil, scanErr
		}
		products = append(products, product)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for i := range products {
		if err := s.loadProductRelations(ctx, &products[i], false); err != nil {
			return nil, err
		}
	}
	return products, nil
}

func (s *Store) product(ctx context.Context, query string, args ...any) (Product, error) {
	p, err := scanProduct(s.db.QueryRowContext(ctx, query, args...))
	if err != nil {
		return Product{}, mapNotFound(err)
	}
	if err := s.loadProductRelations(ctx, &p, false); err != nil {
		return Product{}, err
	}
	return p, nil
}

func scanProduct(row scanner) (Product, error) {
	var p Product
	var created, updated int64
	if err := row.Scan(&p.ID, &p.Slug, &p.MasterSKU, &p.Name, &p.Description, &p.Type, &p.Active, &created, &updated); err != nil {
		return Product{}, err
	}
	p.CreatedAt = time.Unix(created, 0).UTC()
	p.UpdatedAt = time.Unix(updated, 0).UTC()
	return p, nil
}

func (s *Store) loadProductRelations(ctx context.Context, p *Product, sellableOnly bool) error {
	query := `SELECT ` + skuColumns + ` FROM commerce_skus WHERE product_id=?`
	if sellableOnly {
		query += ` AND active=1 AND (inventory_count IS NULL OR inventory_count > 0)`
	}
	query += ` ORDER BY kind, id`
	rows, err := s.db.QueryContext(ctx, query, p.ID)
	if err != nil {
		return fmt.Errorf("load product skus: %w", err)
	}
	var all []SKU
	for rows.Next() {
		sku, scanErr := scanSKU(rows)
		if scanErr != nil {
			_ = rows.Close()
			return scanErr
		}
		all = append(all, sku)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	p.SKUs = make([]SKU, 0)
	p.Addons = make([]SKU, 0)
	for _, sku := range all {
		if sku.Kind == SKUKindAddon {
			p.Addons = append(p.Addons, sku)
		} else {
			p.SKUs = append(p.SKUs, sku)
		}
	}
	shippingQuery := `SELECT ` + shippingColumns + ` FROM commerce_product_shipping_options WHERE product_id=?`
	if sellableOnly {
		shippingQuery += ` AND active=1`
	}
	shippingQuery += ` ORDER BY sort_order,id`
	sr, err := s.db.QueryContext(ctx, shippingQuery, p.ID)
	if err != nil {
		return fmt.Errorf("load shipping options: %w", err)
	}
	p.ShippingOptions = make([]ShippingOption, 0)
	for sr.Next() {
		option, scanErr := scanShipping(sr)
		if scanErr != nil {
			_ = sr.Close()
			return scanErr
		}
		p.ShippingOptions = append(p.ShippingOptions, option)
	}
	if err := sr.Err(); err != nil {
		_ = sr.Close()
		return err
	}
	return sr.Close()
}

func (s *Store) CreateSKU(ctx context.Context, p SKUParams) (SKU, error) { return s.createSKU(ctx, p) }
func (s *Store) CreateAddon(ctx context.Context, p SKUParams) (SKU, error) {
	p.Kind = SKUKindAddon
	return s.createSKU(ctx, p)
}
func (s *Store) createSKU(ctx context.Context, p SKUParams) (SKU, error) {
	if p.ProductID <= 0 || strings.TrimSpace(p.SKU) == "" || strings.TrimSpace(p.Name) == "" || p.PriceCents < 0 || !validSKUKind(p.Kind) || (p.Inventory != nil && *p.Inventory < 0) {
		return SKU{}, fmt.Errorf("%w: invalid SKU fields", ErrInvalidInput)
	}
	options, err := canonicalOptions(p.OptionsJSON)
	if err != nil {
		return SKU{}, err
	}
	p.SKU = strings.TrimSpace(p.SKU)
	p.Name = strings.TrimSpace(p.Name)
	p.StripePriceID = strings.TrimSpace(p.StripePriceID)
	product, err := s.ProductByID(ctx, p.ProductID)
	if err != nil {
		return SKU{}, err
	}
	if product.Type == ProductTypeSubscription && p.Kind == SKUKindAddon {
		return SKU{}, fmt.Errorf("%w: subscriptions cannot have add-ons", ErrInvalidProductType)
	}
	now := nowUnix(s)
	result, err := s.db.ExecContext(ctx, `INSERT INTO commerce_skus(product_id,sku,name,kind,options_json,price_cents,inventory_count,active,stripe_price_id,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, p.ProductID, p.SKU, p.Name, p.Kind, options, p.PriceCents, nullableInt64(p.Inventory), p.Active, p.StripePriceID, now, now)
	if err != nil {
		return SKU{}, fmt.Errorf("create SKU: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return SKU{}, err
	}
	return s.SKUByID(ctx, id)
}

func (s *Store) UpdateSKU(ctx context.Context, id int64, p SKUParams) (SKU, error) {
	if id <= 0 || p.ProductID <= 0 || strings.TrimSpace(p.SKU) == "" || strings.TrimSpace(p.Name) == "" || p.PriceCents < 0 || !validSKUKind(p.Kind) || (p.Inventory != nil && *p.Inventory < 0) {
		return SKU{}, fmt.Errorf("%w: invalid SKU fields", ErrInvalidInput)
	}
	options, err := canonicalOptions(p.OptionsJSON)
	if err != nil {
		return SKU{}, err
	}
	product, err := s.ProductByID(ctx, p.ProductID)
	if err != nil {
		return SKU{}, err
	}
	if product.Type == ProductTypeSubscription && p.Kind == SKUKindAddon {
		return SKU{}, fmt.Errorf("%w: subscriptions cannot have add-ons", ErrInvalidProductType)
	}
	result, err := s.db.ExecContext(ctx, `UPDATE commerce_skus SET product_id=?,sku=?,name=?,kind=?,options_json=?,price_cents=?,inventory_count=?,active=?,stripe_price_id=?,updated_at=? WHERE id=?`, p.ProductID, strings.TrimSpace(p.SKU), strings.TrimSpace(p.Name), p.Kind, options, p.PriceCents, nullableInt64(p.Inventory), p.Active, strings.TrimSpace(p.StripePriceID), nowUnix(s), id)
	if err != nil {
		return SKU{}, fmt.Errorf("update SKU: %w", err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return SKU{}, ErrNotFound
	}
	return s.SKUByID(ctx, id)
}

func (s *Store) UpdateAddon(ctx context.Context, id int64, p SKUParams) (SKU, error) {
	p.Kind = SKUKindAddon
	return s.UpdateSKU(ctx, id, p)
}

func (s *Store) SKUByID(ctx context.Context, id int64) (SKU, error) {
	if id <= 0 {
		return SKU{}, fmt.Errorf("%w: SKU id is required", ErrInvalidInput)
	}
	sku, err := scanSKU(s.db.QueryRowContext(ctx, `SELECT `+skuColumns+` FROM commerce_skus WHERE id=?`, id))
	if err != nil {
		return SKU{}, mapNotFound(err)
	}
	return sku, nil
}
func (s *Store) SKUByCode(ctx context.Context, code string) (SKU, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return SKU{}, fmt.Errorf("%w: SKU is required", ErrInvalidInput)
	}
	sku, err := scanSKU(s.db.QueryRowContext(ctx, `SELECT `+skuColumns+` FROM commerce_skus WHERE sku=? COLLATE NOCASE`, code))
	if err != nil {
		return SKU{}, mapNotFound(err)
	}
	return sku, nil
}
func (s *Store) SKUBySKU(ctx context.Context, code string) (SKU, error) {
	return s.SKUByCode(ctx, code)
}
func (s *Store) ResolveSKU(ctx context.Context, productID int64, optionsJSON string) (SKU, error) {
	if productID <= 0 {
		return SKU{}, fmt.Errorf("%w: product id is required", ErrInvalidInput)
	}
	options, err := canonicalOptions(optionsJSON)
	if err != nil {
		return SKU{}, err
	}
	sku, err := scanSKU(s.db.QueryRowContext(ctx, `SELECT s.`+strings.ReplaceAll(skuColumns, `, `, `, s.`)+` FROM commerce_skus s JOIN commerce_products p ON p.id=s.product_id WHERE s.product_id=? AND s.kind='primary' AND s.options_json=? AND s.active=1 AND p.active=1 AND (s.inventory_count IS NULL OR s.inventory_count>0)`, productID, options))
	if err != nil {
		return SKU{}, mapNotFound(err)
	}
	return sku, nil
}

func scanSKU(row scanner) (SKU, error) {
	var s SKU
	var inventory sql.NullInt64
	var created, updated int64
	if err := row.Scan(&s.ID, &s.ProductID, &s.SKU, &s.Name, &s.Kind, &s.OptionsJSON, &s.PriceCents, &inventory, &s.Active, &s.StripePriceID, &created, &updated); err != nil {
		return SKU{}, err
	}
	s.Inventory = ptrInt64(inventory)
	s.CreatedAt = time.Unix(created, 0).UTC()
	s.UpdatedAt = time.Unix(updated, 0).UTC()
	return s, nil
}

// DeleteSKU deactivates a SKU so historical checkout and order snapshots remain valid.
func (s *Store) DeleteSKU(ctx context.Context, id int64) error {
	if id <= 0 {
		return fmt.Errorf("%w: SKU id is required", ErrInvalidInput)
	}
	result, err := s.db.ExecContext(ctx, `UPDATE commerce_skus SET active=0,updated_at=? WHERE id=?`, nowUnix(s), id)
	if err != nil {
		return fmt.Errorf("deactivate SKU: %w", err)
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) CreateShippingOption(ctx context.Context, p ShippingOptionParams) (ShippingOption, error) {
	if p.ProductID <= 0 || strings.TrimSpace(p.Name) == "" || p.AmountCents < 0 {
		return ShippingOption{}, fmt.Errorf("%w: invalid shipping option", ErrInvalidInput)
	}
	product, err := s.ProductByID(ctx, p.ProductID)
	if err != nil {
		return ShippingOption{}, err
	}
	if product.Type != ProductTypeOneTime {
		return ShippingOption{}, ErrInvalidProductType
	}
	now := nowUnix(s)
	result, err := s.db.ExecContext(ctx, `INSERT INTO commerce_product_shipping_options(product_id,name,amount_cents,active,sort_order,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, p.ProductID, strings.TrimSpace(p.Name), p.AmountCents, p.Active, p.SortOrder, now, now)
	if err != nil {
		return ShippingOption{}, fmt.Errorf("create shipping option: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return ShippingOption{}, err
	}
	return s.ShippingOptionByID(ctx, id)
}
func (s *Store) AddShippingOption(ctx context.Context, p ShippingOptionParams) (ShippingOption, error) {
	return s.CreateShippingOption(ctx, p)
}
func (s *Store) UpdateShippingOption(ctx context.Context, id int64, p ShippingOptionParams) (ShippingOption, error) {
	if id <= 0 || p.ProductID <= 0 || strings.TrimSpace(p.Name) == "" || p.AmountCents < 0 {
		return ShippingOption{}, fmt.Errorf("%w: invalid shipping option", ErrInvalidInput)
	}
	product, err := s.ProductByID(ctx, p.ProductID)
	if err != nil {
		return ShippingOption{}, err
	}
	if product.Type != ProductTypeOneTime {
		return ShippingOption{}, ErrInvalidProductType
	}
	result, err := s.db.ExecContext(ctx, `UPDATE commerce_product_shipping_options SET product_id=?,name=?,amount_cents=?,active=?,sort_order=?,updated_at=? WHERE id=?`, p.ProductID, strings.TrimSpace(p.Name), p.AmountCents, p.Active, p.SortOrder, nowUnix(s), id)
	if err != nil {
		return ShippingOption{}, fmt.Errorf("update shipping option: %w", err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return ShippingOption{}, ErrNotFound
	}
	return s.ShippingOptionByID(ctx, id)
}
func (s *Store) ShippingOptionByID(ctx context.Context, id int64) (ShippingOption, error) {
	if id <= 0 {
		return ShippingOption{}, fmt.Errorf("%w: shipping option id required", ErrInvalidInput)
	}
	option, err := scanShipping(s.db.QueryRowContext(ctx, `SELECT `+shippingColumns+` FROM commerce_product_shipping_options WHERE id=?`, id))
	if err != nil {
		return ShippingOption{}, mapNotFound(err)
	}
	return option, nil
}
func scanShipping(row scanner) (ShippingOption, error) {
	var option ShippingOption
	var created, updated int64
	if err := row.Scan(&option.ID, &option.ProductID, &option.Name, &option.AmountCents, &option.Active, &option.SortOrder, &created, &updated); err != nil {
		return ShippingOption{}, err
	}
	option.CreatedAt = time.Unix(created, 0).UTC()
	option.UpdatedAt = time.Unix(updated, 0).UTC()
	return option, nil
}

// DeleteShippingOption deactivates an option while preserving completed checkout snapshots.
func (s *Store) DeleteShippingOption(ctx context.Context, id int64) error {
	if id <= 0 {
		return fmt.Errorf("%w: shipping option id is required", ErrInvalidInput)
	}
	result, err := s.db.ExecContext(ctx, `UPDATE commerce_product_shipping_options SET active=0,updated_at=? WHERE id=?`, nowUnix(s), id)
	if err != nil {
		return fmt.Errorf("deactivate shipping option: %w", err)
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return ErrNotFound
	}
	return nil
}

var _ = errors.Is
