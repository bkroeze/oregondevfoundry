# Oregon Dev Foundry

The standalone Go web application for [oregondevfoundry.com](https://oregondevfoundry.com). It preserves the original site at `/`, serves embedded CSS and JavaScript at `/styles.css` and `/script.js`, and accepts contact inquiries at `POST /api/contact`.

## Architecture

- `cmd/server`: process startup, validated configuration, HTTP timeouts, signal handling, and graceful shutdown.
- `internal/templates`: [templ](https://templ.guide/) source and generated Go for the page and contact-form fragment.
- `internal/web`: routes, embedded static assets, request limits, security headers, HTMX responses, and full-page form fallback.
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

Mailgun and Turnstile secrets remain server-side. Startup fails with a clear error if required values are absent or if `PORT` or `MAILGUN_REGION` is invalid.

## Development

```sh
just generate  # regenerate internal/templates/page_templ.go
just dev       # watch templ files and restart through templ's development proxy
just run       # generate and run directly
```

The service logs its listen address. Stop it with `Ctrl-C`; `SIGINT` and `SIGTERM` trigger a bounded graceful shutdown.

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
| `GET /styles.css` | Embedded stylesheet. |
| `GET /script.js` | Embedded navigation/year/HTMX integration script. |
| `POST /api/contact` | Validate Turnstile and send the inquiry through Mailgun. |
| `GET /healthz` | Container health probe; returns `ok`. |
| `GET /up` | Compatibility health alias; returns `ok`. |

Contact requests are limited to 16 KiB. User fields have server-side length and email checks, the honeypot suppresses automated sends, provider operations are bounded, and provider details are logged rather than exposed in HTTP responses. A direct `mailto:` link remains visible if the form or an external provider is unavailable.

## Container deployment

The Dockerfile uses a Go build stage and a small Alpine runtime containing only CA certificates and the compiled binary, which runs under an unprivileged numeric UID. Static assets and templates are embedded in that binary. The image exposes port `8080` and includes a `/healthz` health check.

```sh
just docker-build

docker run --rm --env-file .env -p 8080:8080 \
  ghcr.io/bkroeze/oregon-dev-foundry:latest

# Set IMAGE to the intended registry/repository before publishing.
IMAGE=ghcr.io/bkroeze/oregon-dev-foundry:latest just docker-push
```

Do not bake `.env` into an image or commit it. Configure production secrets through the deployment platform.
