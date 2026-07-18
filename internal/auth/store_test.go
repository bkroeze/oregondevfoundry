package auth

import (
	"database/sql"
	"errors"
	"testing"
	"time"
)

const storeTestPassword = "correct horse battery staple"

func TestCustomerStatusDefinitions(t *testing.T) {
	for _, test := range []struct {
		user User
		want Status
	}{
		{User{}, StatusVisitor},
		{User{Role: RoleUser}, StatusNewCustomer},
		{User{Role: RoleUser, HasPurchases: true}, StatusCustomer},
		{User{Role: RoleClient}, StatusClient},
		{User{Role: RoleAdmin}, StatusAdmin},
	} {
		if got := test.user.CustomerStatus(); got != test.want {
			t.Fatalf("CustomerStatus(%#v)=%q, want %q", test.user, got, test.want)
		}
	}
}

func TestOpenConfiguresEveryConnection(t *testing.T) {
	store, err := Open(t.TempDir() + "/users.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	store.db.SetMaxIdleConns(0)

	var foreignKeys int
	if err := store.db.QueryRow(`PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
		t.Fatal(err)
	}
	if foreignKeys != 1 {
		t.Fatalf("foreign_keys=%d", foreignKeys)
	}

	var busyTimeout int
	if err := store.db.QueryRow(`PRAGMA busy_timeout`).Scan(&busyTimeout); err != nil {
		t.Fatal(err)
	}
	if busyTimeout != 5000 {
		t.Fatalf("busy_timeout=%d", busyTimeout)
	}
}

func TestUserCRUDCredentialsAndSessions(t *testing.T) {
	store, err := Open(t.TempDir() + "/users.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	fixedNow := time.Now().UTC().Truncate(time.Second)
	store.now = func() time.Time { return fixedNow }
	ctx := t.Context()

	created, err := store.CreateUser(ctx, CreateUserParams{
		Username:    "Ada.Lovelace",
		DisplayName: "Ada Lovelace",
		Role:        RoleUser,
		Password:    []byte(storeTestPassword),
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Username != "ada.lovelace" || created.CustomerStatus() != StatusNewCustomer || created.ID == 0 {
		t.Fatalf("unexpected created user: %#v", created)
	}
	if _, err := store.CreateUser(ctx, CreateUserParams{Username: "ADA.LOVELACE", DisplayName: "Duplicate", Role: RoleUser, Password: []byte(storeTestPassword)}); !errors.Is(err, ErrUsernameExists) {
		t.Fatalf("duplicate username error=%v", err)
	}

	authenticated, err := store.Authenticate(ctx, "ADA.LOVELACE", []byte(storeTestPassword))
	if err != nil || authenticated.ID != created.ID {
		t.Fatalf("authenticate user=%#v error=%v", authenticated, err)
	}
	if _, err := store.Authenticate(ctx, created.Username, []byte("not the password")); !errors.Is(err, ErrInvalidCredential) {
		t.Fatalf("wrong password error=%v", err)
	}

	token, expires, err := store.CreateSession(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(token) != 64 || !expires.After(created.CreatedAt) {
		t.Fatalf("invalid session token length=%d expires=%s", len(token), expires)
	}
	fromSession, err := store.UserBySession(ctx, token)
	if err != nil || fromSession.ID != created.ID {
		t.Fatalf("session user=%#v error=%v", fromSession, err)
	}

	updated, err := store.UpdateUser(ctx, created.ID, UpdateUserParams{
		Username:        "ada",
		DisplayName:     "Ada Byron",
		Role:            RoleUser,
		HasPurchases:    true,
		Password:        []byte("another secure password"),
		ExpectedVersion: created.Version,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Username != "ada" || updated.DisplayName != "Ada Byron" || updated.CustomerStatus() != StatusCustomer || updated.Version != 2 {
		t.Fatalf("unexpected updated user: %#v", updated)
	}
	if !updated.UpdatedAt.Equal(created.UpdatedAt) {
		t.Fatalf("rapid update changed wall-clock time from %s to %s", created.UpdatedAt, updated.UpdatedAt)
	}
	if _, err := store.UpdateUser(ctx, created.ID, UpdateUserParams{Username: updated.Username, DisplayName: updated.DisplayName, Role: updated.Role, HasPurchases: updated.HasPurchases, ExpectedVersion: created.Version}); !errors.Is(err, ErrConcurrentUpdate) {
		t.Fatalf("stale update error=%v", err)
	}
	if _, err := store.UserBySession(ctx, token); !errors.Is(err, ErrNotFound) {
		t.Fatalf("password update did not revoke session: %v", err)
	}
	if _, err := store.Authenticate(ctx, "ada", []byte(storeTestPassword)); !errors.Is(err, ErrInvalidCredential) {
		t.Fatalf("old password still authenticates: %v", err)
	}
	if _, err := store.Authenticate(ctx, "ada", []byte("another secure password")); err != nil {
		t.Fatalf("new password error=%v", err)
	}

	users, err := store.ListUsers(ctx)
	if err != nil || len(users) != 1 || users[0].ID != created.ID {
		t.Fatalf("users=%#v error=%v", users, err)
	}
	if err := store.DeleteUser(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UserByID(ctx, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted user error=%v", err)
	}
}

func TestUserValidationRejectsWeakCredentialsAndUnknownRoles(t *testing.T) {
	store, err := Open(t.TempDir() + "/users.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	for _, test := range []CreateUserParams{
		{Username: "ab", DisplayName: "Short", Role: RoleUser, Password: []byte(storeTestPassword)},
		{Username: "valid.user", DisplayName: "Valid", Role: Role("owner"), Password: []byte(storeTestPassword)},
		{Username: "valid.user", DisplayName: "Valid", Role: RoleUser, Password: []byte("too short")},
	} {
		if _, err := store.CreateUser(t.Context(), test); err == nil {
			t.Fatalf("CreateUser(%#v) succeeded", test)
		}
	}
}

func TestOpenMigratesExistingUsersWithVersions(t *testing.T) {
	database := t.TempDir() + "/legacy.db"
	db, err := sql.Open("sqlite", database)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
CREATE TABLE users (
    id INTEGER PRIMARY KEY,
    username TEXT NOT NULL COLLATE NOCASE UNIQUE,
    display_name TEXT NOT NULL,
    role TEXT NOT NULL CHECK (role IN ('user', 'client', 'admin')),
    has_purchases INTEGER NOT NULL DEFAULT 0 CHECK (has_purchases IN (0, 1)),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
INSERT INTO users (id, username, display_name, role, created_at, updated_at) VALUES (1, 'legacy.user', 'Legacy User', 'user', 1, 1);
`)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	results := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			store, err := Open(database)
			if err == nil {
				err = store.Close()
			}
			results <- err
		}()
	}
	close(start)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatalf("concurrent migration: %v", err)
		}
	}

	store, err := Open(database)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var version int64
	if err := store.db.QueryRow(`SELECT version FROM users WHERE id = 1`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 1 {
		t.Fatalf("legacy user version=%d", version)
	}
	legacy, err := store.UserByID(t.Context(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateUser(t.Context(), legacy.ID, UpdateUserParams{
		Username:        legacy.Username,
		DisplayName:     legacy.DisplayName,
		Role:            legacy.Role,
		Password:        []byte(storeTestPassword),
		ExpectedVersion: legacy.Version,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Authenticate(t.Context(), legacy.Username, []byte(storeTestPassword)); err != nil {
		t.Fatalf("authenticate migrated user: %v", err)
	}
}
