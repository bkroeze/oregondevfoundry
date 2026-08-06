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
		Email:       "Ada@Example.COM",
		DisplayName: "Ada Lovelace",
		Role:        RoleUser,
		Password:    []byte(storeTestPassword),
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Username != "ada.lovelace" || created.Email != "ada@example.com" || created.CustomerStatus() != StatusNewCustomer || created.ID == 0 {
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
		Email:           "ada.byron@example.com",
		DisplayName:     "Ada Byron",
		Role:            RoleUser,
		HasPurchases:    true,
		Password:        []byte("another secure password"),
		ExpectedVersion: created.Version,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Username != "ada" || updated.Email != "ada.byron@example.com" || updated.DisplayName != "Ada Byron" || updated.CustomerStatus() != StatusCustomer || updated.Version != 2 {
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

func TestUserEmailCRUDValidationAndUniqueness(t *testing.T) {
	store, err := Open(t.TempDir() + "/users.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := t.Context()

	first, err := store.CreateUser(ctx, CreateUserParams{
		Username: "email.one", Email: " First.Person@Example.COM ", DisplayName: "First Person",
		Role: RoleUser, Password: []byte(storeTestPassword),
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.Email != "first.person@example.com" {
		t.Fatalf("normalized email=%q", first.Email)
	}
	loaded, err := store.UserByID(ctx, first.ID)
	if err != nil || loaded.Email != first.Email {
		t.Fatalf("loaded user=%#v error=%v", loaded, err)
	}
	users, err := store.ListUsers(ctx)
	if err != nil || len(users) != 1 || users[0].Email != first.Email {
		t.Fatalf("users=%#v error=%v", users, err)
	}
	if _, err := store.CreateUser(ctx, CreateUserParams{
		Username: "email.two", Email: "FIRST.PERSON@example.com", DisplayName: "Second Person",
		Role: RoleUser, Password: []byte(storeTestPassword),
	}); !errors.Is(err, ErrEmailExists) {
		t.Fatalf("duplicate email error=%v", err)
	}
	if _, err := store.CreateUser(ctx, CreateUserParams{
		Username: "email.bad", Email: "Example Person <person@example.com>", DisplayName: "Bad Email",
		Role: RoleUser, Password: []byte(storeTestPassword),
	}); err == nil {
		t.Fatal("CreateUser accepted a display-name email address")
	}
	empty, err := store.CreateUser(ctx, CreateUserParams{
		Username: "email.empty", DisplayName: "Empty Email", Role: RoleUser, Password: []byte(storeTestPassword),
	})
	if err != nil || empty.Email != "" {
		t.Fatalf("empty email user=%#v error=%v", empty, err)
	}
	if _, err := store.UpdateUser(ctx, empty.ID, UpdateUserParams{
		Username: empty.Username, Email: "FIRST.PERSON@example.com", DisplayName: empty.DisplayName,
		Role: empty.Role, ExpectedVersion: empty.Version,
	}); !errors.Is(err, ErrEmailExists) {
		t.Fatalf("duplicate update email error=%v", err)
	}
	updated, err := store.UpdateUser(ctx, first.ID, UpdateUserParams{
		Username: first.Username, Email: "new@example.com", DisplayName: first.DisplayName,
		Role: first.Role, ExpectedVersion: first.Version,
	})
	if err != nil || updated.Email != "new@example.com" {
		t.Fatalf("updated user=%#v error=%v", updated, err)
	}
	cleared, err := store.UpdateUser(ctx, updated.ID, UpdateUserParams{
		Username: updated.Username, Email: "", DisplayName: updated.DisplayName,
		Role: updated.Role, ExpectedVersion: updated.Version,
	})
	if err != nil || cleared.Email != "" {
		t.Fatalf("cleared user=%#v error=%v", cleared, err)
	}
}

func TestChangePasswordVerificationProtectionAndSessionRevocation(t *testing.T) {
	store, err := Open(t.TempDir() + "/users.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := t.Context()
	oldPassword := []byte(storeTestPassword)
	newPassword := []byte("a different secure password")

	user, err := store.CreateUser(ctx, CreateUserParams{
		Username: "password.user", DisplayName: "Password User", Role: RoleUser, Password: oldPassword,
	})
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := store.CreateSession(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	secondToken, _, err := store.CreateSession(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.VerifyPassword(ctx, user.ID, oldPassword); err != nil {
		t.Fatalf("verify password: %v", err)
	}
	if err := store.VerifyPassword(ctx, user.ID, []byte("wrong password")); !errors.Is(err, ErrInvalidCredential) {
		t.Fatalf("wrong verification error=%v", err)
	}
	if err := store.ChangePassword(ctx, user.ID, []byte("wrong password"), newPassword); !errors.Is(err, ErrInvalidCredential) {
		t.Fatalf("wrong current password error=%v", err)
	}
	if _, err := store.UserBySession(ctx, token); err != nil {
		t.Fatalf("wrong password revoked session: %v", err)
	}
	if err := store.ChangePassword(ctx, user.ID, oldPassword, newPassword); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UserBySession(ctx, token); !errors.Is(err, ErrNotFound) {
		t.Fatalf("changed password session error=%v", err)
	}
	if _, err := store.UserBySession(ctx, secondToken); !errors.Is(err, ErrNotFound) {
		t.Fatalf("changed password second session error=%v", err)
	}
	if err := store.VerifyPassword(ctx, user.ID, oldPassword); !errors.Is(err, ErrInvalidCredential) {
		t.Fatalf("old password verification error=%v", err)
	}
	if err := store.VerifyPassword(ctx, user.ID, newPassword); err != nil {
		t.Fatalf("new password verification: %v", err)
	}

	admin, err := store.CreateUser(ctx, CreateUserParams{
		Username: "password.admin", DisplayName: "Password Admin", Role: RoleAdmin, Password: oldPassword,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ChangePassword(ctx, admin.ID, oldPassword, newPassword); !errors.Is(err, ErrAdminPasswordChange) {
		t.Fatalf("admin self-change error=%v", err)
	}
	if err := store.VerifyPassword(ctx, admin.ID, oldPassword); err != nil {
		t.Fatalf("admin password changed: %v", err)
	}
}

func TestUpdateUserAdminPasswordProtectionIsOptional(t *testing.T) {
	store, err := Open(t.TempDir() + "/users.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := t.Context()
	oldPassword := []byte(storeTestPassword)
	newPassword := []byte("a different secure password")

	admin, err := store.CreateUser(ctx, CreateUserParams{
		Username: "protected.admin", DisplayName: "Protected Admin", Role: RoleAdmin, Password: oldPassword,
	})
	if err != nil {
		t.Fatal(err)
	}
	protected := UpdateUserParams{
		Username: admin.Username, Email: admin.Email, DisplayName: admin.DisplayName, Role: RoleUser,
		Password: newPassword, ProtectAdminPassword: true, ExpectedVersion: admin.Version,
	}
	if _, err := store.UpdateUser(ctx, admin.ID, protected); !errors.Is(err, ErrAdminPasswordChange) {
		t.Fatalf("protected current admin update error=%v", err)
	}
	current, err := store.UserByID(ctx, admin.ID)
	if err != nil || current.Role != RoleAdmin || current.Version != admin.Version {
		t.Fatalf("protected update mutated admin=%#v error=%v", current, err)
	}
	protected.ProtectAdminPassword = false
	protected.Role = RoleAdmin
	updated, err := store.UpdateUser(ctx, admin.ID, protected)
	if err != nil {
		t.Fatalf("CLI-compatible admin password update: %v", err)
	}
	if updated.Version != admin.Version+1 {
		t.Fatalf("updated admin version=%d", updated.Version)
	}
	if err := store.VerifyPassword(ctx, admin.ID, newPassword); err != nil {
		t.Fatalf("unprotected admin password update: %v", err)
	}

	user, err := store.CreateUser(ctx, CreateUserParams{
		Username: "promoted.user", DisplayName: "Promoted User", Role: RoleUser, Password: oldPassword,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateUser(ctx, user.ID, UpdateUserParams{
		Username: user.Username, Email: user.Email, DisplayName: user.DisplayName, Role: RoleAdmin,
		Password: newPassword, ProtectAdminPassword: true, ExpectedVersion: user.Version,
	}); !errors.Is(err, ErrAdminPasswordChange) {
		t.Fatalf("protected resulting admin update error=%v", err)
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
INSERT INTO users (id, username, display_name, role, created_at, updated_at) VALUES
    (1, 'Legacy.Person@Example.COM', 'Legacy Email User', 'user', 1, 1),
    (2, 'legacy.user', 'Legacy User', 'user', 1, 1);
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
	if err := store.migrate(t.Context()); err != nil {
		t.Fatalf("repeat migration: %v", err)
	}
	var emailNotNull int
	var emailDefault string
	if err := store.db.QueryRow(`SELECT [notnull], dflt_value FROM pragma_table_info('users') WHERE name = 'email'`).Scan(&emailNotNull, &emailDefault); err != nil {
		t.Fatal(err)
	}
	if emailNotNull != 1 || emailDefault != "''" {
		t.Fatalf("email schema notnull=%d default=%q", emailNotNull, emailDefault)
	}
	var version int64
	var email string
	if err := store.db.QueryRow(`SELECT version, email FROM users WHERE id = 1`).Scan(&version, &email); err != nil {
		t.Fatal(err)
	}
	if version != 1 || email != "legacy.person@example.com" {
		t.Fatalf("legacy email user version=%d email=%q", version, email)
	}
	if _, err := store.db.Exec(`UPDATE users SET email = '' WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	if err := store.migrate(t.Context()); err != nil {
		t.Fatalf("repeat email backfill: %v", err)
	}
	if err := store.db.QueryRow(`SELECT email FROM users WHERE id = 1`).Scan(&email); err != nil || email != "legacy.person@example.com" {
		t.Fatalf("repeated backfill email=%q error=%v", email, err)
	}
	if _, err := store.CreateUser(t.Context(), CreateUserParams{
		Username: "new.user", Email: "LEGACY.PERSON@example.com", DisplayName: "New User",
		Role: RoleUser, Password: []byte(storeTestPassword),
	}); !errors.Is(err, ErrEmailExists) {
		t.Fatalf("migrated email uniqueness error=%v", err)
	}
	legacy, err := store.UserByID(t.Context(), 2)
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
