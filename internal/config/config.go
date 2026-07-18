package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const defaultPort = 8080

type Config struct {
	Address            string
	ShutdownTimeout    time.Duration
	MailgunAPIKey      string
	MailgunDomain      string
	MailgunRegion      string
	ContactTo          string
	ContactFrom        string
	TurnstileSiteKey   string
	TurnstileSecretKey string
}

func Load() (Config, error) {
	cfg := Config{
		ShutdownTimeout:    10 * time.Second,
		MailgunAPIKey:      strings.TrimSpace(os.Getenv("MAILGUN_API_KEY")),
		MailgunDomain:      strings.TrimSpace(os.Getenv("MAILGUN_DOMAIN")),
		MailgunRegion:      strings.ToLower(strings.TrimSpace(os.Getenv("MAILGUN_REGION"))),
		ContactTo:          strings.TrimSpace(os.Getenv("CONTACT_TO")),
		ContactFrom:        strings.TrimSpace(os.Getenv("CONTACT_FROM")),
		TurnstileSiteKey:   strings.TrimSpace(os.Getenv("TURNSTILE_SITE_KEY")),
		TurnstileSecretKey: strings.TrimSpace(os.Getenv("TURNSTILE_SECRET_KEY")),
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
