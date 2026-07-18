package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"
)

const (
	RoleUser   Role = "user"
	RoleClient Role = "client"
	RoleAdmin  Role = "admin"

	StatusVisitor     Status = "Visitor"
	StatusNewCustomer Status = "New Customer"
	StatusCustomer    Status = "Customer"
	StatusClient      Status = "Client"
	StatusAdmin       Status = "Admin"

	PasswordProvider = "password"

	minimumPasswordLength = 12
	passwordCost          = 12
	sessionLifetime       = 24 * time.Hour
)

var (
	ErrNotFound          = errors.New("user not found")
	ErrInvalidCredential = errors.New("invalid username or password")
	ErrUsernameExists    = errors.New("username already exists")
	ErrConcurrentUpdate  = errors.New("user was updated by another process")
	usernamePattern      = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{2,63}$`)
)

type Role string

type Status string

type User struct {
	ID           int64
	Username     string
	DisplayName  string
	Role         Role
	HasPurchases bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
	Version      int64
}

func (u User) CustomerStatus() Status {
	switch u.Role {
	case RoleAdmin:
		return StatusAdmin
	case RoleClient:
		return StatusClient
	case RoleUser:
		if u.HasPurchases {
			return StatusCustomer
		}
		return StatusNewCustomer
	default:
		return StatusVisitor
	}
}

func ValidRole(role Role) bool {
	return role == RoleUser || role == RoleClient || role == RoleAdmin
}

type CreateUserParams struct {
	Username     string
	DisplayName  string
	Role         Role
	HasPurchases bool
	Password     []byte
}

type UpdateUserParams struct {
	Username        string
	DisplayName     string
	Role            Role
	HasPurchases    bool
	Password        []byte
	ExpectedVersion int64
}

type Store struct {
	db  *sql.DB
	now func() time.Time
}

func Open(path string) (*Store, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("database path is required")
	}
	if path != ":memory:" && !strings.HasPrefix(path, "file:") {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, fmt.Errorf("create database directory: %w", err)
		}
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	db.SetMaxOpenConns(1)
	store := &Store{db: db, now: time.Now}
	if err := store.migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate(ctx context.Context) error {
	const schema = `
PRAGMA foreign_keys = ON;
PRAGMA busy_timeout = 5000;
PRAGMA journal_mode = WAL;
CREATE TABLE IF NOT EXISTS users (
    id INTEGER PRIMARY KEY,
    username TEXT NOT NULL COLLATE NOCASE UNIQUE,
    display_name TEXT NOT NULL,
    role TEXT NOT NULL CHECK (role IN ('user', 'client', 'admin')),
    has_purchases INTEGER NOT NULL DEFAULT 0 CHECK (has_purchases IN (0, 1)),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    version INTEGER NOT NULL DEFAULT 1
);
CREATE TABLE IF NOT EXISTS auth_identities (
    id INTEGER PRIMARY KEY,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider TEXT NOT NULL,
    subject TEXT NOT NULL,
    credential_hash TEXT,
    UNIQUE(provider, subject)
);
CREATE INDEX IF NOT EXISTS auth_identities_user_id ON auth_identities(user_id);
CREATE TABLE IF NOT EXISTS sessions (
    token_hash BLOB PRIMARY KEY,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at INTEGER NOT NULL,
    created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS sessions_user_id ON sessions(user_id);
CREATE INDEX IF NOT EXISTS sessions_expires_at ON sessions(expires_at);
`
	if _, err := s.db.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("migrate database: %w", err)
	}
	if err := s.ensureUserVersion(ctx); err != nil {
		return err
	}
	return nil
}

func (s *Store) ensureUserVersion(ctx context.Context) error {
	found, err := s.userVersionExists(ctx)
	if err != nil {
		return err
	}
	if found {
		return nil
	}
	if _, err := s.db.ExecContext(ctx, `ALTER TABLE users ADD COLUMN version INTEGER NOT NULL DEFAULT 1`); err != nil {
		// Another process may have completed the same migration after our inspection.
		found, inspectErr := s.userVersionExists(ctx)
		if inspectErr == nil && found {
			return nil
		}
		return fmt.Errorf("add users version: %w", err)
	}
	return nil
}

func (s *Store) userVersionExists(ctx context.Context) (bool, error) {
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(users)`)
	if err != nil {
		return false, fmt.Errorf("inspect users schema: %w", err)
	}
	found := false
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			_ = rows.Close()
			return false, fmt.Errorf("inspect users column: %w", err)
		}
		if name == "version" {
			found = true
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return false, fmt.Errorf("inspect users schema rows: %w", err)
	}
	if err := rows.Close(); err != nil {
		return false, fmt.Errorf("close users schema rows: %w", err)
	}
	return found, nil
}

func (s *Store) CreateUser(ctx context.Context, params CreateUserParams) (User, error) {
	params.Username = normalizeUsername(params.Username)
	params.DisplayName = strings.TrimSpace(params.DisplayName)
	if err := validateUser(params.Username, params.DisplayName, params.Role); err != nil {
		return User{}, err
	}
	hash, err := hashPassword(params.Password)
	if err != nil {
		return User{}, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, fmt.Errorf("begin create user: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	now := s.now().UTC().Truncate(time.Second)
	result, err := tx.ExecContext(ctx, `INSERT INTO users (username, display_name, role, has_purchases, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`, params.Username, params.DisplayName, params.Role, params.HasPurchases, now.Unix(), now.Unix())
	if isUniqueConstraint(err) {
		return User{}, ErrUsernameExists
	}
	if err != nil {
		return User{}, fmt.Errorf("insert user: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return User{}, fmt.Errorf("read user id: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO auth_identities (user_id, provider, subject, credential_hash) VALUES (?, ?, ?, ?)`, id, PasswordProvider, params.Username, string(hash)); err != nil {
		return User{}, fmt.Errorf("insert password identity: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return User{}, fmt.Errorf("commit create user: %w", err)
	}
	return User{ID: id, Username: params.Username, DisplayName: params.DisplayName, Role: params.Role, HasPurchases: params.HasPurchases, CreatedAt: now, UpdatedAt: now, Version: 1}, nil
}

func (s *Store) Authenticate(ctx context.Context, username string, password []byte) (User, error) {
	username = normalizeUsername(username)
	var user User
	var hash string
	var createdAt, updatedAt int64
	err := s.db.QueryRowContext(ctx, `
SELECT u.id, u.username, u.display_name, u.role, u.has_purchases, u.created_at, u.updated_at, u.version, i.credential_hash
FROM auth_identities i
JOIN users u ON u.id = i.user_id
WHERE i.provider = ? AND i.subject = ?`, PasswordProvider, username).Scan(&user.ID, &user.Username, &user.DisplayName, &user.Role, &user.HasPurchases, &createdAt, &updatedAt, &user.Version, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		// Keep the missing-user path computationally similar to a wrong password.
		_ = bcrypt.CompareHashAndPassword([]byte("$2a$12$3V5Y7fMrWZ9rrJzY.5HfSe6Wf6x0PptuYLM2lGXB7k4f7w2bxAxeW"), password)
		return User{}, ErrInvalidCredential
	}
	if err != nil {
		return User{}, fmt.Errorf("load password identity: %w", err)
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), password) != nil {
		return User{}, ErrInvalidCredential
	}
	user.CreatedAt = time.Unix(createdAt, 0).UTC()
	user.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return user, nil
}

func (s *Store) UserByID(ctx context.Context, id int64) (User, error) {
	return s.queryUser(ctx, `SELECT id, username, display_name, role, has_purchases, created_at, updated_at, version FROM users WHERE id = ?`, id)
}

func (s *Store) UserByUsername(ctx context.Context, username string) (User, error) {
	return s.queryUser(ctx, `SELECT id, username, display_name, role, has_purchases, created_at, updated_at, version FROM users WHERE username = ?`, normalizeUsername(username))
}

func (s *Store) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, username, display_name, role, has_purchases, created_at, updated_at, version FROM users ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()

	users := make([]User, 0)
	for rows.Next() {
		user, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, user)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	return users, nil
}

func (s *Store) UpdateUser(ctx context.Context, id int64, params UpdateUserParams) (User, error) {
	params.Username = normalizeUsername(params.Username)
	params.DisplayName = strings.TrimSpace(params.DisplayName)
	if err := validateUser(params.Username, params.DisplayName, params.Role); err != nil {
		return User{}, err
	}
	if params.ExpectedVersion < 1 {
		return User{}, errors.New("expected user version is required")
	}
	var hash []byte
	var err error
	if len(params.Password) > 0 {
		hash, err = hashPassword(params.Password)
		if err != nil {
			return User{}, err
		}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, fmt.Errorf("begin update user: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	now := s.now().UTC().Truncate(time.Second)
	result, err := tx.ExecContext(ctx, `UPDATE users SET username = ?, display_name = ?, role = ?, has_purchases = ?, updated_at = ?, version = version + 1 WHERE id = ? AND version = ?`, params.Username, params.DisplayName, params.Role, params.HasPurchases, now.Unix(), id, params.ExpectedVersion)
	if isUniqueConstraint(err) {
		return User{}, ErrUsernameExists
	}
	if err != nil {
		return User{}, fmt.Errorf("update user: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return User{}, fmt.Errorf("read updated rows: %w", err)
	}
	if changed == 0 {
		var exists int
		if err := tx.QueryRowContext(ctx, `SELECT 1 FROM users WHERE id = ?`, id).Scan(&exists); errors.Is(err, sql.ErrNoRows) {
			return User{}, ErrNotFound
		} else if err != nil {
			return User{}, fmt.Errorf("check updated user: %w", err)
		}
		return User{}, ErrConcurrentUpdate
	}
	if len(hash) > 0 {
		result, err := tx.ExecContext(ctx, `UPDATE auth_identities SET subject = ?, credential_hash = ? WHERE user_id = ? AND provider = ?`, params.Username, string(hash), id, PasswordProvider)
		if err != nil {
			return User{}, fmt.Errorf("update password identity: %w", err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return User{}, fmt.Errorf("read updated password identities: %w", err)
		}
		if changed == 0 {
			if _, err := tx.ExecContext(ctx, `INSERT INTO auth_identities (user_id, provider, subject, credential_hash) VALUES (?, ?, ?, ?)`, id, PasswordProvider, params.Username, string(hash)); err != nil {
				return User{}, fmt.Errorf("insert password identity: %w", err)
			}
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, id); err != nil {
			return User{}, fmt.Errorf("revoke user sessions: %w", err)
		}
	} else {
		if _, err := tx.ExecContext(ctx, `UPDATE auth_identities SET subject = ? WHERE user_id = ? AND provider = ?`, params.Username, id, PasswordProvider); err != nil {
			return User{}, fmt.Errorf("update password identity: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return User{}, fmt.Errorf("commit update user: %w", err)
	}
	return s.UserByID(ctx, id)
}

func (s *Store) DeleteUser(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete user: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read deleted rows: %w", err)
	}
	if changed == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) CreateSession(ctx context.Context, userID int64) (string, time.Time, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", time.Time{}, fmt.Errorf("generate session token: %w", err)
	}
	token := fmt.Sprintf("%x", raw)
	hash := sha256.Sum256([]byte(token))
	now := s.now().UTC()
	expires := now.Add(sessionLifetime)
	if _, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, now.Unix()); err != nil {
		return "", time.Time{}, fmt.Errorf("delete expired sessions: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO sessions (token_hash, user_id, expires_at, created_at) VALUES (?, ?, ?, ?)`, hash[:], userID, expires.Unix(), now.Unix()); err != nil {
		return "", time.Time{}, fmt.Errorf("create session: %w", err)
	}
	return token, expires, nil
}

func (s *Store) UserBySession(ctx context.Context, token string) (User, error) {
	if len(token) != 64 {
		return User{}, ErrNotFound
	}
	hash := sha256.Sum256([]byte(token))
	now := s.now().UTC().Unix()
	user, err := s.queryUser(ctx, `
SELECT u.id, u.username, u.display_name, u.role, u.has_purchases, u.created_at, u.updated_at, u.version
FROM sessions s
JOIN users u ON u.id = s.user_id
WHERE s.token_hash = ? AND s.expires_at > ?`, hash[:], now)
	if err == nil {
		return user, nil
	}
	return User{}, err
}

func (s *Store) DeleteSession(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	hash := sha256.Sum256([]byte(token))
	if _, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, hash[:]); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

func (s *Store) queryUser(ctx context.Context, query string, args ...any) (User, error) {
	user, err := scanUser(s.db.QueryRowContext(ctx, query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return user, err
}

type scanner interface {
	Scan(...any) error
}

func scanUser(row scanner) (User, error) {
	var user User
	var createdAt, updatedAt int64
	if err := row.Scan(&user.ID, &user.Username, &user.DisplayName, &user.Role, &user.HasPurchases, &createdAt, &updatedAt, &user.Version); err != nil {
		return User{}, err
	}
	user.CreatedAt = time.Unix(createdAt, 0).UTC()
	user.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return user, nil
}

func validateUser(username, displayName string, role Role) error {
	if !usernamePattern.MatchString(username) {
		return errors.New("username must be 3-64 characters using letters, numbers, dots, underscores, or hyphens")
	}
	if displayName == "" || len(displayName) > 120 {
		return errors.New("display name must be 1-120 characters")
	}
	if !ValidRole(role) {
		return errors.New("role must be user, client, or admin")
	}
	return nil
}

func hashPassword(password []byte) ([]byte, error) {
	if len(password) < minimumPasswordLength {
		return nil, fmt.Errorf("password must be at least %d characters", minimumPasswordLength)
	}
	if len(password) > 72 {
		return nil, errors.New("password must be at most 72 bytes")
	}
	hash, err := bcrypt.GenerateFromPassword(password, passwordCost)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}
	return hash, nil
}

func normalizeUsername(username string) string {
	return strings.ToLower(strings.TrimSpace(username))
}

func isUniqueConstraint(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}
