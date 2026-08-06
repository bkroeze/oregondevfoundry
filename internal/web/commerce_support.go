package web

import (
	"errors"
	"fmt"
	"net/mail"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/bkroeze/oregon-dev-foundry/internal/templates"
)

const (
	maxCommerceBody = 64 << 10
	cartCookie      = "odf_cart"
	referralCookie  = "odf_referral"
)

func money(cents int64, currency string) templates.Money {
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if currency == "" {
		currency = "USD"
	}
	sign := ""
	if cents < 0 {
		sign = "-"
		cents = -cents
	}
	if currency == "USD" {
		return templates.Money(fmt.Sprintf("%s$%d.%02d", sign, cents/100, cents%100))
	}
	return templates.Money(fmt.Sprintf("%s%d.%02d %s", sign, cents/100, cents%100, currency))
}

func parsePositiveInt(value string, maximum int64) (int64, error) {
	parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || parsed < 1 || parsed > maximum {
		return 0, errors.New("value is outside the allowed range")
	}
	return parsed, nil
}

func parseNonnegativeInt(value string, maximum int64) (int64, error) {
	parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || parsed < 0 || parsed > maximum {
		return 0, errors.New("value is outside the allowed range")
	}
	return parsed, nil
}

func validateBuyer(name, email string) map[string]string {
	problems := make(map[string]string)
	if length := utf8.RuneCountInString(strings.TrimSpace(name)); length < 1 || length > 120 {
		problems["name"] = "Enter a name no longer than 120 characters."
	}
	address, err := mail.ParseAddress(strings.TrimSpace(email))
	if err != nil || address.Address != strings.TrimSpace(email) || utf8.RuneCountInString(email) > 254 {
		problems["email"] = "Enter a valid email address."
	}
	return problems
}

func externalReferrer(raw string, requestURL *url.URL, requestHost string) (string, string, bool) {
	referrer, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (referrer.Scheme != "http" && referrer.Scheme != "https") || referrer.Host == "" {
		return "", "", false
	}
	requestHostname := requestHost
	if requestURL != nil && requestURL.Hostname() != "" {
		requestHostname = requestURL.Hostname()
	} else if parsedHost, err := url.Parse("//" + requestHost); err == nil && parsedHost.Hostname() != "" {
		requestHostname = parsedHost.Hostname()
	}
	if strings.EqualFold(referrer.Hostname(), requestHostname) {
		return "", "", false
	}
	return referrer.String(), strings.ToLower(referrer.Hostname()), true
}
