package web

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bkroeze/oregon-dev-foundry/internal/commerce"
	"github.com/bkroeze/oregon-dev-foundry/internal/config"
	"github.com/bkroeze/oregon-dev-foundry/internal/templates"
)

func TestSignageSelectionUsesActiveNotInventory(t *testing.T) {
	zero := int64(0)
	product := commerce.Product{ID: 1, SKUs: []commerce.SKU{
		{ID: 10, Name: "12x8 Slate", Active: false},
		{ID: 11, Name: "4x4 Slate", Active: true, Inventory: &zero},
		{ID: 12, Name: "8x4 Slate", Active: true},
	}, Addons: []commerce.SKU{{ID: 20, SKU: "SLATE-STAND", Active: true, Inventory: &zero}}}
	data := signagePageData(product, nil)
	if len(data.Options) != 3 || data.Options[0].Active || data.Options[0].Selected || !data.Options[1].Selected || data.Options[2].Selected {
		t.Fatalf("unexpected selections: %+v", data.Options)
	}
	if !data.Available || !data.StandActive || data.StandID != "20" || data.ImageURL != "" {
		t.Fatalf("unexpected preview state: %+v", data)
	}
}

func TestSignageSelectionRecognizesLegacyStandSKU(t *testing.T) {
	data := signagePageData(commerce.Product{
		SKUs:   []commerce.SKU{{ID: 11, Name: "4x4 Slate", Active: true}},
		Addons: []commerce.SKU{{ID: 20, SKU: "wooden-stand", Active: true}},
	}, nil)
	if !data.StandActive || data.StandID != "20" {
		t.Fatalf("legacy stand was not recognized: %+v", data)
	}
}

func TestSignageImagesMatchExactConfiguration(t *testing.T) {
	product := commerce.Product{ID: 1, SKUs: []commerce.SKU{{ID: 11, Name: "4x4 Slate", Active: true}}, Addons: []commerce.SKU{{ID: 20, SKU: "SLATE-STAND", Active: true}}}
	images := []commerce.ProductImage{
		{ProductID: 1, Filename: "default.jpg"},
		{ProductID: 1, SKUID: 11, AddonSKUID: 21, Filename: "other-addon.jpg"},
		{ProductID: 2, SKUID: 11, Filename: "other-product.jpg"},
		{ProductID: 1, SKUID: 11, AddonSKUID: 20, Filename: "stand.jpg"},
	}
	data := signagePageData(product, images)
	if data.ImageURL != "" || data.Options[0].ImageURL != "" || data.Options[0].StandImageURL != "/media/products/stand.jpg" {
		t.Fatalf("unexpected image fallback: %+v", data)
	}
	images = append(images, commerce.ProductImage{ProductID: 1, SKUID: 11, Filename: "slate.jpg"})
	data = signagePageData(product, images)
	if data.ImageURL != "/media/products/slate.jpg" || !strings.Contains(data.ImageAlt, "4x4 Slate") {
		t.Fatalf("initial image not selected: %+v", data)
	}
}

func TestSignageUnavailableAndAccessiblePreviewFields(t *testing.T) {
	data := signagePageData(commerce.Product{SKUs: []commerce.SKU{{ID: 11, Name: "4x4 Slate", Active: false}}}, nil)
	if data.Available || data.StandActive || data.Options[0].Selected {
		t.Fatalf("inactive options must not be selected: %+v", data)
	}
	var output bytes.Buffer
	if err := templates.VendingSignagePage(data).Render(context.Background(), &output); err != nil {
		t.Fatal(err)
	}
	html := output.String()
	for _, attribute := range []string{`maxlength="20"`, `maxlength="200"`, `type="url"`, `for="signage-title"`, `for="signage-destination"`, `data-signage-empty`, `disabled`, `href="/#contact"`} {
		if !strings.Contains(html, attribute) {
			t.Errorf("missing field/control attribute %q", attribute)
		}
	}
	for _, forbidden := range []string{`qr-grid`, `qr-mock`, `action="/cart/items"`, `action="/checkout"`, `name="frame"`, `name="magnets"`} {
		if strings.Contains(html, forbidden) {
			t.Errorf("unexpected obsolete or purchasing UI %q", forbidden)
		}
	}
}

func TestVendingSignagePageUsesExistingMasterSKU(t *testing.T) {
	users := testStore(t)
	catalog, err := commerce.New(users.DB())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.CreateProduct(t.Context(), commerce.ProductParams{
		Slug: "slate-signage", MasterSKU: "SLATE-SIGNAGE", Name: "Slate Vendor Signage",
		Type: commerce.ProductTypeOneTime, Active: true,
	}); err != nil {
		t.Fatal(err)
	}
	handler := NewHandlerWithCommerce(config.Config{ServicesEnabled: true}, &fakeSender{}, fakeVerifier{}, users, catalog, nil, nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/shop/vending-signage", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `data-signage`) {
		t.Fatalf("response does not render signage page: %s", response.Body.String())
	}
}
