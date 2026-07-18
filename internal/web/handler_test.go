package web

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/bkroeze/oregon-dev-foundry/internal/config"
	"github.com/bkroeze/oregon-dev-foundry/internal/contact"
)

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

func testHandler(sender *fakeSender, verifier fakeVerifier) http.Handler {
	return NewHandler(config.Config{TurnstileSiteKey: "site-key"}, sender, verifier)
}

func TestPublicRoutes(t *testing.T) {
	handler := testHandler(&fakeSender{}, fakeVerifier{})
	for _, test := range []struct {
		path, contains string
	}{
		{"/", "Oregon Dev Foundry"},
		{"/styles.css", "--paper"},
		{"/script.js", "menuButton"},
		{"/healthz", "ok\n"},
		{"/up", "ok\n"},
		{"/api/contact-config", `"turnstileSiteKey":"site-key"`},
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

func TestContactHTMXSuccessUsesInjectedDependencies(t *testing.T) {
	sender := &fakeSender{}
	handler := testHandler(sender, fakeVerifier{})
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
	handler := testHandler(sender, fakeVerifier{})
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
	handler := testHandler(sender, fakeVerifier{})
	form := validForm()
	request := httptest.NewRequest(http.MethodPost, "/api/contact", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusBadGateway || strings.Contains(response.Body.String(), "secret provider detail") || !strings.Contains(response.Body.String(), "Please email us instead") {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
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
