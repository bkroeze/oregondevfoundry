package config

import (
	"strings"
	"testing"
)

func TestLoadValidatesEnvironment(t *testing.T) {
	for _, name := range []string{"MAILGUN_API_KEY", "MAILGUN_DOMAIN", "CONTACT_TO", "CONTACT_FROM", "TURNSTILE_SITE_KEY", "TURNSTILE_SECRET_KEY"} {
		t.Setenv(name, "")
	}
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "missing required environment variables") {
		t.Fatalf("expected missing environment error, got %v", err)
	}
}

func TestLoadAppliesDefaultsAndPort(t *testing.T) {
	values := map[string]string{
		"MAILGUN_API_KEY": "test-key", "MAILGUN_DOMAIN": "mg.example.com",
		"CONTACT_TO": "hello@example.com", "CONTACT_FROM": "Contact <contact@mg.example.com>",
		"TURNSTILE_SITE_KEY": "test-site", "TURNSTILE_SECRET_KEY": "test-secret",
		"MAILGUN_REGION": "", "PORT": "9090", "DATABASE_PATH": "", "SESSION_COOKIE_SECURE": "",
		"SERVICES_ENABLED": "", "TESTIMONIALS_ENABLED": "",
	}
	for name, value := range values {
		t.Setenv(name, value)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Address != ":9090" || cfg.MailgunRegion != "us" || cfg.DatabasePath != "data/oregon-dev-foundry.db" || !cfg.SessionCookieSecure || cfg.ServicesEnabled || cfg.TestimonialsEnabled {
		t.Fatalf("unexpected config: %#v", cfg)
	}
}

func TestLoadFeatureGates(t *testing.T) {
	for _, name := range []string{"MAILGUN_API_KEY", "MAILGUN_DOMAIN", "CONTACT_TO", "CONTACT_FROM", "TURNSTILE_SITE_KEY", "TURNSTILE_SECRET_KEY"} {
		t.Setenv(name, "value")
	}
	t.Setenv("SERVICES_ENABLED", "true")
	t.Setenv("TESTIMONIALS_ENABLED", "true")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.ServicesEnabled || !cfg.TestimonialsEnabled {
		t.Fatalf("feature gates not enabled: %#v", cfg)
	}
}

func TestLoadRejectsInvalidPortAndRegion(t *testing.T) {
	for _, name := range []string{"MAILGUN_API_KEY", "MAILGUN_DOMAIN", "CONTACT_TO", "CONTACT_FROM", "TURNSTILE_SITE_KEY", "TURNSTILE_SECRET_KEY"} {
		t.Setenv(name, "value")
	}
	for _, test := range []struct{ port, region, contains string }{
		{"70000", "us", "PORT"},
		{"8080", "elsewhere", "MAILGUN_REGION"},
	} {
		t.Setenv("PORT", test.port)
		t.Setenv("MAILGUN_REGION", test.region)
		if _, err := Load(); err == nil || !strings.Contains(err.Error(), test.contains) {
			t.Fatalf("port=%s region=%s: got %v", test.port, test.region, err)
		}
	}
}

func TestLoadRejectsInvalidCookieSecurity(t *testing.T) {
	for _, name := range []string{"MAILGUN_API_KEY", "MAILGUN_DOMAIN", "CONTACT_TO", "CONTACT_FROM", "TURNSTILE_SITE_KEY", "TURNSTILE_SECRET_KEY"} {
		t.Setenv(name, "value")
	}
	t.Setenv("SESSION_COOKIE_SECURE", "sometimes")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "SESSION_COOKIE_SECURE") {
		t.Fatalf("expected cookie security error, got %v", err)
	}
}

func TestLoadRejectsInvalidFeatureGates(t *testing.T) {
	for _, name := range []string{"MAILGUN_API_KEY", "MAILGUN_DOMAIN", "CONTACT_TO", "CONTACT_FROM", "TURNSTILE_SITE_KEY", "TURNSTILE_SECRET_KEY"} {
		t.Setenv(name, "value")
	}
	for _, name := range []string{"SERVICES_ENABLED", "TESTIMONIALS_ENABLED"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(name, "sometimes")
			if _, err := Load(); err == nil || !strings.Contains(err.Error(), name) {
				t.Fatalf("expected %s error, got %v", name, err)
			}
		})
	}
}

func TestLoadStripeConfiguration(t *testing.T) {
	for _, name := range []string{"MAILGUN_API_KEY", "MAILGUN_DOMAIN", "CONTACT_TO", "CONTACT_FROM", "TURNSTILE_SITE_KEY", "TURNSTILE_SECRET_KEY"} {
		t.Setenv(name, "value")
	}
	t.Setenv("STRIPE_SECRET_KEY", "sk_test_value")
	t.Setenv("STRIPE_PUBLISHABLE_KEY", "pk_test_value")
	t.Setenv("STRIPE_WEBHOOK_SECRET", "whsec_value")
	t.Setenv("PUBLIC_BASE_URL", "https://example.com/")
	t.Setenv("ORDER_FROM", "")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PublicBaseURL != "https://example.com" || cfg.OrderFrom != cfg.ContactFrom {
		t.Fatalf("unexpected Stripe config: %#v", cfg)
	}
}

func TestLoadRejectsPartialStripeConfiguration(t *testing.T) {
	for _, name := range []string{"MAILGUN_API_KEY", "MAILGUN_DOMAIN", "CONTACT_TO", "CONTACT_FROM", "TURNSTILE_SITE_KEY", "TURNSTILE_SECRET_KEY"} {
		t.Setenv(name, "value")
	}
	t.Setenv("STRIPE_SECRET_KEY", "sk_test_value")
	t.Setenv("STRIPE_PUBLISHABLE_KEY", "")
	t.Setenv("STRIPE_WEBHOOK_SECRET", "")
	t.Setenv("PUBLIC_BASE_URL", "")

	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "configured together") {
		t.Fatalf("expected partial Stripe configuration error, got %v", err)
	}
}

func TestLoadRejectsMalformedPublicBaseURL(t *testing.T) {
	for _, name := range []string{"MAILGUN_API_KEY", "MAILGUN_DOMAIN", "CONTACT_TO", "CONTACT_FROM", "TURNSTILE_SITE_KEY", "TURNSTILE_SECRET_KEY"} {
		t.Setenv(name, "value")
	}
	t.Setenv("STRIPE_SECRET_KEY", "sk_test_value")
	t.Setenv("STRIPE_PUBLISHABLE_KEY", "pk_test_value")
	t.Setenv("STRIPE_WEBHOOK_SECRET", "whsec_value")
	t.Setenv("PUBLIC_BASE_URL", "example.com/store?unsafe=true")

	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "PUBLIC_BASE_URL") {
		t.Fatalf("expected public URL error, got %v", err)
	}
}

func TestLoadRejectsPublicBaseURLPath(t *testing.T) {
	for _, name := range []string{"MAILGUN_API_KEY", "MAILGUN_DOMAIN", "CONTACT_TO", "CONTACT_FROM", "TURNSTILE_SITE_KEY", "TURNSTILE_SECRET_KEY"} {
		t.Setenv(name, "value")
	}
	t.Setenv("STRIPE_SECRET_KEY", "sk_test_value")
	t.Setenv("STRIPE_PUBLISHABLE_KEY", "pk_test_value")
	t.Setenv("STRIPE_WEBHOOK_SECRET", "whsec_value")
	t.Setenv("PUBLIC_BASE_URL", "https://example.com/store")

	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "without a path") {
		t.Fatalf("expected public URL path error, got %v", err)
	}
}
