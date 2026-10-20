# Oregon Dev Foundry feature map

This map routes user-visible behavior to a few current source anchors and a behavioral verification path. It is a navigation cache, not architecture truth: current source and runtime behavior always win.

## How to use this map

1. Match the request to one feature below.
2. Read only that feature file initially.
3. Resolve its `path::symbol` anchors against current source.
4. Expand the current path with LSP, grep, or AST search before editing.
5. Fall back to normal discovery when the map is missing or stale.

## Baseline verification

- Read `AGENTS.md` and follow its kata workflow when the task is kata-tracked.
- Run focused Go tests from the repository root; the mapped checks use fake Stripe, Turnstile, and mail implementations.
- Use authored `internal/templates/*.templ` files as source. Regenerate and check `*_templ.go` only when a template changes.
- Real checkout, contact delivery, and account-deletion smoke paths require disposable data and non-production provider credentials.

## Features

| Feature ID | User intent and vocabulary | Entry points | Map |
| --- | --- | --- | --- |
| `contact-inquiry` | contact form, inquiry, Turnstile, Mailgun, HTMX form response | `GET /`, `GET /api/contact-config`, `POST /api/contact` | [`contact-inquiry.md`](./contact-inquiry.md) |
| `account-access` | login, logout, session, profile, role navigation, login limiting | `GET, POST /login`, `POST /logout`, `GET /profile`, `GET /client`, `GET /admin` | [`account-access.md`](./account-access.md) |
| `one-time-purchase` | product, cart, one-time checkout, inventory reservation, order, referral | `GET /shop/{slug}`, `GET /cart`, `POST /cart/items...`, `POST /checkout`, checkout return and webhook | [`one-time-purchase.md`](./one-time-purchase.md) |
| `monthly-subscription` | membership, recurring checkout, subscription fulfillment | `POST /subscriptions/checkout`, checkout return and webhook | [`monthly-subscription.md`](./monthly-subscription.md) |
| `admin-catalog` | admin product, SKU, add-on, price, inventory, shipping, image import, availability, slate signage preview | `GET, POST /admin/products...`, `/admin/catalog...`, `GET /shop/vending-signage` | [`admin-catalog.md`](./admin-catalog.md) |

## Coverage boundary

- **Mapped:** public contact delivery, authentication and role-aware profiles, the two purchase modes, and admin commerce catalog management.
- **Not yet mapped:** public service-content pages, account password change and deletion, invitation completion, admin user management, the `users` CLI, static assets, health endpoints, and deployment operations.
