# Oregon Dev Foundry

The standalone Go web application for [oregondevfoundry.com](https://oregondevfoundry.com). It serves the public site, role-aware accounts, contact inquiries, and a database-backed Stripe Embedded Checkout catalog for one-time products and monthly subscriptions.

## Architecture

- `cmd/server`: process startup, validated configuration, HTTP timeouts, signal handling, and graceful shutdown.
- `cmd/users`: non-interactive user CRUD with password input restricted to standard input.
- `internal/auth`: SQLite users, provider-neutral identities, bcrypt credentials, opaque sessions, roles, and customer-status derivation.
- `internal/commerce`: SQLite products, SKU variations and add-ons, fixed shipping, carts, immutable checkout snapshots, orders, subscriptions, inventory, referrals, confirmations, invitations, and idempotent webhook/delivery state.
- `internal/templates`: [templ](https://templ.guide/) source and generated Go for public and account pages.
- `internal/web`: routes, authentication and authorization boundaries, CSRF protection, secure cookies, embedded assets, request limits, and HTMX fallback.
- `internal/contact`: input validation, Cloudflare Turnstile verification, a testable `Sender` interface, and the production [`mailgun-go/v4`](https://github.com/mailgun/mailgun-go) implementation.
- `internal/payment`: a testable Stripe Checkout gateway for embedded one-time and monthly sessions plus signed webhook decoding.
- `internal/notifications`: purchaser-facing Mailgun template delivery for order status and account invitations.
- `internal/config`: environment loading, defaults, required-value checks, port bounds, Mailgun-region validation, grouped Stripe configuration, and public-origin validation.

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
| `ORDER_FROM` | No | Verified purchaser-email sender; defaults to `CONTACT_FROM`. |
| `STRIPE_SECRET_KEY` | Checkout | Stripe server key. Configure all checkout variables together. |
| `STRIPE_PUBLISHABLE_KEY` | Checkout | Browser-safe Stripe key used by Embedded Checkout. |
| `STRIPE_WEBHOOK_SECRET` | Checkout | Signing secret for `POST /stripe/webhook`. |
| `PUBLIC_BASE_URL` | Checkout | Absolute public HTTP(S) origin used for Stripe returns and invitation links; no query or fragment. |
| `TURNSTILE_SITE_KEY` | Yes | Public Cloudflare Turnstile widget key. |
| `TURNSTILE_SECRET_KEY` | Yes | Private Turnstile verification key. |
| `IMAGE` | No | Image tag used by the Docker recipes. |
| `DATABASE_PATH` | No | SQLite path, default `data/oregon-dev-foundry.db`; the parent directory is created with user-only permissions. |
| `SESSION_COOKIE_SECURE` | No | `true` by default. Set `false` only for local plain-HTTP development. |
| `SERVICES_ENABLED` | No | Registers `GET /services` and shows its primary-navigation link when `true`; default `false`. |
| `TESTIMONIALS_ENABLED` | No | Renders the homepage testimonials section when `true`; default `false`. |

Mailgun, Turnstile, and Stripe secrets remain server-side. Stripe configuration is optional as a group for local development without checkout; checkout routes return unavailable until all four checkout values are configured. Production Mailgun must contain active `order-status` and `purchase-account-invitation` templates using the variables sent by `internal/notifications`.

Configure the Stripe webhook endpoint for `checkout.session.completed`, `checkout.session.async_payment_succeeded`, `checkout.session.async_payment_failed`, and `checkout.session.expired`. Expiration and asynchronous-payment failure events release reserved SKU inventory for unpaid Checkout Sessions.

## Development

```sh
just generate  # regenerate Go sources from all templ files
just dev       # watch templ files and restart through templ's development proxy
just run       # generate and run directly
```

The service logs its listen address. Stop it with `Ctrl-C`; `SIGINT` and `SIGTERM` trigger a bounded graceful shutdown.

Apply the default test product fixture (or provide another JSON fixture and database path):

```sh
just fixture
just fixture fixtures/another-product.json /tmp/oregon-dev-foundry.db
```

The fixture command creates or updates the product by slug or master SKU, then creates or updates its listed SKUs and shipping options. Records omitted from a fixture are left unchanged. Use `null` for an unlimited SKU inventory.


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

The final `true`/`false` value on `create` and `update` controls the purchase-history marker used to distinguish `New Customer` from `Customer`. Successful checkout fulfillment sets it automatically. Set `DATABASE_PATH` to manage a non-default database.
The underlying `go run ./cmd/users` commands also accept `--email` when creating or updating an account; passing an empty value during an update clears the email.
CLI deletion refuses users with active Stripe Checkout or subscription records; cancel those through the account deletion workflow first.

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
| `GET /` | Render the public site, including process, work, company context, contact, and testimonials when `TESTIMONIALS_ENABLED=true`. |
| `GET /services` | Render public services when `SERVICES_ENABLED=true`; otherwise the route is not registered. |
| `GET /login` | Render username/password login and Visitor status. |
| `POST /login` | Validate CSRF and credentials, then start a session. |
| `POST /logout` | Validate CSRF and end the current session. |
| `GET /profile` | Authenticated user profile and applicable customer status. |
| `GET, POST /profile/password` | Change a non-admin password after current-password verification and revoke active sessions. |
| `GET, POST /profile/delete` | Delete the signed-in identity after canceling open Stripe checkouts and subscriptions; retain legal commerce/payment records. |
| `GET /client` | Client-only landing page. |
| `GET /admin` | Admin-only landing page. |
| `GET /admin/shop` | Admin landing alias for product, SKU, inventory, pricing, and fixed-shipping management. |
| `GET, POST /admin/users...` | Admin-only user CRUD, account details, and order/subscription history; admin password changes remain CLI-only. |
| `GET, POST /admin/products...` | Admin-only product, SKU, inventory, pricing, and fixed-shipping management. |
| `GET /shop/{slug}` | Render any active database-backed product. |
| `GET /shop/blocks/{key}` | Render a reusable purchase block by product slug, master SKU, or variation SKU. |
| `GET /cart` | Review the one-time product cart. |
| `POST /cart/items...` | Add, update, or remove one-time cart items. |
| `POST /checkout` | Snapshot a cart and create one-time Embedded Checkout. |
| `POST /subscriptions/checkout` | Create a separate monthly Embedded Checkout session. |
| `GET /checkout/return` | Verify Stripe session state and render confirmation. |
| `POST /stripe/webhook` | Verify Stripe signatures and idempotently fulfill paid sessions. |
| `GET, POST /account/invite` | Activate an account requested during purchase. |
| `GET /styles.css` | Embedded stylesheet. |
| `GET /script.js` | Embedded navigation/year/HTMX integration script. |
| `GET /assets/*` | Serve embedded public image assets, including testimonial logos. |
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
