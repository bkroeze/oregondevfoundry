package commerce

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const webhookClaimLease = 5 * time.Minute

func (s *Store) CreateReferral(ctx context.Context, token, sourceURL, host string) (Referral, error) {
	token = strings.TrimSpace(token)
	sourceURL = strings.TrimSpace(sourceURL)
	host = strings.ToLower(strings.TrimSpace(host))
	if token == "" {
		var err error
		token, err = randomToken()
		if err != nil {
			return Referral{}, err
		}
	}
	if len(token) > 128 {
		return Referral{}, fmt.Errorf("%w: referral token too long", ErrInvalidInput)
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO commerce_referrals(token,source_url,host,created_at) VALUES(?,?,?,?)`, token, sourceURL, host, nowUnix(s))
	if err != nil {
		return Referral{}, fmt.Errorf("create referral: %w", err)
	}
	id, _ := result.LastInsertId()
	return s.referralByID(ctx, id)
}
func (s *Store) ReferralByToken(ctx context.Context, token string) (Referral, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return Referral{}, fmt.Errorf("%w: referral token required", ErrInvalidInput)
	}
	var id int64
	if err := s.db.QueryRowContext(ctx, `SELECT id FROM commerce_referrals WHERE token=? COLLATE NOCASE`, token).Scan(&id); err != nil {
		return Referral{}, mapNotFound(err)
	}
	return s.referralByID(ctx, id)
}
func (s *Store) ReferralByURLHost(ctx context.Context, sourceURL, host string) (Referral, error) {
	sourceURL = strings.TrimSpace(sourceURL)
	host = strings.ToLower(strings.TrimSpace(host))
	if sourceURL == "" && host == "" {
		return Referral{}, fmt.Errorf("%w: URL or host required", ErrInvalidInput)
	}
	var id int64
	if err := s.db.QueryRowContext(ctx, `SELECT id FROM commerce_referrals WHERE source_url=? AND host=? COLLATE NOCASE ORDER BY id LIMIT 1`, sourceURL, host).Scan(&id); err != nil {
		return Referral{}, mapNotFound(err)
	}
	return s.referralByID(ctx, id)
}
func (s *Store) FindOrCreateReferral(ctx context.Context, rawURL, host string) (Referral, error) {
	rawURL = strings.TrimSpace(rawURL)
	host = strings.ToLower(strings.TrimSpace(host))
	if rawURL != "" {
		parsed, err := url.Parse(rawURL)
		if err != nil {
			return Referral{}, fmt.Errorf("%w: invalid referral URL", ErrInvalidInput)
		}
		if token := strings.TrimSpace(parsed.Query().Get("ref")); token != "" {
			return s.ReferralByToken(ctx, token)
		}
	}
	referral, err := s.ReferralByURLHost(ctx, rawURL, host)
	if err == nil {
		return referral, nil
	}
	if err != ErrNotFound {
		return Referral{}, err
	}
	referral, err = s.CreateReferral(ctx, "", rawURL, host)
	if err == nil {
		return referral, nil
	}
	// A concurrent creator can win the unique URL/host insert.
	if existing, lookupErr := s.ReferralByURLHost(ctx, rawURL, host); lookupErr == nil {
		return existing, nil
	}
	return Referral{}, err
}
func (s *Store) AssociateReferralWithUser(ctx context.Context, userID, referralID int64) error {
	if userID <= 0 || referralID <= 0 {
		return fmt.Errorf("%w: user and referral required", ErrInvalidInput)
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO commerce_user_referrals(user_id,referral_id,created_at) VALUES(?,?,?) ON CONFLICT(user_id) DO UPDATE SET referral_id=excluded.referral_id`, userID, referralID, nowUnix(s))
	if err != nil {
		return fmt.Errorf("associate referral: %w", err)
	}
	return nil
}
func (s *Store) ReferralForUser(ctx context.Context, userID int64) (Referral, error) {
	if userID <= 0 {
		return Referral{}, fmt.Errorf("%w: user required", ErrInvalidInput)
	}
	var id int64
	if err := s.db.QueryRowContext(ctx, `SELECT referral_id FROM commerce_user_referrals WHERE user_id=?`, userID).Scan(&id); err != nil {
		return Referral{}, mapNotFound(err)
	}
	return s.referralByID(ctx, id)
}
func (s *Store) referralByID(ctx context.Context, id int64) (Referral, error) {
	var r Referral
	var created int64
	if err := s.db.QueryRowContext(ctx, `SELECT id,token,source_url,host,created_at FROM commerce_referrals WHERE id=?`, id).Scan(&r.ID, &r.Token, &r.SourceURL, &r.Host, &created); err != nil {
		return Referral{}, mapNotFound(err)
	}
	r.CreatedAt = time.Unix(created, 0).UTC()
	return r, nil
}

func (s *Store) ClaimWebhookEvent(ctx context.Context, eventID, eventType string) (bool, error) {
	eventID = strings.TrimSpace(eventID)
	eventType = strings.TrimSpace(eventType)
	if eventID == "" || eventType == "" {
		return false, fmt.Errorf("%w: event id and type required", ErrInvalidInput)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	var status string
	var claimedAt int64
	err = tx.QueryRowContext(ctx, `SELECT status,claimed_at FROM commerce_webhook_events WHERE stripe_event_id=?`, eventID).Scan(&status, &claimedAt)
	now := nowUnix(s)
	if err == sql.ErrNoRows {
		if _, err = tx.ExecContext(ctx, `INSERT INTO commerce_webhook_events(stripe_event_id,event_type,status,attempts,claimed_at,created_at,updated_at) VALUES(?,?,'processing',1,?,?,?)`, eventID, eventType, now, now, now); err != nil {
			return false, fmt.Errorf("claim webhook: %w", err)
		}
		if err = tx.Commit(); err != nil {
			return false, err
		}
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("load webhook event: %w", err)
	}
	if status == "completed" {
		return false, nil
	}
	if status == "processing" && claimedAt > now-int64(webhookClaimLease/time.Second) {
		return false, ErrWebhookInProgress
	}
	result, err := tx.ExecContext(ctx, `UPDATE commerce_webhook_events SET status='processing',event_type=?,attempts=attempts+1,last_error='',claimed_at=?,updated_at=? WHERE stripe_event_id=? AND (status='failed' OR (status='processing' AND claimed_at<=?))`, eventType, now, now, eventID, now-int64(webhookClaimLease/time.Second))
	if err != nil {
		return false, fmt.Errorf("reclaim webhook: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return changed == 1, nil
}
func (s *Store) CompleteWebhookEvent(ctx context.Context, eventID string) error {
	eventID = strings.TrimSpace(eventID)
	if eventID == "" {
		return fmt.Errorf("%w: event id required", ErrInvalidInput)
	}
	now := nowUnix(s)
	result, err := s.db.ExecContext(ctx, `UPDATE commerce_webhook_events SET status='completed',completed_at=?,updated_at=? WHERE stripe_event_id=? AND status='processing'`, now, now, eventID)
	if err != nil {
		return fmt.Errorf("complete webhook: %w", err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		var status string
		if loadErr := s.db.QueryRowContext(ctx, `SELECT status FROM commerce_webhook_events WHERE stripe_event_id=?`, eventID).Scan(&status); loadErr != nil {
			return mapNotFound(loadErr)
		}
		if status == "completed" {
			return nil
		}
		return fmt.Errorf("%w: webhook event is not claimed", ErrInvalidInput)
	}
	return nil
}
func (s *Store) ReleaseWebhookEvent(ctx context.Context, eventID string, processingErr error) error {
	eventID = strings.TrimSpace(eventID)
	if eventID == "" {
		return fmt.Errorf("%w: event id required", ErrInvalidInput)
	}
	message := ""
	if processingErr != nil {
		message = processingErr.Error()
	}
	result, err := s.db.ExecContext(ctx, `UPDATE commerce_webhook_events SET status='failed',last_error=?,updated_at=? WHERE stripe_event_id=? AND status='processing'`, message, nowUnix(s), eventID)
	if err != nil {
		return fmt.Errorf("release webhook: %w", err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
func (s *Store) FailWebhookEvent(ctx context.Context, eventID string, processingErr error) error {
	return s.ReleaseWebhookEvent(ctx, eventID, processingErr)
}
func (s *Store) WebhookEvent(ctx context.Context, eventID string) (WebhookEvent, error) {
	eventID = strings.TrimSpace(eventID)
	if eventID == "" {
		return WebhookEvent{}, fmt.Errorf("%w: event id required", ErrInvalidInput)
	}
	var event WebhookEvent
	var completed sql.NullInt64
	var claimed, created, updated int64
	if err := s.db.QueryRowContext(ctx, `SELECT stripe_event_id,event_type,status,attempts,last_error,claimed_at,completed_at,created_at,updated_at FROM commerce_webhook_events WHERE stripe_event_id=?`, eventID).Scan(&event.StripeEventID, &event.EventType, &event.Status, &event.Attempts, &event.LastError, &claimed, &completed, &created, &updated); err != nil {
		return WebhookEvent{}, mapNotFound(err)
	}
	event.ClaimedAt = time.Unix(claimed, 0).UTC()
	event.CompletedAt = ptrTime(completed)
	event.CreatedAt = time.Unix(created, 0).UTC()
	event.UpdatedAt = time.Unix(updated, 0).UTC()
	return event, nil
}
