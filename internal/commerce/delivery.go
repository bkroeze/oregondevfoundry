package commerce

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

const deliveryClaimLease = 5 * time.Minute

func (s *Store) issueInvitationTokenTx(ctx context.Context, tx *sql.Tx, userID int64, email string, referralID *int64) (string, error) {
	raw, err := randomToken()
	if err != nil {
		return "", err
	}
	hash := tokenHash(raw)
	now := nowUnix(s)
	result, err := tx.ExecContext(ctx, `INSERT INTO commerce_invitations(user_id,email,token_hash,referral_id,delivery_status,created_at) VALUES(?,?,?,?,'pending',?) ON CONFLICT(user_id) DO UPDATE SET token_hash=excluded.token_hash,referral_id=COALESCE(commerce_invitations.referral_id,excluded.referral_id),delivery_status='pending',delivery_claimed_at=NULL,sent_at=NULL WHERE commerce_invitations.used_at IS NULL AND commerce_invitations.delivery_status='pending'`, userID, email, hash[:], nullableInt64(referralID), now)
	if err != nil {
		return "", fmt.Errorf("issue invitation: %w", err)
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return "", ErrInvitationAlreadyUsed
	}
	return raw, nil
}

func (s *Store) IssueInvitationToken(ctx context.Context, userID int64) (string, error) {
	if userID <= 0 {
		return "", fmt.Errorf("%w: user id required", ErrInvalidInput)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()
	var email string
	var referral, used sql.NullInt64
	var delivery string
	if err := tx.QueryRowContext(ctx, `SELECT email,referral_id,used_at,delivery_status FROM commerce_invitations WHERE user_id=?`, userID).Scan(&email, &referral, &used, &delivery); err != nil {
		return "", mapNotFound(err)
	}
	if used.Valid {
		return "", ErrInvitationAlreadyUsed
	}
	if delivery != "pending" {
		return "", fmt.Errorf("%w: invitation delivery is already in progress or complete", ErrInvalidInput)
	}
	raw, err := s.issueInvitationTokenTx(ctx, tx, userID, email, ptrInt64(referral))
	if err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return raw, nil
}

func (s *Store) InvitationByToken(ctx context.Context, token string) (Invitation, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return Invitation{}, fmt.Errorf("%w: invitation token required", ErrInvalidInput)
	}
	hash := tokenHash(token)
	var invitation Invitation
	var used, sent sql.NullInt64
	var created int64
	if err := s.db.QueryRowContext(ctx, `SELECT id,user_id,email,used_at,sent_at,created_at FROM commerce_invitations WHERE token_hash=?`, hash[:]).Scan(&invitation.ID, &invitation.UserID, &invitation.Email, &used, &sent, &created); err != nil {
		return Invitation{}, mapNotFound(err)
	}
	invitation.UsedAt = ptrTime(used)
	invitation.SentAt = ptrTime(sent)
	invitation.CreatedAt = time.Unix(created, 0).UTC()
	return invitation, nil
}

func (s *Store) ClaimInvitationDelivery(ctx context.Context, userID int64) (bool, error) {
	return s.claimDelivery(ctx, `UPDATE commerce_invitations SET delivery_status='sending',delivery_claimed_at=? WHERE user_id=? AND used_at IS NULL AND (delivery_status='pending' OR (delivery_status='sending' AND (delivery_claimed_at IS NULL OR delivery_claimed_at<=?)))`, `SELECT delivery_status FROM commerce_invitations WHERE user_id=?`, userID)
}
func (s *Store) ReleaseInvitationDelivery(ctx context.Context, userID int64) error {
	return s.releaseDelivery(ctx, `UPDATE commerce_invitations SET delivery_status='pending',delivery_claimed_at=NULL WHERE user_id=? AND delivery_status='sending'`, userID)
}
func (s *Store) MarkInvitationSent(ctx context.Context, userID int64) error {
	if userID <= 0 {
		return fmt.Errorf("%w: user id required", ErrInvalidInput)
	}
	result, err := s.db.ExecContext(ctx, `UPDATE commerce_invitations SET delivery_status='sent',delivery_claimed_at=NULL,sent_at=COALESCE(sent_at,?) WHERE user_id=? AND delivery_status IN ('pending','sending','sent')`, nowUnix(s), userID)
	if err != nil {
		return fmt.Errorf("mark invitation sent: %w", err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) ClaimOrderEmail(ctx context.Context, orderID int64) (bool, error) {
	return s.claimDelivery(ctx, `UPDATE commerce_orders SET email_delivery_status='sending',email_delivery_claimed_at=? WHERE id=? AND (email_delivery_status='pending' OR (email_delivery_status='sending' AND (email_delivery_claimed_at IS NULL OR email_delivery_claimed_at<=?)))`, `SELECT email_delivery_status FROM commerce_orders WHERE id=?`, orderID)
}
func (s *Store) ReleaseOrderEmail(ctx context.Context, orderID int64) error {
	return s.releaseDelivery(ctx, `UPDATE commerce_orders SET email_delivery_status='pending',email_delivery_claimed_at=NULL WHERE id=? AND email_delivery_status='sending'`, orderID)
}
func (s *Store) MarkOrderEmailSent(ctx context.Context, orderID int64) error {
	return s.markEmailSent(ctx, `UPDATE commerce_orders SET email_delivery_status='sent',email_delivery_claimed_at=NULL,email_sent_at=COALESCE(email_sent_at,?) WHERE id=? AND email_delivery_status IN ('pending','sending','sent')`, orderID, `UPDATE commerce_booking_confirmations SET email_sent_at=COALESCE(email_sent_at,?) WHERE order_id=?`, orderID)
}
func (s *Store) ClaimSubscriptionEmail(ctx context.Context, subscriptionID int64) (bool, error) {
	return s.claimDelivery(ctx, `UPDATE commerce_subscriptions SET email_delivery_status='sending',email_delivery_claimed_at=? WHERE id=? AND (email_delivery_status='pending' OR (email_delivery_status='sending' AND (email_delivery_claimed_at IS NULL OR email_delivery_claimed_at<=?)))`, `SELECT email_delivery_status FROM commerce_subscriptions WHERE id=?`, subscriptionID)
}
func (s *Store) ReleaseSubscriptionEmail(ctx context.Context, subscriptionID int64) error {
	return s.releaseDelivery(ctx, `UPDATE commerce_subscriptions SET email_delivery_status='pending',email_delivery_claimed_at=NULL WHERE id=? AND email_delivery_status='sending'`, subscriptionID)
}
func (s *Store) MarkSubscriptionEmailSent(ctx context.Context, subscriptionID int64) error {
	return s.markEmailSent(ctx, `UPDATE commerce_subscriptions SET email_delivery_status='sent',email_delivery_claimed_at=NULL,email_sent_at=COALESCE(email_sent_at,?) WHERE id=? AND email_delivery_status IN ('pending','sending','sent')`, subscriptionID, `UPDATE commerce_booking_confirmations SET email_sent_at=COALESCE(email_sent_at,?) WHERE subscription_id=?`, subscriptionID)
}

func (s *Store) claimDelivery(ctx context.Context, query, statusQuery string, id int64) (bool, error) {
	if id <= 0 {
		return false, fmt.Errorf("%w: delivery id required", ErrInvalidInput)
	}
	now := nowUnix(s)
	staleBefore := now - int64(deliveryClaimLease/time.Second)
	result, err := s.db.ExecContext(ctx, query, now, id, staleBefore)
	if err != nil {
		return false, fmt.Errorf("claim delivery: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if n == 1 {
		return true, nil
	}
	var status string
	if err := s.db.QueryRowContext(ctx, statusQuery, id).Scan(&status); err != nil {
		return false, mapNotFound(err)
	}
	if status == "sending" {
		return false, ErrDeliveryInProgress
	}
	return false, nil
}
func (s *Store) releaseDelivery(ctx context.Context, query string, id int64) error {
	if id <= 0 {
		return fmt.Errorf("%w: delivery id required", ErrInvalidInput)
	}
	result, err := s.db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("release delivery: %w", err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
func (s *Store) markEmailSent(ctx context.Context, primary string, id int64, confirmation string, confirmationID int64) error {
	if id <= 0 {
		return fmt.Errorf("%w: delivery id required", ErrInvalidInput)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	now := nowUnix(s)
	result, err := tx.ExecContext(ctx, primary, now, id)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	if _, err = tx.ExecContext(ctx, confirmation, now, confirmationID); err != nil {
		return err
	}
	return tx.Commit()
}
