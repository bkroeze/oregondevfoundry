package payment

import (
	"testing"
	"time"

	"github.com/stripe/stripe-go/v82/webhook"
)

func TestParseWebhookVerifiesAndDecodesPaidCheckout(t *testing.T) {
	const secret = "whsec_test"
	payload := []byte(`{"id":"evt_123","api_version":"2025-08-27.basil","type":"checkout.session.completed","data":{"object":{"id":"cs_123","mode":"payment","payment_status":"paid","status":"complete","client_reference_id":"42","customer":{"id":"cus_123"},"payment_intent":{"id":"pi_123"},"customer_details":{"email":"buyer@example.com"},"metadata":{"checkout_id":"42"}}}}`)
	signed := webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{Payload: payload, Secret: secret, Timestamp: time.Now()})

	event, err := NewStripeGateway("sk_test", secret).ParseWebhook(signed.Payload, signed.Header)
	if err != nil {
		t.Fatal(err)
	}
	if event.ID != "evt_123" || event.Type != "checkout.session.completed" || event.Session.ID != "cs_123" || event.Session.PaymentStatus != "paid" {
		t.Fatalf("unexpected event: %#v", event)
	}
	if event.Session.CustomerID != "cus_123" || event.Session.PaymentIntentID != "pi_123" || event.Session.CustomerEmail != "buyer@example.com" || event.Session.Metadata["checkout_id"] != "42" {
		t.Fatalf("unexpected session: %#v", event.Session)
	}
}

func TestParseWebhookDecodesExpiredCheckout(t *testing.T) {
	const secret = "whsec_test"
	payload := []byte(`{"id":"evt_expired","api_version":"2025-08-27.basil","type":"checkout.session.expired","data":{"object":{"id":"cs_expired","mode":"payment","payment_status":"unpaid","status":"expired"}}}`)
	signed := webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{Payload: payload, Secret: secret, Timestamp: time.Now()})
	event, err := NewStripeGateway("sk_test", secret).ParseWebhook(signed.Payload, signed.Header)
	if err != nil {
		t.Fatal(err)
	}
	if event.Type != "checkout.session.expired" || event.Session.ID != "cs_expired" || event.Session.Status != "expired" {
		t.Fatalf("unexpected expired event: %#v", event)
	}
}

func TestParseWebhookDecodesAsynchronousPaymentFailure(t *testing.T) {
	const secret = "whsec_test"
	payload := []byte(`{"id":"evt_failed","api_version":"2025-08-27.basil","type":"checkout.session.async_payment_failed","data":{"object":{"id":"cs_failed","mode":"payment","payment_status":"unpaid","status":"complete"}}}`)
	signed := webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{Payload: payload, Secret: secret, Timestamp: time.Now()})
	event, err := NewStripeGateway("sk_test", secret).ParseWebhook(signed.Payload, signed.Header)
	if err != nil {
		t.Fatal(err)
	}
	if event.Type != "checkout.session.async_payment_failed" || event.Session.ID != "cs_failed" || event.Session.Status != "complete" {
		t.Fatalf("unexpected failed event: %#v", event)
	}
}

func TestParseWebhookRejectsInvalidSignature(t *testing.T) {
	_, err := NewStripeGateway("sk_test", "whsec_test").ParseWebhook([]byte(`{"id":"evt_123"}`), "t=1,v1=invalid")
	if err == nil {
		t.Fatal("expected invalid signature error")
	}
}
