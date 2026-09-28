package commerce

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// ProductFixture describes one product and its sellable catalog records.
type ProductFixture struct {
	Slug            string                  `json:"slug"`
	MasterSKU       string                  `json:"master_sku"`
	Name            string                  `json:"name"`
	Description     string                  `json:"description"`
	Type            ProductType             `json:"type"`
	Active          bool                    `json:"active"`
	SKUs            []SKUFixture            `json:"skus"`
	ShippingOptions []ShippingOptionFixture `json:"shipping_options"`
}

// SKUFixture describes a primary or add-on SKU belonging to a product fixture.
type SKUFixture struct {
	SKU           string          `json:"sku"`
	Name          string          `json:"name"`
	Kind          SKUKind         `json:"kind"`
	Options       json.RawMessage `json:"options"`
	PriceCents    int64           `json:"price_cents"`
	Inventory     *int64          `json:"inventory"`
	Active        bool            `json:"active"`
	StripePriceID string          `json:"stripe_price_id"`
}

// ShippingOptionFixture describes a fixed shipping option for a product.
type ShippingOptionFixture struct {
	Name        string `json:"name"`
	AmountCents int64  `json:"amount_cents"`
	Active      bool   `json:"active"`
	SortOrder   int    `json:"sort_order"`
}

// DecodeProductFixture decodes exactly one JSON product fixture.
func DecodeProductFixture(r io.Reader) (ProductFixture, error) {
	var fixture ProductFixture
	decoder := json.NewDecoder(r)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&fixture); err != nil {
		return ProductFixture{}, fmt.Errorf("decode product fixture: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return ProductFixture{}, fmt.Errorf("decode product fixture: multiple JSON values")
		}
		return ProductFixture{}, fmt.Errorf("decode product fixture: %w", err)
	}
	return fixture, nil
}

// ApplyProductFixture idempotently creates or updates a product and its
// fixture-defined SKUs and shipping options. Existing records not listed in
// the fixture are left unchanged.
func (s *Store) ApplyProductFixture(ctx context.Context, fixture ProductFixture) (Product, error) {
	if strings.TrimSpace(fixture.Slug) == "" || strings.TrimSpace(fixture.MasterSKU) == "" || strings.TrimSpace(fixture.Name) == "" {
		return Product{}, fmt.Errorf("%w: fixture slug, master SKU, and name are required", ErrInvalidInput)
	}
	if !validProductType(fixture.Type) {
		return Product{}, ErrInvalidProductType
	}

	product, found, err := s.fixtureProduct(ctx, fixture)
	if err != nil {
		return Product{}, err
	}
	params := ProductParams{
		Slug:        fixture.Slug,
		MasterSKU:   fixture.MasterSKU,
		Name:        fixture.Name,
		Description: fixture.Description,
		Type:        fixture.Type,
		Active:      fixture.Active,
	}
	if found {
		product, err = s.UpdateProduct(ctx, product.ID, params)
	} else {
		product, err = s.CreateProduct(ctx, params)
	}
	if err != nil {
		return Product{}, err
	}

	for _, spec := range fixture.SKUs {
		code := strings.TrimSpace(spec.SKU)
		if code == "" {
			return Product{}, fmt.Errorf("%w: fixture SKU is required", ErrInvalidInput)
		}
		existing, lookupErr := s.SKUByCode(ctx, code)
		if lookupErr != nil && !errors.Is(lookupErr, ErrNotFound) {
			return Product{}, lookupErr
		}
		if lookupErr == nil && existing.ProductID != product.ID {
			return Product{}, fmt.Errorf("%w: SKU %q belongs to another product", ErrInvalidInput, code)
		}
		params := SKUParams{
			ProductID:     product.ID,
			SKU:           spec.SKU,
			Name:          spec.Name,
			Kind:          spec.Kind,
			OptionsJSON:   string(spec.Options),
			PriceCents:    spec.PriceCents,
			Inventory:     spec.Inventory,
			Active:        spec.Active,
			StripePriceID: spec.StripePriceID,
		}
		if lookupErr == nil {
			if _, err = s.UpdateSKU(ctx, existing.ID, params); err != nil {
				return Product{}, err
			}
		} else if _, err = s.CreateSKU(ctx, params); err != nil {
			return Product{}, err
		}
	}

	for _, spec := range fixture.ShippingOptions {
		name := strings.TrimSpace(spec.Name)
		if name == "" {
			return Product{}, fmt.Errorf("%w: fixture shipping option name is required", ErrInvalidInput)
		}
		var existing *ShippingOption
		for i := range product.ShippingOptions {
			if strings.EqualFold(product.ShippingOptions[i].Name, name) {
				existing = &product.ShippingOptions[i]
				break
			}
		}
		params := ShippingOptionParams{ProductID: product.ID, Name: spec.Name, AmountCents: spec.AmountCents, Active: spec.Active, SortOrder: spec.SortOrder}
		if existing != nil {
			if _, err = s.UpdateShippingOption(ctx, existing.ID, params); err != nil {
				return Product{}, err
			}
		} else if _, err = s.CreateShippingOption(ctx, params); err != nil {
			return Product{}, err
		}
	}
	return s.ProductByID(ctx, product.ID)
}

func (s *Store) fixtureProduct(ctx context.Context, fixture ProductFixture) (Product, bool, error) {
	bySlug, slugErr := s.ProductBySlug(ctx, fixture.Slug)
	if slugErr != nil && !errors.Is(slugErr, ErrNotFound) {
		return Product{}, false, slugErr
	}
	byMasterSKU, masterErr := s.ProductByMasterSKU(ctx, fixture.MasterSKU)
	if masterErr != nil && !errors.Is(masterErr, ErrNotFound) {
		return Product{}, false, masterErr
	}
	slugFound := slugErr == nil
	masterFound := masterErr == nil
	if slugFound && masterFound && bySlug.ID != byMasterSKU.ID {
		return Product{}, false, fmt.Errorf("%w: fixture slug and master SKU identify different products", ErrInvalidInput)
	}
	if slugFound {
		return bySlug, true, nil
	}
	if masterFound {
		return byMasterSKU, true, nil
	}
	return Product{}, false, nil
}
