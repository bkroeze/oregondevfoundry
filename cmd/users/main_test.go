package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/bkroeze/oregon-dev-foundry/internal/auth"
	"github.com/bkroeze/oregon-dev-foundry/internal/commerce"
)

func TestUserCRUDCommand(t *testing.T) {
	database := t.TempDir() + "/users.db"
	password := "correct horse battery staple"
	var output bytes.Buffer

	code := run([]string{"create", "--database", database, "--username", "client.one", "--email", "Client.One@Example.COM", "--display-name", "Client One", "--role", "client", "--password-stdin"}, strings.NewReader(password+"\n"), &output)
	if code != 0 || !strings.Contains(output.String(), "status: \"Client\"") || !strings.Contains(output.String(), "email: \"client.one@example.com\"") {
		t.Fatalf("create code=%d output=%s", code, output.String())
	}

	output.Reset()
	code = run([]string{"list", "--database", database}, strings.NewReader(""), &output)
	if code != 0 || !strings.Contains(output.String(), "count: 1") || !strings.Contains(output.String(), "client.one") {
		t.Fatalf("list code=%d output=%s", code, output.String())
	}

	output.Reset()
	code = run([]string{"show", "--database", database, "--username", "client.one"}, strings.NewReader(""), &output)
	if code != 0 || !strings.Contains(output.String(), "displayName: \"Client One\"") || !strings.Contains(output.String(), "email: \"client.one@example.com\"") {
		t.Fatalf("show code=%d output=%s", code, output.String())
	}

	output.Reset()
	code = run([]string{"update", "--database", database, "--username", "client.one", "--email", "customer.one@example.com", "--display-name", "Customer One", "--role", "user", "--has-purchases=true"}, strings.NewReader(""), &output)
	if code != 0 || !strings.Contains(output.String(), "status: \"Customer\"") || !strings.Contains(output.String(), "email: \"customer.one@example.com\"") {
		t.Fatalf("update code=%d output=%s", code, output.String())
	}

	output.Reset()
	code = run([]string{"delete", "--database", database, "--username", "client.one", "--confirm", "client.one"}, strings.NewReader(""), &output)
	if code != 0 || !strings.Contains(output.String(), "deleted:") {
		t.Fatalf("delete code=%d output=%s", code, output.String())
	}

	output.Reset()
	code = run([]string{"list", "--database", database}, strings.NewReader(""), &output)
	if code != 0 || !strings.Contains(output.String(), "users: 0 users found") {
		t.Fatalf("empty list code=%d output=%s", code, output.String())
	}
}

func TestDeleteCommandRefusesActiveStripeRecords(t *testing.T) {
	database := t.TempDir() + "/users.db"
	var output bytes.Buffer
	if code := run([]string{"create", "--database", database, "--username", "active.user", "--display-name", "Active User", "--password-stdin"}, strings.NewReader("correct horse battery staple\n"), &output); code != 0 {
		t.Fatalf("create code=%d output=%s", code, output.String())
	}
	authStore, err := auth.Open(database)
	if err != nil {
		t.Fatal(err)
	}
	user, err := authStore.UserByUsername(context.Background(), "active.user")
	if err != nil {
		t.Fatal(err)
	}
	store, err := commerce.New(authStore.DB())
	if err != nil {
		t.Fatal(err)
	}
	product, err := store.CreateProduct(context.Background(), commerce.ProductParams{Slug: "active-order", MasterSKU: "ACTIVE-ORDER", Name: "Active order", Type: commerce.ProductTypeOneTime, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	sku, err := store.CreateSKU(context.Background(), commerce.SKUParams{ProductID: product.ID, SKU: "ACTIVE-ORDER-SKU", Name: "General", Kind: commerce.SKUKindPrimary, PriceCents: 1000, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	cart, err := store.CreateCart(context.Background(), &user.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.AddCartItem(context.Background(), cart.Token, sku.ID, 1); err != nil {
		t.Fatal(err)
	}
	checkout, err := store.CreateCheckout(context.Background(), commerce.CreateCheckoutParams{CartToken: cart.Token, UserID: &user.ID, BuyerEmail: "active@example.com", BuyerName: "Active User"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.AttachStripeSession(context.Background(), checkout.ID, "cs_active_cli"); err != nil {
		t.Fatal(err)
	}
	if err = authStore.Close(); err != nil {
		t.Fatal(err)
	}

	output.Reset()
	code := run([]string{"delete", "--database", database, "--username", user.Username, "--confirm", user.Username}, strings.NewReader(""), &output)
	if code != 1 || !strings.Contains(output.String(), "active Stripe checkout or subscription") {
		t.Fatalf("delete code=%d output=%s", code, output.String())
	}
	authStore, err = auth.Open(database)
	if err != nil {
		t.Fatal(err)
	}
	defer authStore.Close()
	if _, err = authStore.UserByID(context.Background(), user.ID); err != nil {
		t.Fatalf("blocked deletion removed user: %v", err)
	}
}

func TestUserCommandRequiresPasswordFromStandardInput(t *testing.T) {
	var output bytes.Buffer
	code := run([]string{"create", "--database", t.TempDir() + "/users.db", "--username", "safe.user", "--display-name", "Safe User", "--password", "secret-on-command-line"}, strings.NewReader(""), &output)
	if code != 2 || !strings.Contains(output.String(), "flag provided but not defined") {
		t.Fatalf("code=%d output=%s", code, output.String())
	}
}

func TestSubcommandHelpSucceeds(t *testing.T) {
	var output bytes.Buffer
	if code := run([]string{"create", "--help"}, strings.NewReader(""), &output); code != 0 || !strings.Contains(output.String(), "--password-stdin") {
		t.Fatalf("code=%d output=%s", code, output.String())
	}
}
