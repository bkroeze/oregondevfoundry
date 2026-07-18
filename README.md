# Oregon Dev Foundry

The standalone Go web application for [oregondevfoundry.com](https://oregondevfoundry.com). It serves the public site, username/password accounts with role-aware profile pages, and the contact inquiry flow.

## Architecture

- `cmd/server`: process startup, validated configuration, HTTP timeouts, signal handling, and graceful shutdown.
- `cmd/users`: non-interactive user CRUD with password input restricted to standard input.
- `internal/auth`: SQLite users, provider-neutral identities, bcrypt credentials, opaque sessions, roles, and customer-status derivation.
- `internal/templates`: [templ](https://templ.guide/) source and generated Go for public and account pages.
- `internal/web`: routes, authentication and authorization boundaries, CSRF protection, secure cookies, embedded assets, request limits, and HTMX fallback.
- `internal/contact`: input validation, Cloudflare Turnstile verification, a testable `Sender` interface, and the production [`mailgun-go/v4`](https://github.com/mailgun/mailgun-go) implementation.
- `internal/config`: environment loading, defaults, required-value checks, port bounds, and Mailgun-region validation.

The contact form is an ordinary HTML `POST` form first. With JavaScript available, HTMX swaps only the rendered form fragment and preserves the visual interaction. Without HTMX, the server returns the complete page with the same validation or delivery status, so the form remains accessible. Tests inject fake verification and mail implementations and never contact Mailgun or send real email.

## Requirements

- Go 1.24 or newer
- `just` (optional, but recommended)
- Docker (optional, for container builds)

The templ CLI is invoked through `go run` at a pinned version, so it does not need to be installed globally.

## Configuration

Copy the example and replace every placeholder:

```sh
cp .env.example .env
$EDITOR .env
```

| Variable | Required | Description |
| --- | --- | --- |
| `PORT` | No | HTTP port, default `8080`; must be from 1 through 65535. |
| `MAILGUN_API_KEY` | Yes | Private Mailgun API key. |
| `MAILGUN_DOMAIN` | Yes | Verified sending domain, such as `mg.oregondevfoundry.com`. |
| `MAILGUN_REGION` | No | `us` (default) or `eu`. |
| `CONTACT_TO` | Yes | Destination mailbox. Sandbox domains must authorize it. |
| `CONTACT_FROM` | Yes | Verified sender, including an optional display name. |
| `TURNSTILE_SITE_KEY` | Yes | Public Cloudflare Turnstile widget key. |
| `TURNSTILE_SECRET_KEY` | Yes | Private Turnstile verification key. |
| `IMAGE` | No | Image tag used by the Docker recipes. |
| `DATABASE_PATH` | No | SQLite path, default `data/oregon-dev-foundry.db`; the parent directory is created with user-only permissions. |
| `SESSION_COOKIE_SECURE` | No | `true` by default. Set `false` only for local plain-HTTP development. |

Mailgun and Turnstile secrets remain server-side. Startup fails with a clear error if required values are absent or if `PORT` or `MAILGUN_REGION` is invalid.

## Development

```sh
just generate  # regenerate Go sources from all templ files
just dev       # watch templ files and restart through templ's development proxy
just run       # generate and run directly
```

The service logs its listen address. Stop it with `Ctrl-C`; `SIGINT` and `SIGTERM` trigger a bounded graceful shutdown.

### User CRUD

The `users` recipe supports `list`, `show`, `create`, `update`, and `delete`. Passwords are never accepted in command arguments. Pipe them over standard input:

```sh
read -rsp "Password: " PASSWORD; printf '\n'
printf '%s\n' "$PASSWORD" | just users create ada "Ada Lovelace" user false
unset PASSWORD

just users list
just users show ada
just users update ada "Ada Lovelace" client false
just users delete ada
```

The final `true`/`false` value on `create` and `update` is the future purchase-history marker used only to distinguish `New Customer` from `Customer`; no commerce behavior exists in this round. Set `DATABASE_PATH` to manage a non-default database.

### Quality commands

```sh
just fmt        # templ fmt and gofmt
just test       # go test -race ./...
just lint       # go vet ./...
just build      # production-style static Go binary in bin/
just check      # generation consistency, format, tests, vet, and build
```

`bin/` is ignored. Generated `*_templ.go` files are source artifacts and should remain synchronized with their `.templ` inputs.

## HTTP behavior

| Method and path | Purpose |
| --- | --- |
| `GET /` | Render the site. |
| `GET /login` | Render username/password login and Visitor status. |
| `POST /login` | Validate CSRF and credentials, then start a session. |
| `POST /logout` | Validate CSRF and end the current session. |
| `GET /profile` | Authenticated user profile and applicable customer status. |
| `GET /client` | Client-only landing page. |
| `GET /admin` | Admin-only landing page. |
| `GET /styles.css` | Embedded stylesheet. |
| `GET /script.js` | Embedded navigation/year/HTMX integration script. |
| `GET /api/contact-config` | Return the public Turnstile site key. |
| `POST /api/contact` | Validate Turnstile and send the inquiry through Mailgun. |
| `GET /healthz` | Container health probe; returns `ok`. |
| `GET /up` | Compatibility health alias; returns `ok`. |

Contact requests are limited to 16 KiB. User fields have server-side length and email checks, the honeypot suppresses automated sends, provider operations are bounded, and provider details are logged rather than exposed in HTTP responses. A direct `mailto:` link remains visible if the form or an external provider is unavailable.

## Container deployment

The Dockerfile uses a Go build stage and a small Alpine runtime containing CA certificates, the server, and the user-administration binary. The binaries run under an unprivileged numeric UID. Static assets and templates are embedded in the server. SQLite data lives under `/data`, which must be mounted persistently. The image exposes port `8080` and includes a `/healthz` health check.

```sh
just docker-build

# Before the first server start, create the initial administrator in the same volume.
read -rsp "Initial admin password: " PASSWORD; printf '\n'
printf '%s\n' "$PASSWORD" | docker run --rm -i \
  --entrypoint /usr/local/bin/users \
  -v odf-data:/data ghcr.io/bkroeze/oregon-dev-foundry:latest \
  create --username admin --display-name "Administrator" --role admin --password-stdin
unset PASSWORD

docker run --rm --env-file .env -p 8080:8080 \
  -v odf-data:/data ghcr.io/bkroeze/oregon-dev-foundry:latest

# Set IMAGE to the intended registry/repository before publishing.
IMAGE=ghcr.io/bkroeze/oregon-dev-foundry:latest just docker-push
```

Do not bake `.env` into an image or commit it. Configure production secrets through the deployment platform.
