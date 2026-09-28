package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bkroeze/oregon-dev-foundry/internal/auth"
	"github.com/bkroeze/oregon-dev-foundry/internal/commerce"
)

func TestFixtureCommandAppliesDefaultShapeIdempotently(t *testing.T) {
	database := filepath.Join(t.TempDir(), "fixture.db")
	fixture := filepath.Join("..", "..", "fixtures", "test-product.json")
	var output bytes.Buffer

	if code := run([]string{"fixture", "--database", database, fixture}, strings.NewReader(""), &output); code != 0 {
		t.Fatalf("first apply code=%d output=%s", code, output.String())
	}
	output.Reset()
	if code := run([]string{"fixture", "--database", database, fixture}, strings.NewReader(""), &output); code != 0 {
		t.Fatalf("second apply code=%d output=%s", code, output.String())
	}

	users, err := auth.Open(database)
	if err != nil {
		t.Fatal(err)
	}
	defer users.Close()
	store, err := commerce.New(users.DB())
	if err != nil {
		t.Fatal(err)
	}
	product, err := store.ProductBySlug(t.Context(), "test-product")
	if err != nil {
		t.Fatal(err)
	}
	if len(product.SKUs) != 2 || len(product.ShippingOptions) != 1 || product.ShippingOptions[0].AmountCents != 0 {
		t.Fatalf("fixture product = %#v", product)
	}
	if product.SKUs[0].PriceCents != 1 || product.SKUs[1].PriceCents != 1 {
		t.Fatalf("fixture SKU prices = %#v", product.SKUs)
	}
}

func TestFixtureCommandReadsStandardInput(t *testing.T) {
	database := filepath.Join(t.TempDir(), "fixture.db")
	input := strings.NewReader(`{"slug":"stdin-product","master_sku":"STDIN-PRODUCT","name":"Stdin Product","type":"one_time","active":true}`)
	var output bytes.Buffer
	if code := run([]string{"fixture", "--database", database, "-"}, input, &output); code != 0 {
		t.Fatalf("apply code=%d output=%s", code, output.String())
	}
	if !strings.Contains(output.String(), `applied product "stdin-product"`) {
		t.Fatalf("output=%s", output.String())
	}
	if _, err := os.Stat(database); err != nil {
		t.Fatal(err)
	}
}
