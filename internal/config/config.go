package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const defaultPort = 8080

type Config struct {
	Address              string
	ShutdownTimeout      time.Duration
	MailgunAPIKey        string
	MailgunDomain        string
	MailgunRegion        string
	ContactTo            string
	ContactFrom          string
	TurnstileSiteKey     string
	TurnstileSecretKey   string
	DatabasePath         string
	SessionCookieSecure  bool
	StripeSecretKey      string
	StripePublishableKey string
	StripeWebhookSecret  string
	PublicBaseURL        string
	OrderFrom            string
}

func Load() (Config, error) {
	cfg := Config{
		ShutdownTimeout:      10 * time.Second,
		MailgunAPIKey:        strings.TrimSpace(os.Getenv("MAILGUN_API_KEY")),
		MailgunDomain:        strings.TrimSpace(os.Getenv("MAILGUN_DOMAIN")),
		MailgunRegion:        strings.ToLower(strings.TrimSpace(os.Getenv("MAILGUN_REGION"))),
		ContactTo:            strings.TrimSpace(os.Getenv("CONTACT_TO")),
		ContactFrom:          strings.TrimSpace(os.Getenv("CONTACT_FROM")),
		TurnstileSiteKey:     strings.TrimSpace(os.Getenv("TURNSTILE_SITE_KEY")),
		TurnstileSecretKey:   strings.TrimSpace(os.Getenv("TURNSTILE_SECRET_KEY")),
		DatabasePath:         strings.TrimSpace(os.Getenv("DATABASE_PATH")),
		SessionCookieSecure:  true,
		StripeSecretKey:      strings.TrimSpace(os.Getenv("STRIPE_SECRET_KEY")),
		StripePublishableKey: strings.TrimSpace(os.Getenv("STRIPE_PUBLISHABLE_KEY")),
		StripeWebhookSecret:  strings.TrimSpace(os.Getenv("STRIPE_WEBHOOK_SECRET")),
		PublicBaseURL:        strings.TrimRight(strings.TrimSpace(os.Getenv("PUBLIC_BASE_URL")), "/"),
		OrderFrom:            strings.TrimSpace(os.Getenv("ORDER_FROM")),
	}

	if cfg.DatabasePath == "" {
		cfg.DatabasePath = "data/oregon-dev-foundry.db"
	}
	if value := strings.TrimSpace(os.Getenv("SESSION_COOKIE_SECURE")); value != "" {
		secure, err := strconv.ParseBool(value)
		if err != nil {
			return Config{}, errors.New("SESSION_COOKIE_SECURE must be true or false")
		}
		cfg.SessionCookieSecure = secure
	}

	port := defaultPort
	if value := strings.TrimSpace(os.Getenv("PORT")); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 65535 {
			return Config{}, fmt.Errorf("PORT must be an integer between 1 and 65535")
		}
		port = parsed
	}
	cfg.Address = fmt.Sprintf(":%d", port)

	if cfg.MailgunRegion == "" {
		cfg.MailgunRegion = "us"
	}
	if cfg.MailgunRegion != "us" && cfg.MailgunRegion != "eu" {
		return Config{}, errors.New("MAILGUN_REGION must be either us or eu")
	}

	if cfg.OrderFrom == "" {
		cfg.OrderFrom = cfg.ContactFrom
	}
	stripeValues := []string{cfg.StripeSecretKey, cfg.StripePublishableKey, cfg.StripeWebhookSecret, cfg.PublicBaseURL}
	configured := 0
	for _, value := range stripeValues {
		if value != "" {
			configured++
		}
	}
	if configured != 0 && configured != len(stripeValues) {
		return Config{}, errors.New("STRIPE_SECRET_KEY, STRIPE_PUBLISHABLE_KEY, STRIPE_WEBHOOK_SECRET, and PUBLIC_BASE_URL must be configured together")
	}
	if cfg.PublicBaseURL != "" {
		baseURL, err := url.Parse(cfg.PublicBaseURL)
		if err != nil || (baseURL.Scheme != "http" && baseURL.Scheme != "https") || baseURL.Host == "" || baseURL.RawQuery != "" || baseURL.Fragment != "" {
			return Config{}, errors.New("PUBLIC_BASE_URL must be an absolute http or https URL without a query or fragment")
		}
		cfg.PublicBaseURL = strings.TrimRight(baseURL.String(), "/")
	}

	required := map[string]string{
		"MAILGUN_API_KEY":      cfg.MailgunAPIKey,
		"MAILGUN_DOMAIN":       cfg.MailgunDomain,
		"CONTACT_TO":           cfg.ContactTo,
		"CONTACT_FROM":         cfg.ContactFrom,
		"TURNSTILE_SITE_KEY":   cfg.TurnstileSiteKey,
		"TURNSTILE_SECRET_KEY": cfg.TurnstileSecretKey,
	}
	missing := make([]string, 0, len(required))
	for _, name := range []string{"MAILGUN_API_KEY", "MAILGUN_DOMAIN", "CONTACT_TO", "CONTACT_FROM", "TURNSTILE_SITE_KEY", "TURNSTILE_SECRET_KEY"} {
		if required[name] == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return Config{}, fmt.Errorf("missing required environment variables: %s", strings.Join(missing, ", "))
	}

	return cfg, nil
}
