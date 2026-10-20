package web

import (
	"bytes"
	"image"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/bkroeze/oregon-dev-foundry/internal/auth"
	"github.com/bkroeze/oregon-dev-foundry/internal/commerce"
	"github.com/bkroeze/oregon-dev-foundry/internal/config"
	"github.com/bkroeze/oregon-dev-foundry/internal/templates"
)

func TestAdminCatalogStatusFormSelectsCurrentState(t *testing.T) {
	options := regexp.MustCompile(`<option\b([^>]*)>`)
	selected := regexp.MustCompile(`(?:^|\s)selected(?:\s|=|$)`)
	value := regexp.MustCompile(`\bvalue="([^"]*)"`)
	for _, active := range []bool{true, false} {
		t.Run(strconv.FormatBool(active), func(t *testing.T) {
			var rendered bytes.Buffer
			if err := templates.CatalogStatusForm("/status", active, "csrf", "Availability").Render(t.Context(), &rendered); err != nil {
				t.Fatal(err)
			}
			matches := options.FindAllStringSubmatch(rendered.String(), -1)
			if len(matches) != 2 {
				t.Fatalf("expected two status options, got %d", len(matches))
			}
			selectedCount := 0
			for _, option := range matches {
				if !selected.MatchString(option[1]) {
					continue
				}
				selectedCount++
				optionValue := value.FindStringSubmatch(option[1])
				if len(optionValue) != 2 || optionValue[1] != strconv.FormatBool(active) {
					t.Fatalf("selected option does not match active=%t: %s", active, option[0])
				}
			}
			if selectedCount != 1 {
				t.Fatalf("expected exactly one selected option, got %d: %s", selectedCount, rendered.String())
			}
		})
	}
}

func catalogTestHandler(t *testing.T) (http.Handler, *commerce.Store, map[string]*http.Cookie, string) {
	t.Helper()
	users := testStore(t)
	store, err := commerce.New(users.DB())
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	handler := NewHandlerWithCommerce(config.Config{MediaDir: dir, PublicBaseURL: "https://shop.example"}, &fakeSender{}, fakeVerifier{}, users, store, nil, nil)
	admin, err := users.CreateUser(t.Context(), auth.CreateUserParams{Username: "images.admin", DisplayName: "Images Admin", Role: auth.RoleAdmin, Password: []byte(testPassword)})
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := users.CreateSession(t.Context(), admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	cookies := map[string]*http.Cookie{sessionCookie: {Name: sessionCookie, Value: token}}
	page := serveWithCookies(handler, http.MethodGet, "/admin/catalog", nil, cookies)
	mergeCookies(cookies, page.Result().Cookies())
	if page.Code != http.StatusOK || cookies[csrfCookie] == nil {
		t.Fatalf("catalog page: %d %s", page.Code, page.Body.String())
	}
	return handler, store, cookies, dir
}

func catalogUpload(t *testing.T, handler http.Handler, path string, cookies map[string]*http.Cookie, csrf, sku, addon string, data []byte) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for key, value := range map[string]string{"csrf_token": csrf, "sku_id": sku, "addon_sku_id": addon} {
		if err := writer.WriteField(key, value); err != nil {
			t.Fatal(err)
		}
	}
	part, err := writer.CreateFormFile("image", "../../untrusted.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, path, &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return serveRequest(handler, req, cookies)
}

func TestAdminCatalogImageImportAndPublicServing(t *testing.T) {
	handler, store, cookies, dir := catalogTestHandler(t)
	product, primary, _, _ := seedOneTimeProduct(t, store)
	base := "/admin/catalog/" + strconv.FormatInt(product.ID, 10)
	var source bytes.Buffer
	if err := png.Encode(&source, image.NewRGBA(image.Rect(0, 0, 1600, 800))); err != nil {
		t.Fatal(err)
	}
	csrf := cookies[csrfCookie].Value
	for _, test := range []struct {
		name, token, sku, addon string
		data                    []byte
		status                  int
	}{
		{"csrf", "invalid", "0", "0", source.Bytes(), http.StatusForbidden},
		{"unsupported", csrf, "0", "0", []byte("not an image"), http.StatusUnprocessableEntity},
		{"addon without primary", csrf, "0", "123", source.Bytes(), http.StatusUnprocessableEntity},
		{"foreign slot", csrf, "999999", "0", source.Bytes(), http.StatusUnprocessableEntity},
		{"oversized body", csrf, "0", "0", bytes.Repeat([]byte("x"), (8<<20)+(128<<10)), http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := catalogUpload(t, handler, base+"/images", cookies, test.token, test.sku, test.addon, test.data)
			if response.Code != test.status {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
	images, err := store.ListProductImages(t.Context(), product.ID)
	if err != nil || len(images) != 0 {
		t.Fatalf("invalid uploads saved images: %#v %v", images, err)
	}
	response := catalogUpload(t, handler, base+"/images", cookies, csrf, strconv.FormatInt(primary.ID, 10), "0", source.Bytes())
	if response.Code != http.StatusSeeOther {
		t.Fatalf("upload: %d %s", response.Code, response.Body.String())
	}
	images, err = store.ListProductImages(t.Context(), product.ID)
	if err != nil || len(images) != 1 || images[0].Width != 1200 || images[0].Height != 600 {
		t.Fatalf("normalized images: %#v %v", images, err)
	}
	imageURL := "/media/products/" + images[0].Filename
	served := serveWithCookies(handler, http.MethodGet, imageURL, nil, nil)
	if served.Code != http.StatusOK || served.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("public image: %d %s", served.Code, served.Body.String())
	}
	decoded, err := png.Decode(bytes.NewReader(served.Body.Bytes()))
	if err != nil || decoded.Bounds().Dx() != 1200 {
		t.Fatalf("served normalized image: %v", err)
	}
	page := serveWithCookies(handler, http.MethodGet, "/shop/"+product.Slug, nil, nil)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), imageURL) {
		t.Fatalf("product missing imported image: %d %s", page.Code, page.Body.String())
	}
	response = serveForm(handler, http.MethodPost, base+"/skus/"+strconv.FormatInt(primary.ID, 10)+"/status", url.Values{"csrf_token": {csrf}, "active": {"false"}}, cookies)
	if response.Code != http.StatusSeeOther {
		t.Fatalf("disable SKU: %d", response.Code)
	}
	page = serveWithCookies(handler, http.MethodGet, "/shop/"+product.Slug, nil, nil)
	if strings.Contains(page.Body.String(), imageURL) {
		t.Fatal("inactive SKU image remains on public product")
	}
	if _, err := os.Stat(filepath.Join(dir, images[0].Filename)); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/media/products/not-an-image.svg", "/media/products/missing.png", "/media/products/"} {
		response := serveWithCookies(handler, http.MethodGet, path, nil, nil)
		if response.Code != http.StatusNotFound {
			t.Fatalf("unsafe/missing image %s returned %d", path, response.Code)
		}
	}
}

func TestAdminCatalogAuthorizationAndStatusPreservation(t *testing.T) {
	handler, store, cookies, _ := catalogTestHandler(t)
	product, primary, _, _ := seedOneTimeProduct(t, store)
	base := "/admin/catalog/" + strconv.FormatInt(product.ID, 10)
	anonymous := serveWithCookies(handler, http.MethodGet, "/admin/catalog", nil, nil)
	if anonymous.Code != http.StatusSeeOther {
		t.Fatalf("anonymous access: %d", anonymous.Code)
	}
	missingCSRF := serveForm(handler, http.MethodPost, base+"/status", url.Values{"active": {"false"}}, cookies)
	if missingCSRF.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF: %d", missingCSRF.Code)
	}
	for _, path := range []string{base + "/status", base + "/skus/" + strconv.FormatInt(primary.ID, 10) + "/status"} {
		response := serveForm(handler, http.MethodPost, path, url.Values{"csrf_token": {cookies[csrfCookie].Value}, "active": {"false"}}, cookies)
		if response.Code != http.StatusSeeOther {
			t.Fatalf("status toggle: %d %s", response.Code, response.Body.String())
		}
	}
	updated, err := store.ProductByID(t.Context(), product.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Active || updated.SKUs[0].Active || updated.Name != product.Name || updated.Description != product.Description || updated.SKUs[0].PriceCents != primary.PriceCents || updated.SKUs[0].OptionsJSON != primary.OptionsJSON || *updated.SKUs[0].Inventory != *primary.Inventory {
		t.Fatalf("status update changed catalog metadata: %#v", updated)
	}
	page := serveWithCookies(handler, http.MethodGet, "/admin/catalog", nil, cookies)
	if !strings.Contains(page.Body.String(), product.Name) || !strings.Contains(page.Body.String(), primary.Name) {
		t.Fatal("inactive catalog options missing from admin")
	}
}

func TestAdminCatalogRejectsNonAdmin(t *testing.T) {
	handler, users, store, _, _ := commerceHandler(t)
	product, primary, _, _ := seedOneTimeProduct(t, store)
	user, err := users.CreateUser(t.Context(), auth.CreateUserParams{Username: "catalog.reader", DisplayName: "Reader", Role: auth.RoleUser, Password: []byte(testPassword)})
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := users.CreateSession(t.Context(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	cookies := map[string]*http.Cookie{sessionCookie: {Name: sessionCookie, Value: token}}
	base := "/admin/catalog/" + strconv.FormatInt(product.ID, 10)
	for _, test := range []struct{ method, path string }{
		{http.MethodGet, "/admin/catalog"},
		{http.MethodPost, base + "/images"},
		{http.MethodPost, base + "/status"},
		{http.MethodPost, base + "/skus/" + strconv.FormatInt(primary.ID, 10) + "/status"},
	} {
		response := serveWithCookies(handler, test.method, test.path, nil, cookies)
		if response.Code != http.StatusForbidden {
			t.Fatalf("%s %s returned %d", test.method, test.path, response.Code)
		}
	}
}
