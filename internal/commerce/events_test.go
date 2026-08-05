package commerce

import (
	"errors"
	"testing"
	"time"
)

func TestWebhookClaimReclaimsExpiredProcessingLease(t *testing.T) {
	store, _ := newTestStore(t)
	now := time.Date(2026, time.July, 21, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	claimed, err := store.ClaimWebhookEvent(t.Context(), "evt_stale", "checkout.session.completed")
	if err != nil || !claimed {
		t.Fatalf("first claim = %v, %v", claimed, err)
	}
	claimed, err = store.ClaimWebhookEvent(t.Context(), "evt_stale", "checkout.session.completed")
	if !errors.Is(err, ErrWebhookInProgress) || claimed {
		t.Fatalf("active lease claim = %v, %v", claimed, err)
	}
	now = now.Add(webhookClaimLease + time.Second)
	claimed, err = store.ClaimWebhookEvent(t.Context(), "evt_stale", "checkout.session.completed")
	if err != nil || !claimed {
		t.Fatalf("expired lease claim = %v, %v", claimed, err)
	}
	event, err := store.WebhookEvent(t.Context(), "evt_stale")
	if err != nil || event.Attempts != 2 || !event.ClaimedAt.Equal(now) {
		t.Fatalf("reclaimed event = %#v, %v", event, err)
	}
}
