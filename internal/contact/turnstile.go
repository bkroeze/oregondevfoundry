package contact

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

type Verifier interface {
	Verify(context.Context, string, string) error
}

type TurnstileVerifier struct {
	secret string
	client *http.Client
}

func NewTurnstileVerifier(secret string, client *http.Client) *TurnstileVerifier {
	return &TurnstileVerifier{secret: secret, client: client}
}

func (v *TurnstileVerifier) Verify(ctx context.Context, token, remoteIP string) error {
	values := url.Values{"secret": {v.secret}, "response": {token}, "remoteip": {remoteIP}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://challenges.cloudflare.com/turnstile/v0/siteverify", strings.NewReader(values.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := v.client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("turnstile returned status %d", response.StatusCode)
	}
	var result struct {
		Success bool   `json:"success"`
		Action  string `json:"action"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return err
	}
	if !result.Success || result.Action != "contact" {
		return fmt.Errorf("turnstile verification failed")
	}
	return nil
}
