package commerce

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/mail"
	"strings"
	"time"
)

type Store struct {
	db  *sql.DB
	now func() time.Time
}

func Open(db *sql.DB) (*Store, error) { return New(db) }

func New(db *sql.DB) (*Store, error) {
	if db == nil {
		return nil, fmt.Errorf("%w: database is required", ErrInvalidInput)
	}
	s := &Store{db: db, now: time.Now}
	if _, err := db.ExecContext(context.Background(), `PRAGMA foreign_keys = ON`); err != nil {
		return nil, fmt.Errorf("enable foreign keys: %w", err)
	}
	if err := s.migrate(context.Background()); err != nil {
		return nil, err
	}
	if err := s.CancelStalePendingCheckouts(context.Background(), unattachedCheckoutLease); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) DB() *sql.DB { return s.db }

func validProductType(t ProductType) bool {
	return t == ProductTypeOneTime || t == ProductTypeSubscription
}

func validSKUKind(k SKUKind) bool { return k == SKUKindPrimary || k == SKUKindAddon }

func nowUnix(s *Store) int64 { return s.now().UTC().Truncate(time.Second).Unix() }

func randomToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate random token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

func tokenHash(token string) [32]byte { return sha256.Sum256([]byte(strings.TrimSpace(token))) }

func canonicalOptions(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "{}", nil
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return "", fmt.Errorf("%w: options must be valid JSON", ErrInvalidInput)
	}
	if _, ok := value.(map[string]any); !ok {
		return "", fmt.Errorf("%w: options must be a JSON object", ErrInvalidInput)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("%w: options must contain one JSON value", ErrInvalidInput)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode options: %w", err)
	}
	return string(encoded), nil
}

func validateEmail(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	address, err := mail.ParseAddress(value)
	if err != nil || !strings.EqualFold(address.Address, value) || !strings.Contains(value, "@") || len(value) > 254 {
		return "", fmt.Errorf("%w: valid buyer email is required", ErrInvalidInput)
	}
	return value, nil
}

func mapNotFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

func nullableInt64(v *int64) any {
	if v == nil {
		return nil
	}
	return *v
}

func ptrInt64(v sql.NullInt64) *int64 {
	if !v.Valid {
		return nil
	}
	value := v.Int64
	return &value
}

func ptrTime(v sql.NullInt64) *time.Time {
	if !v.Valid {
		return nil
	}
	value := time.Unix(v.Int64, 0).UTC()
	return &value
}

type scanner interface{ Scan(...any) error }
