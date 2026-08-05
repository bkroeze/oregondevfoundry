package web

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/bkroeze/oregon-dev-foundry/internal/auth"
	"github.com/bkroeze/oregon-dev-foundry/internal/commerce"
	"github.com/bkroeze/oregon-dev-foundry/internal/config"
	"github.com/bkroeze/oregon-dev-foundry/internal/notifications"
	"github.com/bkroeze/oregon-dev-foundry/internal/payment"
)

type fakePaymentGateway struct {
	created    []payment.CreateSessionParams
	sessions   map[string]payment.Session
	event      payment.Event
	createErr  error
	createHook func()
	expired    []string
	canceled   []string
	expireErr  error
	cancelErr  error
}

func (f *fakePaymentGateway) CreateSession(_ context.Context, params payment.CreateSessionParams) (payment.Session, error) {
	if f.createHook != nil {
		f.createHook()
	}
	if f.createErr != nil {
		return payment.Session{}, f.createErr
	}
	f.created = append(f.created, params)
	id := "cs_test_" + string(rune('0'+len(f.created)))
	session := payment.Session{ID: id, ClientSecret: "secret_" + id, Mode: params.Mode, ClientReference: params.ClientReference, Metadata: params.Metadata}
	if f.sessions == nil {
		f.sessions = make(map[string]payment.Session)
	}
	f.sessions[id] = session
	return session, nil
}
func (f *fakePaymentGateway) GetSession(_ context.Context, id string) (payment.Session, error) {
	return f.sessions[id], nil
}
func (f *fakePaymentGateway) ExpireSession(_ context.Context, id string) error {
	if f.expireErr != nil {
		return f.expireErr
	}
	f.expired = append(f.expired, id)
	return nil
}
func (f *fakePaymentGateway) CancelSubscription(_ context.Context, id string) error {
	if f.cancelErr != nil {
		return f.cancelErr
	}
	f.canceled = append(f.canceled, id)
	return nil
}
func (f *fakePaymentGateway) ParseWebhook(_ []byte, _ string) (payment.Event, error) {
	return f.event, nil
}

type fakeOrderMailer struct {
	orders      []notifications.OrderStatus
	invitations []notifications.Invitation
}

func (f *fakeOrderMailer) SendOrderStatus(_ context.Context, message notifications.OrderStatus) error {
	f.orders = append(f.orders, message)
	return nil
}
func (f *fakeOrderMailer) SendInvitation(_ context.Context, message notifications.Invitation) error {
	f.invitations = append(f.invitations, message)
	return nil
}

func commerceHandler(t *testing.T) (http.Handler, *auth.Store, *commerce.Store, *fakePaymentGateway, *fakeOrderMailer) {
	t.Helper()
	users := testStore(t)
	store, err := commerce.New(users.DB())
	if err != nil {
		t.Fatal(err)
	}
	gateway := &fakePaymentGateway{sessions: make(map[string]payment.Session)}
	mailer := &fakeOrderMailer{}
	handler := NewHandlerWithCommerce(config.Config{PublicBaseURL: "https://shop.example", StripePublishableKey: "pk_test", SessionCookieSecure: false}, &fakeSender{}, fakeVerifier{}, users, store, gateway, mailer)
	return handler, users, store, gateway, mailer
}

func seedOneTimeProduct(t *testing.T, store *commerce.Store) (commerce.Product, commerce.SKU, commerce.SKU, commerce.ShippingOption) {
	t.Helper()
	product, err := store.CreateProduct(t.Context(), commerce.ProductParams{Slug: "workshop", MasterSKU: "WORKSHOP", Name: "Workshop", Description: "Choose a workshop style.", Type: commerce.ProductTypeOneTime, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	stock := int64(3)
	primary, err := store.CreateSKU(t.Context(), commerce.SKUParams{ProductID: product.ID, SKU: "WORKSHOP-WOOD", Name: "Wood", Kind: commerce.SKUKindPrimary, OptionsJSON: `{"style":"wood"}`, PriceCents: 3500, Inventory: &stock, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	addon, err := store.CreateAddon(t.Context(), commerce.SKUParams{ProductID: product.ID, SKU: "WORKSHOP-FRAME", Name: "Frame", OptionsJSON: `{}`, PriceCents: 700, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	shipping, err := store.CreateShippingOption(t.Context(), commerce.ShippingOptionParams{ProductID: product.ID, Name: "Fixed shipping", AmountCents: 500, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	return product, primary, addon, shipping
}

func TestGuestCartWebhookFulfillmentReferralAndInvitation(t *testing.T) {
	handler, users, store, gateway, mailer := commerceHandler(t)
	product, primary, addon, shipping := seedOneTimeProduct(t, store)
	cookies := make(map[string]*http.Cookie)

	page := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/shop/workshop", nil)
	request.Header.Set("Referer", "https://partner.example/campaign?id=42")
	handler.ServeHTTP(page, request)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "WORKSHOP-WOOD") {
		t.Fatalf("product page: %d %s", page.Code, page.Body.String())
	}
	mergeCookies(cookies, page.Result().Cookies())
	if cookies[referralCookie] == nil || cookies[csrfCookie] == nil {
		t.Fatalf("missing attribution/CSRF cookies: %#v", cookies)
	}

	form := url.Values{"csrf_token": {cookies[csrfCookie].Value}, "sku": {product.MasterSKU}, "addon_sku": {addon.SKU}, "shipping_code": {shippingID(shipping)}, "quantity": {"2"}}
	added := serveForm(handler, http.MethodPost, "/cart/items", form, cookies)
	if added.Code != http.StatusSeeOther {
		t.Fatalf("add cart: %d %s", added.Code, added.Body.String())
	}
	mergeCookies(cookies, added.Result().Cookies())

	cartPage := serveWithCookies(handler, http.MethodGet, "/cart", nil, cookies)
	if cartPage.Code != http.StatusOK || !strings.Contains(cartPage.Body.String(), "$89.00") || !strings.Contains(cartPage.Body.String(), "Frame") {
		t.Fatalf("cart: %d %s", cartPage.Code, cartPage.Body.String())
	}

	checkoutForm := url.Values{"csrf_token": {cookies[csrfCookie].Value}, "name": {"Guest Buyer"}, "email": {"buyer@example.com"}, "create_account": {"true"}}
	embedded := serveForm(handler, http.MethodPost, "/checkout", checkoutForm, cookies)
	if embedded.Code != http.StatusOK || !strings.Contains(embedded.Body.String(), "secret_cs_test_1") {
		t.Fatalf("embedded checkout: %d %s", embedded.Code, embedded.Body.String())
	}
	if len(gateway.created) != 1 || gateway.created[0].Mode != payment.ModePayment || len(gateway.created[0].Lines) != 3 {
		t.Fatalf("Stripe request: %#v", gateway.created)
	}

	gateway.sessions["cs_test_1"] = payment.Session{ID: "cs_test_1", Mode: payment.ModePayment, PaymentStatus: "paid", Status: "complete", PaymentIntentID: "pi_test", CustomerEmail: "buyer@example.com", Metadata: gateway.created[0].Metadata}
	gateway.event = payment.Event{ID: "evt_test_1", Type: "checkout.session.completed", Session: gateway.sessions["cs_test_1"]}
	webhook := httptest.NewRecorder()
	handler.ServeHTTP(webhook, httptest.NewRequest(http.MethodPost, "/stripe/webhook", strings.NewReader("signed")))
	if webhook.Code != http.StatusOK {
		t.Fatalf("webhook: %d %s", webhook.Code, webhook.Body.String())
	}

	order, err := store.OrderByStripeSession(t.Context(), "cs_test_1")
	if err != nil {
		t.Fatal(err)
	}
	if order.TotalCents != 8900 || order.ReferralID == nil || len(order.Items) != 2 {
		t.Fatalf("order: %#v", order)
	}
	remaining, err := store.SKUByID(t.Context(), primary.ID)
	if err != nil || remaining.Inventory == nil || *remaining.Inventory != 1 {
		t.Fatalf("inventory: %#v, %v", remaining, err)
	}
	if len(mailer.orders) != 1 || len(mailer.invitations) != 1 || !strings.Contains(mailer.invitations[0].ActivationURL, "/account/invite?token=") {
		t.Fatalf("mail: %#v %#v", mailer.orders, mailer.invitations)
	}

	repeat := httptest.NewRecorder()
	handler.ServeHTTP(repeat, httptest.NewRequest(http.MethodPost, "/stripe/webhook", strings.NewReader("signed")))
	if repeat.Code != http.StatusOK || len(mailer.orders) != 1 || len(mailer.invitations) != 1 {
		t.Fatalf("idempotent webhook: %d %#v %#v", repeat.Code, mailer.orders, mailer.invitations)
	}

	confirmation := serveWithCookies(handler, http.MethodGet, "/checkout/return?session_id=cs_test_1", nil, cookies)
	if confirmation.Code != http.StatusOK || !strings.Contains(confirmation.Body.String(), order.BuyerEmail) {
		t.Fatalf("confirmation: %d %s", confirmation.Code, confirmation.Body.String())
	}

	referral, err := store.ReferralByToken(t.Context(), cookies[referralCookie].Value)
	if err != nil || referral.Host != "partner.example" {
		t.Fatalf("referral: %#v %v", referral, err)
	}
	invitation, _, err := storeInvitationUser(t, store, mailer.invitations[0].ActivationURL)
	if err != nil || invitation.Email != "buyer@example.com" {
		t.Fatalf("invitation referral user: %#v %v", invitation, err)
	}
	associated, err := store.ReferralForUser(t.Context(), invitation.UserID)
	if err != nil || associated.ID != *order.ReferralID || associated.ID != referral.ID || product.ID == 0 {
		t.Fatalf("user referral: %#v %v", associated, err)
	}
	activationURL, err := url.Parse(mailer.invitations[0].ActivationURL)
	if err != nil {
		t.Fatal(err)
	}
	invitePage := serveWithCookies(handler, http.MethodGet, activationURL.RequestURI(), nil, cookies)
	if invitePage.Code != http.StatusOK || !strings.Contains(invitePage.Body.String(), "Activate your account") {
		t.Fatalf("invitation page: %d %s", invitePage.Code, invitePage.Body.String())
	}
	activation := serveForm(handler, http.MethodPost, "/account/invite", url.Values{
		"csrf_token": {cookies[csrfCookie].Value}, "invitation_token": {activationURL.Query().Get("token")},
		"name": {"Guest Buyer"}, "username": {"guest.buyer"}, "password": {testPassword}, "password_confirmation": {testPassword},
	}, cookies)
	if activation.Code != http.StatusSeeOther {
		t.Fatalf("activate account: %d %s", activation.Code, activation.Body.String())
	}
	activated, err := users.Authenticate(t.Context(), "guest.buyer", []byte(testPassword))
	if err != nil || activated.Email != "buyer@example.com" {
		t.Fatalf("authenticate invited user: %#v, %v", activated, err)
	}
}

func TestMonthlySubscriptionCheckoutStaysOutsideCart(t *testing.T) {
	handler, _, store, gateway, mailer := commerceHandler(t)
	product, err := store.CreateProduct(t.Context(), commerce.ProductParams{Slug: "membership", MasterSKU: "MEMBERSHIP", Name: "Membership", Type: commerce.ProductTypeSubscription, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	sku, err := store.CreateSKU(t.Context(), commerce.SKUParams{ProductID: product.ID, SKU: "MEMBERSHIP-MONTHLY", Name: "Monthly", Kind: commerce.SKUKindPrimary, PriceCents: 1200, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	cookies := csrfCookies(t, handler, "/shop/membership")
	form := url.Values{"csrf_token": {cookies[csrfCookie].Value}, "sku": {sku.SKU}, "name": {"Member"}, "email": {"member@example.com"}}
	response := serveForm(handler, http.MethodPost, "/subscriptions/checkout", form, cookies)
	if response.Code != http.StatusOK {
		t.Fatalf("subscription checkout: %d %s", response.Code, response.Body.String())
	}
	if cookies[cartCookie] != nil || len(gateway.created) != 1 || gateway.created[0].Mode != payment.ModeSubscription || !gateway.created[0].Lines[0].Recurring {
		t.Fatalf("subscription flow: %#v %#v", cookies, gateway.created)
	}
	gateway.sessions["cs_test_1"] = payment.Session{ID: "cs_test_1", Mode: payment.ModeSubscription, PaymentStatus: "paid", Status: "complete", SubscriptionID: "sub_test_1", Metadata: gateway.created[0].Metadata}
	returned := serveWithCookies(handler, http.MethodGet, "/checkout/return?session_id=cs_test_1", nil, cookies)
	if returned.Code != http.StatusOK || len(mailer.orders) != 1 {
		t.Fatalf("subscription return: %d %s mail=%#v", returned.Code, returned.Body.String(), mailer.orders)
	}
	if _, err := store.SubscriptionByStripeID(t.Context(), "sub_test_1"); err != nil {
		t.Fatal(err)
	}
}

func TestCartCheckoutCanRetryAfterStripeSessionFailure(t *testing.T) {
	handler, _, store, gateway, _ := commerceHandler(t)
	_, primary, _, shipping := seedOneTimeProduct(t, store)
	cookies := csrfCookies(t, handler, "/shop/workshop")
	added := serveForm(handler, http.MethodPost, "/cart/items", url.Values{
		"csrf_token": {cookies[csrfCookie].Value}, "sku": {primary.SKU},
		"shipping_code": {shippingID(shipping)}, "quantity": {"1"},
	}, cookies)
	mergeCookies(cookies, added.Result().Cookies())
	checkout := url.Values{"csrf_token": {cookies[csrfCookie].Value}, "name": {"Retry Buyer"}, "email": {"retry@example.com"}}

	failedRequest := httptest.NewRequest(http.MethodPost, "/checkout", strings.NewReader(checkout.Encode()))
	failedRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, cookie := range cookies {
		failedRequest.AddCookie(cookie)
	}
	requestCtx, cancelRequest := context.WithCancel(failedRequest.Context())
	failedRequest = failedRequest.WithContext(requestCtx)
	gateway.createHook = cancelRequest
	gateway.createErr = context.Canceled
	failed := httptest.NewRecorder()
	handler.ServeHTTP(failed, failedRequest)
	if failed.Code != http.StatusInternalServerError {
		t.Fatalf("failed checkout status = %d", failed.Code)
	}
	released, err := store.SKUByID(t.Context(), primary.ID)
	if err != nil || released.Inventory == nil || *released.Inventory != 3 {
		t.Fatalf("cancelled request inventory = %#v, %v", released.Inventory, err)
	}

	gateway.createHook = nil
	gateway.createErr = nil
	retried := serveForm(handler, http.MethodPost, "/checkout", checkout, cookies)
	if retried.Code != http.StatusOK || len(gateway.created) != 1 {
		t.Fatalf("retried checkout: %d %s sessions=%d", retried.Code, retried.Body.String(), len(gateway.created))
	}
	resumed := serveForm(handler, http.MethodPost, "/checkout", checkout, cookies)
	if resumed.Code != http.StatusOK || len(gateway.created) != 1 || !strings.Contains(resumed.Body.String(), "secret_cs_test_1") {
		t.Fatalf("resumed checkout: %d sessions=%d", resumed.Code, len(gateway.created))
	}

	expiredSession := gateway.sessions["cs_test_1"]
	expiredSession.Status = "expired"
	gateway.sessions["cs_test_1"] = expiredSession
	restarted := serveForm(handler, http.MethodPost, "/checkout", checkout, cookies)
	if restarted.Code != http.StatusOK || len(gateway.created) != 2 || !strings.Contains(restarted.Body.String(), "secret_cs_test_2") {
		t.Fatalf("restarted checkout: %d sessions=%d", restarted.Code, len(gateway.created))
	}
	gateway.event = payment.Event{ID: "evt_expired", Type: "checkout.session.expired", Session: payment.Session{ID: "cs_test_2", Status: "expired"}}
	expiredWebhook := httptest.NewRecorder()
	handler.ServeHTTP(expiredWebhook, httptest.NewRequest(http.MethodPost, "/stripe/webhook", strings.NewReader("signed")))
	if expiredWebhook.Code != http.StatusOK {
		t.Fatalf("expired webhook: %d %s", expiredWebhook.Code, expiredWebhook.Body.String())
	}
	released, err = store.SKUByID(t.Context(), primary.ID)
	if err != nil || released.Inventory == nil || *released.Inventory != 3 {
		t.Fatalf("expired webhook inventory = %#v, %v", released.Inventory, err)
	}
	afterExpiry := serveForm(handler, http.MethodPost, "/checkout", checkout, cookies)
	if afterExpiry.Code != http.StatusOK || len(gateway.created) != 3 {
		t.Fatalf("checkout after expiry: %d sessions=%d", afterExpiry.Code, len(gateway.created))
	}
}

func TestAsyncPaymentFailureReleasesReservedInventory(t *testing.T) {
	handler, _, store, gateway, _ := commerceHandler(t)
	_, primary, _, shipping := seedOneTimeProduct(t, store)
	cookies := csrfCookies(t, handler, "/shop/workshop")
	added := serveForm(handler, http.MethodPost, "/cart/items", url.Values{
		"csrf_token": {cookies[csrfCookie].Value}, "sku": {primary.SKU},
		"shipping_code": {shippingID(shipping)}, "quantity": {"1"},
	}, cookies)
	mergeCookies(cookies, added.Result().Cookies())
	started := serveForm(handler, http.MethodPost, "/checkout", url.Values{
		"csrf_token": {cookies[csrfCookie].Value}, "name": {"Delayed Buyer"}, "email": {"delayed@example.com"},
	}, cookies)
	if started.Code != http.StatusOK || len(gateway.created) != 1 {
		t.Fatalf("start delayed checkout: %d %s", started.Code, started.Body.String())
	}
	reserved, err := store.SKUByID(t.Context(), primary.ID)
	if err != nil || reserved.Inventory == nil || *reserved.Inventory != 2 {
		t.Fatalf("reserved inventory = %#v, %v", reserved.Inventory, err)
	}
	gateway.event = payment.Event{ID: "evt_async_failed", Type: "checkout.session.async_payment_failed", Session: payment.Session{ID: "cs_test_1", Status: "complete", PaymentStatus: "unpaid"}}
	webhook := httptest.NewRecorder()
	handler.ServeHTTP(webhook, httptest.NewRequest(http.MethodPost, "/stripe/webhook", strings.NewReader("signed")))
	if webhook.Code != http.StatusOK {
		t.Fatalf("failed-payment webhook: %d %s", webhook.Code, webhook.Body.String())
	}
	released, err := store.SKUByID(t.Context(), primary.ID)
	if err != nil || released.Inventory == nil || *released.Inventory != 3 {
		t.Fatalf("released failed-payment inventory = %#v, %v", released.Inventory, err)
	}
	if _, err = store.CheckoutByStripeSession(t.Context(), "cs_test_1"); !errors.Is(err, commerce.ErrNotFound) {
		t.Fatalf("failed checkout lookup = %v", err)
	}
}

func TestLoggedInCheckoutAssociatesUser(t *testing.T) {
	handler, users, store, gateway, _ := commerceHandler(t)

	_, primary, _, shipping := seedOneTimeProduct(t, store)
	user, err := users.CreateUser(t.Context(), auth.CreateUserParams{Username: "buyer.one", DisplayName: "Buyer One", Role: auth.RoleUser, Password: []byte(testPassword)})
	if err != nil {
		t.Fatal(err)
	}
	session, _, err := users.CreateSession(t.Context(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	cookies := csrfCookies(t, handler, "/shop/workshop")
	cookies[sessionCookie] = &http.Cookie{Name: sessionCookie, Value: session}
	added := serveForm(handler, http.MethodPost, "/cart/items", url.Values{"csrf_token": {cookies[csrfCookie].Value}, "sku": {primary.SKU}, "shipping_code": {shippingID(shipping)}, "quantity": {"1"}}, cookies)
	mergeCookies(cookies, added.Result().Cookies())
	response := serveForm(handler, http.MethodPost, "/checkout", url.Values{"csrf_token": {cookies[csrfCookie].Value}, "name": {"Buyer One"}, "email": {"buyer@example.com"}}, cookies)
	if response.Code != http.StatusOK {
		t.Fatalf("checkout: %d %s", response.Code, response.Body.String())
	}
	checkout, err := store.CheckoutByStripeSession(t.Context(), "cs_test_1")
	if err != nil || checkout.UserID == nil || *checkout.UserID != user.ID || len(gateway.created) != 1 {
		t.Fatalf("user checkout: %#v %v", checkout, err)
	}
}

func TestAdminProductManagementRequiresAdmin(t *testing.T) {
	handler, users, store, _, _ := commerceHandler(t)
	regular, err := users.CreateUser(t.Context(), auth.CreateUserParams{Username: "regular.user", DisplayName: "Regular User", Role: auth.RoleUser, Password: []byte(testPassword)})
	if err != nil {
		t.Fatal(err)
	}
	admin, err := users.CreateUser(t.Context(), auth.CreateUserParams{Username: "catalog.admin", DisplayName: "Catalog Admin", Role: auth.RoleAdmin, Password: []byte(testPassword)})
	if err != nil {
		t.Fatal(err)
	}
	regularToken, _, err := users.CreateSession(t.Context(), regular.ID)
	if err != nil {
		t.Fatal(err)
	}
	adminToken, _, err := users.CreateSession(t.Context(), admin.ID)
	if err != nil {
		t.Fatal(err)
	}

	forbidden := serveRequest(handler, httptest.NewRequest(http.MethodGet, "/admin/products", nil), map[string]*http.Cookie{sessionCookie: {Name: sessionCookie, Value: regularToken}})
	if forbidden.Code != http.StatusForbidden {
		t.Fatalf("regular user status = %d", forbidden.Code)
	}

	adminCookies := map[string]*http.Cookie{sessionCookie: {Name: sessionCookie, Value: adminToken}}
	page := serveWithCookies(handler, http.MethodGet, "/admin/products/new", nil, adminCookies)
	mergeCookies(adminCookies, page.Result().Cookies())
	if page.Code != http.StatusOK || adminCookies[csrfCookie] == nil || !strings.Contains(page.Body.String(), "Product fields") || strings.Contains(page.Body.String(), "Add SKU") {
		t.Fatalf("admin product page: %d %s", page.Code, page.Body.String())
	}
	created := serveForm(handler, http.MethodPost, "/admin/products", url.Values{
		"csrf_token": {adminCookies[csrfCookie].Value}, "name": {"Admin Product"}, "slug": {"admin-product"},
		"type": {"one_time"}, "master_sku": {"ADMIN-PRODUCT"}, "description": {"Editable product copy."}, "active": {"true"},
	}, adminCookies)
	if created.Code != http.StatusSeeOther {
		t.Fatalf("create product: %d %s", created.Code, created.Body.String())
	}
	products, err := store.ListProducts(t.Context())
	if err != nil || len(products) != 1 || products[0].MasterSKU != "ADMIN-PRODUCT" {
		t.Fatalf("products: %#v %v", products, err)
	}
}

func csrfCookies(t *testing.T, handler http.Handler, path string) map[string]*http.Cookie {
	t.Helper()
	cookies := make(map[string]*http.Cookie)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
	mergeCookies(cookies, response.Result().Cookies())
	if cookies[csrfCookie] == nil {
		t.Fatalf("missing csrf cookie for %s", path)
	}
	return cookies
}
func serveForm(handler http.Handler, method, path string, form url.Values, cookies map[string]*http.Cookie) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return serveRequest(handler, request, cookies)
}
func serveWithCookies(handler http.Handler, method, path string, body *strings.Reader, cookies map[string]*http.Cookie) *httptest.ResponseRecorder {
	var request *http.Request
	if body == nil {
		request = httptest.NewRequest(method, path, nil)
	} else {
		request = httptest.NewRequest(method, path, body)
	}
	return serveRequest(handler, request, cookies)
}
func serveRequest(handler http.Handler, request *http.Request, cookies map[string]*http.Cookie) *httptest.ResponseRecorder {
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
func mergeCookies(target map[string]*http.Cookie, source []*http.Cookie) {
	for _, cookie := range source {
		target[cookie.Name] = cookie
	}
}
func shippingID(option commerce.ShippingOption) string {
	return string(strconv.AppendInt(nil, option.ID, 10))
}
func storeInvitationUser(t *testing.T, store *commerce.Store, activationURL string) (commerce.Invitation, int64, error) {
	t.Helper()
	parsed, err := url.Parse(activationURL)
	if err != nil {
		return commerce.Invitation{}, 0, err
	}
	invitation, err := store.InvitationByToken(t.Context(), parsed.Query().Get("token"))
	return invitation, invitation.UserID, err
}
