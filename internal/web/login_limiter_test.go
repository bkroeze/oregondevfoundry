package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/bkroeze/oregon-dev-foundry/internal/auth"
	"github.com/bkroeze/oregon-dev-foundry/internal/config"
)

func TestLoginRateLimitStopsRepeatedPasswordChecks(t *testing.T) {
	store := testStore(t)
	if _, err := store.CreateUser(t.Context(), auth.CreateUserParams{Username: "limited.user", DisplayName: "Limited User", Role: auth.RoleUser, Password: []byte(testPassword)}); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(config.Config{}, &fakeSender{}, fakeVerifier{}, store)

	loginPage := httptest.NewRecorder()
	handler.ServeHTTP(loginPage, httptest.NewRequest(http.MethodGet, "/login", nil))
	csrf := responseCookie(t, loginPage.Result(), csrfCookie)
	for attempt := range loginFailureLimit {
		request := postFormRequest("/login", url.Values{"username": {"limited.user"}, "password": {"wrong password"}, "csrf_token": {csrf.Value}})
		request.AddCookie(csrf)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d status=%d body=%s", attempt+1, response.Code, response.Body.String())
		}
	}

	request := postFormRequest("/login", url.Values{"username": {"limited.user"}, "password": {testPassword}, "csrf_token": {csrf.Value}})
	request.AddCookie(csrf)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusTooManyRequests || !strings.Contains(response.Body.String(), "Too many login attempts") {
		t.Fatalf("rate limit status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestSuccessfulLoginDoesNotClearClientFailures(t *testing.T) {
	limiter := newLoginLimiter(1)
	const client = "192.0.2.10"
	for range loginFailureLimit - 1 {
		release, allowed := limiter.begin(client, "victim.user")
		if !allowed {
			t.Fatal("client blocked before failure limit")
		}
		release()
		limiter.failed(client, "victim.user")
	}

	release, allowed := limiter.begin(client, "attacker.user")
	if !allowed {
		t.Fatal("own-account login unexpectedly blocked")
	}
	release()
	limiter.succeeded(client, "attacker.user")

	release, allowed = limiter.begin(client, "victim.user")
	if !allowed {
		t.Fatal("client blocked before final failed attempt")
	}
	release()
	limiter.failed(client, "victim.user")
	if _, allowed := limiter.begin(client, "another.user"); allowed {
		t.Fatal("successful own-account login cleared the client failure bucket")
	}
}

func TestLoginRateLimitFollowsAccountAcrossClients(t *testing.T) {
	limiter := newLoginLimiter(1)
	for attempt := range loginFailureLimit {
		client := fmt.Sprintf("192.0.2.%d", attempt+1)
		release, allowed := limiter.begin(client, " Victim.User ")
		if !allowed {
			t.Fatalf("account blocked before failure limit on attempt %d", attempt+1)
		}
		release()
		limiter.failed(client, "victim.user")
	}
	if _, allowed := limiter.begin("198.51.100.10", "VICTIM.USER"); allowed {
		t.Fatal("account limit reset when client address changed")
	}
}
