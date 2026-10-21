package web

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/bkroeze/oregon-dev-foundry/internal/auth"
	"github.com/bkroeze/oregon-dev-foundry/internal/config"
	"github.com/bkroeze/oregon-dev-foundry/internal/contact"
)

const testPassword = "correct horse battery staple"

type fakeSender struct {
	messages []contact.Message
	err      error
}

func (f *fakeSender) Send(_ context.Context, message contact.Message) error {
	f.messages = append(f.messages, message)
	return f.err
}

type fakeVerifier struct{ err error }

func (f fakeVerifier) Verify(context.Context, string, string) error { return f.err }

func testStore(t *testing.T) *auth.Store {
	t.Helper()
	store, err := auth.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func testHandler(t *testing.T, sender *fakeSender, verifier fakeVerifier) http.Handler {
	t.Helper()
	return NewHandler(config.Config{TurnstileSiteKey: "site-key", ServicesEnabled: true, TestimonialsEnabled: true}, sender, verifier, testStore(t))
}

func TestPublicRoutes(t *testing.T) {
	handler := testHandler(t, &fakeSender{}, fakeVerifier{})
	for _, test := range []struct {
		path, contains string
	}{
		{"/", "Oregon Dev Foundry"},
		{"/styles.css", "--paper"},
		{"/script.js", "menuButton"},
		{"/healthz", "ok\n"},
		{"/up", "ok\n"},
		{"/api/contact-config", `"turnstileSiteKey":"site-key"`},
		{"/services", "Custom QR code signage"},
		{"/services", "/shop/vending-signage"},
		{"/services/ai-concierge", "AI Concierge"},
		{"/services/ai-concierge", "Find the busywork"},
		{"/services/ai-concierge", "Request an assessment"},
		{"/login", "Customer status"},
	} {
		t.Run(test.path, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, test.path, nil))
			if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), test.contains) {
				t.Fatalf("GET %s: status=%d body=%q", test.path, response.Code, response.Body.String())
			}
		})
	}
}

func TestMeridianPresaleRedirect(t *testing.T) {
	handler := testHandler(t, &fakeSender{}, fakeVerifier{})
	request := httptest.NewRequest(http.MethodGet, "/meridian-presale", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code < 300 || response.Code >= 400 {
		t.Fatalf("status=%d, want redirect", response.Code)
	}
	if got := response.Header().Get("Location"); got != "/meridian-presale/" {
		t.Fatalf("Location=%q, want /meridian-presale/", got)
	}
	location, err := url.Parse(response.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	assetURL := request.URL.ResolveReference(location).ResolveReference(&url.URL{Path: "assets/chart-portland.svg"})
	assetResponse := httptest.NewRecorder()
	handler.ServeHTTP(assetResponse, httptest.NewRequest(http.MethodGet, assetURL.String(), nil))
	if assetResponse.Code != http.StatusOK {
		t.Fatalf("relative asset %s: status=%d", assetURL, assetResponse.Code)
	}
	if got := assetResponse.Header().Get("Content-Type"); got != "image/svg+xml" {
		t.Fatalf("relative asset Content-Type=%q, want image/svg+xml", got)
	}
}

func TestMeridianPresaleVideoRange(t *testing.T) {
	handler := testHandler(t, &fakeSender{}, fakeVerifier{})
	const path = "/meridian-presale/assets/demo-retro.mp4"
	full := httptest.NewRecorder()
	handler.ServeHTTP(full, httptest.NewRequest(http.MethodGet, path, nil))
	if full.Code != http.StatusOK {
		t.Fatalf("full video status=%d, want %d", full.Code, http.StatusOK)
	}
	if full.Body.Len() < 32 {
		t.Fatalf("video too short: %d bytes", full.Body.Len())
	}
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.Header.Set("Range", "bytes=16-31")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusPartialContent {
		t.Fatalf("range status=%d, want %d", response.Code, http.StatusPartialContent)
	}
	for name, want := range map[string]string{
		"Content-Type":   "video/mp4",
		"Accept-Ranges":  "bytes",
		"Content-Range":  fmt.Sprintf("bytes 16-31/%d", full.Body.Len()),
		"Content-Length": "16",
	} {
		if got := response.Header().Get(name); got != want {
			t.Errorf("%s=%q, want %q", name, got, want)
		}
	}
	if !bytes.Equal(response.Body.Bytes(), full.Body.Bytes()[16:32]) {
		t.Fatal("range response does not match requested video bytes")
	}
}

func TestFeatureGates(t *testing.T) {
	for _, test := range []struct {
		name                string
		config              config.Config
		serviceStatus       int
		testimonialStatus   int
		servicesVisible     bool
		testimonialsVisible bool
	}{
		{
			name:              "disabled",
			serviceStatus:     http.StatusNotFound,
			testimonialStatus: http.StatusNotFound,
		},
		{
			name:              "services only",
			config:            config.Config{ServicesEnabled: true},
			serviceStatus:     http.StatusOK,
			testimonialStatus: http.StatusNotFound,
			servicesVisible:   true,
		},
		{
			name:                "testimonials only",
			config:              config.Config{TestimonialsEnabled: true},
			serviceStatus:       http.StatusNotFound,
			testimonialStatus:   http.StatusOK,
			testimonialsVisible: true,
		},
		{
			name: "enabled",
			config: config.Config{
				ServicesEnabled:     true,
				TestimonialsEnabled: true,
			},
			serviceStatus:       http.StatusOK,
			testimonialStatus:   http.StatusOK,
			servicesVisible:     true,
			testimonialsVisible: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler := NewHandler(test.config, &fakeSender{}, fakeVerifier{}, testStore(t))

			for _, path := range []string{"/services", "/services/ai-concierge", "/shop/vending-signage"} {
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
				if response.Code != test.serviceStatus {
					t.Errorf("GET %s: status=%d, want %d", path, response.Code, test.serviceStatus)
				}
			}

			testimonialResponse := httptest.NewRecorder()
			handler.ServeHTTP(testimonialResponse, httptest.NewRequest(http.MethodGet, "/assets/testimonials/circle-thrice.png", nil))
			if testimonialResponse.Code != test.testimonialStatus {
				t.Errorf("GET testimonial asset: status=%d, want %d", testimonialResponse.Code, test.testimonialStatus)
			}

			homeResponse := httptest.NewRecorder()
			handler.ServeHTTP(homeResponse, httptest.NewRequest(http.MethodGet, "/", nil))
			body := homeResponse.Body.String()
			if got := strings.Contains(body, `href="/services">Services</a>`); got != test.servicesVisible {
				t.Fatalf("services navigation visible = %t, want %t", got, test.servicesVisible)
			}
			if got := strings.Contains(body, `id="testimonials"`); got != test.testimonialsVisible {
				t.Fatalf("testimonials visible = %t, want %t", got, test.testimonialsVisible)
			}
		})
	}
}

// TestAIConciergePageIsUnlinked guards the deliberate decision to keep the
// AI Concierge landing page dark: reachable by direct URL but not linked from
// the homepage or the public services page, and kept out of search indexes.
func TestAIConciergePageIsUnlinked(t *testing.T) {
	handler := testHandler(t, &fakeSender{}, fakeVerifier{})
	for _, path := range []string{"/", "/services"} {
		t.Run(path, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
			if strings.Contains(response.Body.String(), "ai-concierge") {
				t.Fatalf("%s must not link to the AI Concierge page", path)
			}
		})
	}
	t.Run("noindex", func(t *testing.T) {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/services/ai-concierge", nil))
		if got := response.Header().Get("X-Robots-Tag"); got != "noindex, nofollow" {
			t.Fatalf("X-Robots-Tag = %q, want %q", got, "noindex, nofollow")
		}
	})
}

func TestContactHTMXSuccessUsesInjectedDependencies(t *testing.T) {
	sender := &fakeSender{}
	handler := testHandler(t, sender, fakeVerifier{})
	form := validForm()
	request := httptest.NewRequest(http.MethodPost, "/api/contact", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("HX-Request", "true")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Message sent") {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
	if len(sender.messages) != 1 || sender.messages[0].Email != "ada@example.com" {
		t.Fatalf("unexpected messages: %#v", sender.messages)
	}
}

func TestContactValidationDoesNotSendMail(t *testing.T) {
	sender := &fakeSender{}
	handler := testHandler(t, sender, fakeVerifier{})
	form := validForm()
	form.Set("email", "not-an-email")
	request := httptest.NewRequest(http.MethodPost, "/api/contact", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), "Please correct") {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
	if len(sender.messages) != 0 {
		t.Fatalf("validation sent mail: %#v", sender.messages)
	}
	if !strings.Contains(response.Body.String(), "<!doctype html>") {
		t.Fatal("non-HTMX fallback did not render the full accessible page")
	}
}

func TestContactProviderErrorsAreSafe(t *testing.T) {
	sender := &fakeSender{err: errors.New("secret provider detail")}
	handler := testHandler(t, sender, fakeVerifier{})
	form := validForm()
	request := httptest.NewRequest(http.MethodPost, "/api/contact", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusBadGateway || strings.Contains(response.Body.String(), "secret provider detail") || !strings.Contains(response.Body.String(), "Please email us instead") {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
}

func TestAuthenticationRoleNavigationAndAuthorization(t *testing.T) {
	for _, test := range []struct {
		role           auth.Role
		status         auth.Status
		allowedPath    string
		allowedText    string
		forbiddenPath  string
		dropdownLink   string
		hiddenLinkText string
	}{
		{auth.RoleUser, auth.StatusNewCustomer, "/profile", "User profile", "/client", "", "Client profile"},
		{auth.RoleClient, auth.StatusClient, "/client", "Client landing", "/admin", "Client profile", "Admin"},
		{auth.RoleAdmin, auth.StatusAdmin, "/admin", "Admin landing", "/client", "Admin", "Client profile"},
	} {
		t.Run(string(test.role), func(t *testing.T) {
			store := testStore(t)
			username := string(test.role) + ".one"
			if _, err := store.CreateUser(t.Context(), auth.CreateUserParams{Username: username, DisplayName: strings.ToUpper(string(test.role[:1])) + string(test.role[1:]) + " One", Role: test.role, Password: []byte(testPassword)}); err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(NewHandler(config.Config{}, &fakeSender{}, fakeVerifier{}, store))
			defer server.Close()
			client := clientWithCookies(t)

			profileBody := loginUser(t, client, server.URL, username, testPassword)
			if !strings.Contains(profileBody, string(test.status)) || !strings.Contains(profileBody, "href=\"/profile\"") {
				t.Fatalf("profile omitted role status or profile link: %s", profileBody)
			}
			if test.dropdownLink != "" && !strings.Contains(profileBody, test.dropdownLink) {
				t.Fatalf("dropdown omitted %q: %s", test.dropdownLink, profileBody)
			}
			if strings.Contains(profileBody, ">"+test.hiddenLinkText+"</a>") {
				t.Fatalf("dropdown exposed %q to %s", test.hiddenLinkText, test.role)
			}

			response := get(t, client, server.URL+test.allowedPath)
			allowedBody := readBody(t, response)
			if response.StatusCode != http.StatusOK || !strings.Contains(allowedBody, test.allowedText) || !strings.Contains(allowedBody, string(test.status)) {
				t.Fatalf("GET %s: status=%d body=%s", test.allowedPath, response.StatusCode, allowedBody)
			}
			response = get(t, client, server.URL+test.forbiddenPath)
			forbiddenBody := readBody(t, response)
			if response.StatusCode != http.StatusForbidden || !strings.Contains(forbiddenBody, "Access denied") {
				t.Fatalf("GET %s: status=%d body=%s", test.forbiddenPath, response.StatusCode, forbiddenBody)
			}
		})
	}
}

func TestLoginFailureCSRFAndSecureSessionCookie(t *testing.T) {
	store := testStore(t)
	if _, err := store.CreateUser(t.Context(), auth.CreateUserParams{Username: "secure.user", DisplayName: "Secure User", Role: auth.RoleUser, Password: []byte(testPassword)}); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(config.Config{SessionCookieSecure: true}, &fakeSender{}, fakeVerifier{}, store)

	loginPage := httptest.NewRecorder()
	handler.ServeHTTP(loginPage, httptest.NewRequest(http.MethodGet, "/login", nil))
	csrf := responseCookie(t, loginPage.Result(), csrfCookie)

	missingCSRF := postFormRequest("/login", url.Values{"username": {"secure.user"}, "password": {testPassword}})
	missingResponse := httptest.NewRecorder()
	handler.ServeHTTP(missingResponse, missingCSRF)
	if missingResponse.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF status=%d", missingResponse.Code)
	}

	wrongForm := url.Values{"username": {"secure.user"}, "password": {"wrong password"}, "csrf_token": {csrf.Value}}
	wrongRequest := postFormRequest("/login", wrongForm)
	wrongRequest.AddCookie(csrf)
	wrongResponse := httptest.NewRecorder()
	handler.ServeHTTP(wrongResponse, wrongRequest)
	if wrongResponse.Code != http.StatusUnauthorized || !strings.Contains(wrongResponse.Body.String(), "Invalid username or password") {
		t.Fatalf("wrong password status=%d body=%s", wrongResponse.Code, wrongResponse.Body.String())
	}

	validForm := url.Values{"username": {"secure.user"}, "password": {testPassword}, "csrf_token": {csrf.Value}}
	validRequest := postFormRequest("/login", validForm)
	validRequest.AddCookie(csrf)
	validResponse := httptest.NewRecorder()
	handler.ServeHTTP(validResponse, validRequest)
	if validResponse.Code != http.StatusSeeOther {
		t.Fatalf("login status=%d body=%s", validResponse.Code, validResponse.Body.String())
	}
	session := responseCookie(t, validResponse.Result(), sessionCookie)
	if !session.HttpOnly || !session.Secure || session.SameSite != http.SameSiteLaxMode || len(session.Value) != 64 {
		t.Fatalf("insecure session cookie: %#v", session)
	}
}

func TestLogoutClearsSessionAndProtectedRoutesRedirect(t *testing.T) {
	store := testStore(t)
	if _, err := store.CreateUser(t.Context(), auth.CreateUserParams{Username: "logout.user", DisplayName: "Logout User", Role: auth.RoleUser, Password: []byte(testPassword)}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewHandler(config.Config{}, &fakeSender{}, fakeVerifier{}, store))
	defer server.Close()
	client := clientWithCookies(t)
	loginUser(t, client, server.URL, "logout.user", testPassword)

	csrf := jarCookie(t, client, server.URL, csrfCookie)
	response, err := client.PostForm(server.URL+"/logout", url.Values{"csrf_token": {csrf.Value}})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.Request.URL.Path != "/" {
		t.Fatalf("logout ended at %s", response.Request.URL)
	}
	response = get(t, client, server.URL+"/profile")
	_ = response.Body.Close()
	if response.Request.URL.Path != "/login" || response.Request.URL.Query().Get("next") != "/profile" {
		t.Fatalf("protected route ended at %s", response.Request.URL)
	}
}

func validForm() url.Values {
	return url.Values{
		"name":                  {"Ada Lovelace"},
		"email":                 {"ada@example.com"},
		"company":               {"Analytical Engines"},
		"message":               {"We need help building a durable operating system."},
		"cf-turnstile-response": {"test-token"},
	}
}

func clientWithCookies(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{Jar: jar}
}

func loginUser(t *testing.T, client *http.Client, baseURL, username, password string) string {
	t.Helper()
	response := get(t, client, baseURL+"/login")
	_ = response.Body.Close()
	csrf := jarCookie(t, client, baseURL, csrfCookie)
	response, err := client.PostForm(baseURL+"/login", url.Values{"username": {username}, "password": {password}, "csrf_token": {csrf.Value}})
	if err != nil {
		t.Fatal(err)
	}
	body := readBody(t, response)
	if response.StatusCode != http.StatusOK || response.Request.URL.Path != "/profile" {
		t.Fatalf("login status=%d url=%s body=%s", response.StatusCode, response.Request.URL, body)
	}
	return body
}

func get(t *testing.T, client *http.Client, target string) *http.Response {
	t.Helper()
	response, err := client.Get(target)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func readBody(t *testing.T, response *http.Response) string {
	t.Helper()
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func jarCookie(t *testing.T, client *http.Client, baseURL, name string) *http.Cookie {
	t.Helper()
	parsed, err := url.Parse(baseURL)
	if err != nil {
		t.Fatal(err)
	}
	for _, cookie := range client.Jar.Cookies(parsed) {
		if cookie.Name == name {
			return cookie
		}
	}
	t.Fatalf("cookie %s not found", name)
	return nil
}

func responseCookie(t *testing.T, response *http.Response, name string) *http.Cookie {
	t.Helper()
	for _, cookie := range response.Cookies() {
		if cookie.Name == name {
			return cookie
		}
	}
	t.Fatalf("response cookie %s not found", name)
	return nil
}

func postFormRequest(target string, form url.Values) *http.Request {
	request := httptest.NewRequest(http.MethodPost, target, strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return request
}
