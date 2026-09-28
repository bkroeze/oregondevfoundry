# One-time purchase

Visitors can select an active one-time product, maintain a cookie-backed cart, start Stripe Embedded Checkout with reserved inventory, and receive an idempotently fulfilled order with optional referral and account invitation.

## User entry points

- `GET /shop/{slug}` and reusable `GET /shop/blocks/{key}` purchase surfaces.
- `GET /cart`, `POST /cart/items`, `POST /cart/items/update`, and `POST /cart/items/remove`.
- `POST /checkout`, `GET /checkout/return`, and `POST /stripe/webhook`.
- Optional referral capture from an external HTTP `Referer` before product or checkout requests.

## Primary source path

- **Entry:** `internal/web/checkout.go::Handler.checkoutCart` — validates buyer/cart state, resumes or creates a pending checkout, and starts Embedded Checkout.
- **Orchestrator:** `internal/commerce/checkout.go::Store.CreateCheckout` — snapshots cart selections and reserves finite inventory transactionally.
- **Effect boundary:** `internal/commerce/fulfillment.go::Store.Fulfill` — idempotently commits the paid order, order items, referral attribution, cart abandonment, and optional invitation state.

## Variants and gates

- Only `one_time` products enter the cart; subscription products use the separate monthly-subscription flow.
- Cart mutations and checkout require a valid commerce CSRF form.
- Finite inventory is reserved when the checkout snapshot is created, before payment completes.
- An existing usable Stripe session is resumed instead of creating a duplicate checkout.
- A guest can request an account invitation; a logged-in checkout associates the order directly with that user.
- Checkout return and signed webhook paths both converge on fulfillment; Mailgun delivery happens after durable fulfillment.

## Effects and invariants

- Cart selection and inventory validation are atomic.
- Expired or safely canceled pending checkouts release only the inventory they reserved.
- Duplicate return/webhook processing cannot create a second order or invitation.
- Fulfillment preserves immutable checkout pricing, records the order, abandons the source cart, and retains referral attribution when present.

## Verification

- **Focused check:** `go test ./internal/web -run '^TestGuestCartWebhookFulfillmentReferralAndInvitation$'`
- **Runtime path:** with disposable catalog data and Stripe test credentials, add a finite-stock SKU to the cart, start checkout, complete payment, follow the return path, and replay the webhook once.
- **Evidence:** one order exists with the snapshotted price and referral, inventory changed once, the cart is abandoned, and any requested invitation and delivery are emitted once.

## Gotchas

- The Stripe external boundary is `internal/payment/stripe.go::StripeGateway.CreateSession`; do not confuse it with the durable checkout snapshot.
- Inventory reservation occurs before payment, so failure cleanup is part of correctness.
- `internal/web/referrals.go` captures attribution before the purchase handlers run.
- Account invitation completion is a separate user flow even though invitation creation is a fulfillment effect.
