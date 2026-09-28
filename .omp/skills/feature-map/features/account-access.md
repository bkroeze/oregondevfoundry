# Account access

Users authenticate with username and password, receive an opaque session, navigate role-aware profile pages, and can log out without exposing credential or session state.

## User entry points

- `GET /login` and `POST /login` for authentication.
- `POST /logout` for session termination.
- `GET /profile` for any authenticated user, `GET /client` for clients, and `GET /admin` for administrators.

## Primary source path

- **Entry:** `internal/web/handler.go::Handler.login` — applies body, CSRF, rate-limit, redirect, and cookie behavior around authentication.
- **Orchestrator:** `internal/auth/store.go::Store.Authenticate` — normalizes the username and verifies the stored bcrypt credential.
- **Effect boundary:** `internal/auth/store.go::Store.CreateSession` — hashes and persists the token identity and expiry, then returns the raw opaque token for the session cookie.

## Variants and gates

- Login requires a valid CSRF cookie/form token pair and is bounded by the per-client and per-account `loginLimiter`.
- The optional `next` destination is accepted only through `safeNext`; the default success destination is `/profile`.
- `/client` requires the client role and `/admin` requires the admin role; unauthorized authenticated users receive access denied.
- Logout requires CSRF, deletes the stored session, and expires both the session and CSRF cookies.
- Password change, account deletion, invitations, admin user management, and the `users` CLI are adjacent but intentionally outside this initial entry.

## Effects and invariants

- Successful login creates a 24-hour database session and an HTTP-only, SameSite=Lax session cookie; the Secure attribute follows configuration.
- Failed authentication does not create a session and contributes to both applicable limiter keys.
- Role-specific navigation and status derive from the authenticated user returned by the session lookup.
- Logout invalidates the server-side session before clearing browser cookies.

## Verification

- **Focused check:** `go test ./internal/web -run '^(TestAuthenticationRoleNavigationAndAuthorization|TestLoginFailureCSRFAndSecureSessionCookie|TestLogoutClearsSessionAndProtectedRoutesRedirect)$'`
- **Runtime path:** with a disposable database user, submit `/login`, visit the role-appropriate profile, submit `/logout`, then revisit `/profile`.
- **Evidence:** login sets the secure session cookie and reaches the expected profile; logout clears it and the protected route redirects to `/login?next=/profile`.

## Gotchas

- Do not use `cmd/users` as the implementation path for web login; it is an operator CRUD surface.
- A visually correct profile page is not proof of authorization; check the forbidden role path too.
- Successful login does not clear failures accumulated for other client/account limiter keys.
