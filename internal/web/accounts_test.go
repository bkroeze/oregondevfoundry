package web

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/bkroeze/oregon-dev-foundry/internal/auth"
	"github.com/bkroeze/oregon-dev-foundry/internal/commerce"
)

func TestAdminUserCRUDHistoryAndPasswordProtection(t *testing.T) {
	handler, users, store, _, _ := commerceHandler(t)
	admin, err := users.CreateUser(t.Context(), auth.CreateUserParams{Username: "account.admin", Email: "admin@example.com", DisplayName: "Account Admin", Role: auth.RoleAdmin, Password: []byte(testPassword)})
	if err != nil {
		t.Fatal(err)
	}
	cookies := authenticatedCookies(t, handler, users, admin.ID, "/admin")

	landing := serveWithCookies(handler, http.MethodGet, "/admin", nil, cookies)
	if landing.Code != http.StatusOK || !strings.Contains(landing.Body.String(), `href="/admin/shop"`) || !strings.Contains(landing.Body.String(), `href="/admin/users"`) {
		t.Fatalf("admin landing: %d %s", landing.Code, landing.Body.String())
	}
	shop := serveWithCookies(handler, http.MethodGet, "/admin/shop", nil, cookies)
	if shop.Code != http.StatusOK || !strings.Contains(shop.Body.String(), "Products") {
		t.Fatalf("admin shop alias: %d %s", shop.Code, shop.Body.String())
	}

	created := serveForm(handler, http.MethodPost, "/admin/users", url.Values{
		"csrf_token": {cookies[csrfCookie].Value}, "username": {"managed.user"}, "email": {"managed@example.com"},
		"display_name": {"Managed User"}, "role": {"client"}, "password": {testPassword},
	}, cookies)
	if created.Code != http.StatusSeeOther {
		t.Fatalf("create user: %d %s", created.Code, created.Body.String())
	}
	managed, err := users.UserByUsername(t.Context(), "managed.user")
	if err != nil || managed.Email != "managed@example.com" || managed.Role != auth.RoleClient {
		t.Fatalf("created user = %#v, %v", managed, err)
	}
	if got := created.Header().Get("Location"); !strings.HasPrefix(got, "/admin/users/"+strconv.FormatInt(managed.ID, 10)) {
		t.Fatalf("create location = %q", got)
	}

	product, err := store.CreateProduct(t.Context(), commerce.ProductParams{Slug: "managed-history", MasterSKU: "MANAGED-HISTORY", Name: "Managed Workshop", Type: commerce.ProductTypeOneTime, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	sku, err := store.CreateSKU(t.Context(), commerce.SKUParams{ProductID: product.ID, SKU: "MANAGED-HISTORY-GENERAL", Name: "General admission", Kind: commerce.SKUKindPrimary, PriceCents: 2500, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	cart, err := store.CreateCart(t.Context(), &managed.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.AddCartItem(t.Context(), cart.Token, sku.ID, 1); err != nil {
		t.Fatal(err)
	}
	checkout, err := store.CreateCheckout(t.Context(), commerce.CreateCheckoutParams{CartToken: cart.Token, UserID: &managed.ID, BuyerEmail: managed.Email, BuyerName: managed.DisplayName})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.AttachStripeSession(t.Context(), checkout.ID, "cs_managed_history"); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Fulfill(t.Context(), commerce.FulfillParams{StripeSessionID: "cs_managed_history"}); err != nil {
		t.Fatal(err)
	}

	detailPath := "/admin/users/" + strconv.FormatInt(managed.ID, 10)
	detail := serveWithCookies(handler, http.MethodGet, detailPath, nil, cookies)
	if detail.Code != http.StatusOK || !strings.Contains(detail.Body.String(), "managed@example.com") || !strings.Contains(detail.Body.String(), "Managed Workshop") || !strings.Contains(detail.Body.String(), "$25.00") || !strings.Contains(detail.Body.String(), "Signed up") {
		t.Fatalf("user detail: %d %s", detail.Code, detail.Body.String())
	}

	managed, err = users.UserByID(t.Context(), managed.ID)
	if err != nil {
		t.Fatal(err)
	}
	newPassword := "updated horse battery staple"
	updated := serveForm(handler, http.MethodPost, detailPath, url.Values{
		"csrf_token": {cookies[csrfCookie].Value}, "version": {strconv.FormatInt(managed.Version, 10)}, "username": {managed.Username},
		"email": {"updated@example.com"}, "display_name": {"Updated User"}, "role": {"user"}, "has_purchases": {"true"}, "password": {newPassword},
	}, cookies)
	if updated.Code != http.StatusSeeOther {
		t.Fatalf("update user: %d %s", updated.Code, updated.Body.String())
	}
	if _, err = users.Authenticate(t.Context(), managed.Username, []byte(newPassword)); err != nil {
		t.Fatalf("authenticate updated password: %v", err)
	}
	managed, err = users.UserByID(t.Context(), managed.ID)
	if err != nil || managed.Email != "updated@example.com" || managed.DisplayName != "Updated User" || managed.Role != auth.RoleUser || !managed.HasPurchases {
		t.Fatalf("updated user = %#v, %v", managed, err)
	}

	otherAdmin, err := users.CreateUser(t.Context(), auth.CreateUserParams{Username: "protected.admin", Email: "protected@example.com", DisplayName: "Protected Admin", Role: auth.RoleAdmin, Password: []byte(testPassword)})
	if err != nil {
		t.Fatal(err)
	}
	protectedPath := "/admin/users/" + strconv.FormatInt(otherAdmin.ID, 10)
	protected := serveForm(handler, http.MethodPost, protectedPath, url.Values{
		"csrf_token": {cookies[csrfCookie].Value}, "version": {strconv.FormatInt(otherAdmin.Version, 10)}, "username": {otherAdmin.Username},
		"email": {otherAdmin.Email}, "display_name": {otherAdmin.DisplayName}, "role": {"admin"}, "password": {"malicious password replacement"},
	}, cookies)
	if protected.Code != http.StatusUnprocessableEntity || !strings.Contains(protected.Body.String(), "Administrator passwords") {
		t.Fatalf("protected admin password: %d %s", protected.Code, protected.Body.String())
	}
	if _, err = users.Authenticate(t.Context(), otherAdmin.Username, []byte(testPassword)); err != nil {
		t.Fatalf("admin password changed through HTTP: %v", err)
	}

	deleted := serveForm(handler, http.MethodPost, detailPath+"/delete", url.Values{"csrf_token": {cookies[csrfCookie].Value}}, cookies)
	if deleted.Code != http.StatusSeeOther || deleted.Header().Get("Location") != "/admin/users?status=User+deleted" {
		t.Fatalf("delete user: %d location=%q body=%s", deleted.Code, deleted.Header().Get("Location"), deleted.Body.String())
	}
	if _, err = users.UserByID(t.Context(), managed.ID); !errors.Is(err, auth.ErrNotFound) {
		t.Fatalf("deleted user lookup = %v", err)
	}
	preservedOrder, err := store.OrderByStripeSession(t.Context(), "cs_managed_history")
	if err != nil || preservedOrder.UserID != nil || preservedOrder.BuyerEmail != "managed@example.com" {
		t.Fatalf("preserved deleted-user order = %#v, %v", preservedOrder, err)
	}
}

func TestPasswordChangeWorkflowRevokesSessionsAndBlocksAdmins(t *testing.T) {
	handler, users, _, _, _ := commerceHandler(t)
	user, err := users.CreateUser(t.Context(), auth.CreateUserParams{Username: "password.user", Email: "password@example.com", DisplayName: "Password User", Role: auth.RoleUser, Password: []byte(testPassword)})
	if err != nil {
		t.Fatal(err)
	}
	cookies := authenticatedCookies(t, handler, users, user.ID, "/profile")
	oldSession := cookies[sessionCookie].Value
	profile := serveWithCookies(handler, http.MethodGet, "/profile", nil, cookies)
	if profile.Code != http.StatusOK || !strings.Contains(profile.Body.String(), `href="/profile/password"`) || !strings.Contains(profile.Body.String(), `href="/profile/delete"`) {
		t.Fatalf("profile actions: %d %s", profile.Code, profile.Body.String())
	}

	wrong := serveForm(handler, http.MethodPost, "/profile/password", url.Values{
		"csrf_token": {cookies[csrfCookie].Value}, "current_password": {"incorrect password"},
		"new_password": {"new correct horse battery"}, "confirm_password": {"new correct horse battery"},
	}, cookies)
	if wrong.Code != http.StatusUnprocessableEntity || !strings.Contains(wrong.Body.String(), "current password is incorrect") {
		t.Fatalf("wrong current password: %d %s", wrong.Code, wrong.Body.String())
	}

	newPassword := "new correct horse battery"
	changed := serveForm(handler, http.MethodPost, "/profile/password", url.Values{
		"csrf_token": {cookies[csrfCookie].Value}, "current_password": {testPassword},
		"new_password": {newPassword}, "confirm_password": {newPassword},
	}, cookies)
	if changed.Code != http.StatusSeeOther || !strings.HasPrefix(changed.Header().Get("Location"), "/login?status=") {
		t.Fatalf("password change: %d location=%q body=%s", changed.Code, changed.Header().Get("Location"), changed.Body.String())
	}
	if _, err = users.UserBySession(t.Context(), oldSession); !errors.Is(err, auth.ErrNotFound) {
		t.Fatalf("old session lookup = %v", err)
	}
	if _, err = users.Authenticate(t.Context(), user.Username, []byte(testPassword)); !errors.Is(err, auth.ErrInvalidCredential) {
		t.Fatalf("old password authentication = %v", err)
	}
	if _, err = users.Authenticate(t.Context(), user.Username, []byte(newPassword)); err != nil {
		t.Fatalf("new password authentication = %v", err)
	}
	statusPage := httptest.NewRecorder()
	handler.ServeHTTP(statusPage, httptest.NewRequest(http.MethodGet, changed.Header().Get("Location"), nil))
	if statusPage.Code != http.StatusOK || !strings.Contains(statusPage.Body.String(), "Password changed. Please log in again.") {
		t.Fatalf("password status: %d %s", statusPage.Code, statusPage.Body.String())
	}

	admin, err := users.CreateUser(t.Context(), auth.CreateUserParams{Username: "password.admin", DisplayName: "Password Admin", Role: auth.RoleAdmin, Password: []byte(testPassword)})
	if err != nil {
		t.Fatal(err)
	}
	adminCookies := authenticatedCookies(t, handler, users, admin.ID, "/profile/password")
	adminPage := serveWithCookies(handler, http.MethodGet, "/profile/password", nil, adminCookies)
	if adminPage.Code != http.StatusForbidden || strings.Contains(adminPage.Body.String(), `name="new_password"`) || !strings.Contains(adminPage.Body.String(), "server CLI") {
		t.Fatalf("admin password page: %d %s", adminPage.Code, adminPage.Body.String())
	}
}

func TestDeleteAccountCancelsStripeResourcesAndPreservesRecords(t *testing.T) {
	handler, users, store, gateway, _ := commerceHandler(t)
	user, err := users.CreateUser(t.Context(), auth.CreateUserParams{Username: "delete.self", Email: "delete@example.com", DisplayName: "Delete Self", Role: auth.RoleUser, Password: []byte(testPassword)})
	if err != nil {
		t.Fatal(err)
	}
	inventory := int64(2)
	product, err := store.CreateProduct(t.Context(), commerce.ProductParams{Slug: "delete-pending", MasterSKU: "DELETE-PENDING", Name: "Pending order", Type: commerce.ProductTypeOneTime, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	sku, err := store.CreateSKU(t.Context(), commerce.SKUParams{ProductID: product.ID, SKU: "DELETE-PENDING-SKU", Name: "Pending", Kind: commerce.SKUKindPrimary, PriceCents: 1800, Inventory: &inventory, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	cart, err := store.CreateCart(t.Context(), &user.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.AddCartItem(t.Context(), cart.Token, sku.ID, 1); err != nil {
		t.Fatal(err)
	}
	pending, err := store.CreateCheckout(t.Context(), commerce.CreateCheckoutParams{CartToken: cart.Token, UserID: &user.ID, BuyerEmail: user.Email, BuyerName: user.DisplayName})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.AttachStripeSession(t.Context(), pending.ID, "cs_delete_pending"); err != nil {
		t.Fatal(err)
	}

	subProduct, err := store.CreateProduct(t.Context(), commerce.ProductParams{Slug: "delete-membership", MasterSKU: "DELETE-MEMBERSHIP", Name: "Delete Membership", Type: commerce.ProductTypeSubscription, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	subSKU, err := store.CreateSKU(t.Context(), commerce.SKUParams{ProductID: subProduct.ID, SKU: "DELETE-MEMBERSHIP-MONTHLY", Name: "Monthly", Kind: commerce.SKUKindPrimary, PriceCents: 1200, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	subCheckout, err := store.CreateCheckout(t.Context(), commerce.CreateCheckoutParams{StandaloneSKUID: subSKU.ID, UserID: &user.ID, BuyerEmail: user.Email, BuyerName: user.DisplayName})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.AttachStripeSession(t.Context(), subCheckout.ID, "cs_delete_subscription"); err != nil {
		t.Fatal(err)
	}
	fulfilled, err := store.Fulfill(t.Context(), commerce.FulfillParams{StripeSessionID: "cs_delete_subscription", StripeSubscriptionID: "sub_delete_active", SubscriptionStatus: "active"})
	if err != nil || fulfilled.Subscription == nil {
		t.Fatalf("subscription fulfillment = %#v, %v", fulfilled, err)
	}
	subscriptionID := fulfilled.Subscription.ID

	cookies := authenticatedCookies(t, handler, users, user.ID, "/profile/delete")
	page := serveWithCookies(handler, http.MethodGet, "/profile/delete", nil, cookies)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "payment information") || !strings.Contains(page.Body.String(), "legally required") || !strings.Contains(page.Body.String(), "active subscriptions") {
		t.Fatalf("delete account notice: %d %s", page.Code, page.Body.String())
	}
	deleted := serveForm(handler, http.MethodPost, "/profile/delete", url.Values{
		"csrf_token": {cookies[csrfCookie].Value}, "current_password": {testPassword}, "confirm_delete": {"true"},
	}, cookies)
	if deleted.Code != http.StatusSeeOther || deleted.Header().Get("Location") != "/" {
		t.Fatalf("delete account: %d location=%q body=%s", deleted.Code, deleted.Header().Get("Location"), deleted.Body.String())
	}
	if len(gateway.expired) != 1 || gateway.expired[0] != "cs_delete_pending" || len(gateway.canceled) != 1 || gateway.canceled[0] != "sub_delete_active" {
		t.Fatalf("Stripe cancellations: sessions=%#v subscriptions=%#v", gateway.expired, gateway.canceled)
	}
	if _, err = users.UserByID(t.Context(), user.ID); !errors.Is(err, auth.ErrNotFound) {
		t.Fatalf("deleted account lookup = %v", err)
	}
	if _, err = store.CheckoutByID(t.Context(), pending.ID); !errors.Is(err, commerce.ErrNotFound) {
		t.Fatalf("pending checkout lookup = %v", err)
	}
	restored, err := store.SKUByID(t.Context(), sku.ID)
	if err != nil || restored.Inventory == nil || *restored.Inventory != 2 {
		t.Fatalf("restored pending inventory = %#v, %v", restored.Inventory, err)
	}
	preserved, err := store.SubscriptionByID(t.Context(), subscriptionID)
	if err != nil || preserved.UserID != nil || preserved.Status != "canceled" || preserved.StripeSubscriptionID != "sub_delete_active" || preserved.BuyerEmail != "delete@example.com" {
		t.Fatalf("preserved subscription = %#v, %v", preserved, err)
	}
}

func TestAdminUserManagementRejectsNonAdmins(t *testing.T) {
	handler, users, _, _, _ := commerceHandler(t)
	user, err := users.CreateUser(t.Context(), auth.CreateUserParams{Username: "ordinary.user", DisplayName: "Ordinary User", Role: auth.RoleUser, Password: []byte(testPassword)})
	if err != nil {
		t.Fatal(err)
	}
	cookies := authenticatedCookies(t, handler, users, user.ID, "/admin/users")
	response := serveWithCookies(handler, http.MethodGet, "/admin/users", nil, cookies)
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "Access denied") {
		t.Fatalf("non-admin users page: %d %s", response.Code, response.Body.String())
	}
}

func authenticatedCookies(t *testing.T, handler http.Handler, users *auth.Store, userID int64, path string) map[string]*http.Cookie {
	t.Helper()
	token, _, err := users.CreateSession(t.Context(), userID)
	if err != nil {
		t.Fatal(err)
	}
	cookies := map[string]*http.Cookie{sessionCookie: {Name: sessionCookie, Value: token}}
	response := serveWithCookies(handler, http.MethodGet, path, nil, cookies)
	mergeCookies(cookies, response.Result().Cookies())
	if cookies[csrfCookie] == nil {
		t.Fatalf("missing CSRF cookie for authenticated %s", path)
	}
	return cookies
}
