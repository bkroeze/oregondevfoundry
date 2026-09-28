# Monthly subscription

Visitors can start a recurring monthly Stripe checkout directly from a subscription product and receive an idempotently fulfilled subscription without creating or mutating a cart.

## User entry points

- A subscription product form posts directly to `POST /subscriptions/checkout`.
- Stripe Embedded Checkout returns through `GET /checkout/return`.
- Stripe lifecycle completion also enters through `POST /stripe/webhook`.

## Primary source path

- **Entry:** `internal/web/checkout.go::Handler.checkoutSubscription` — validates the standalone SKU and purchaser details for subscription mode.
- **Orchestrator:** `internal/web/checkout.go::Handler.startEmbeddedCheckout` — builds recurring Stripe lines, creates the external session, and attaches it to the durable checkout.
- **Effect boundary:** `internal/commerce/fulfillment.go::Store.Fulfill` — idempotently creates the subscription and booking-confirmation records and links any user or invitation.

## Variants and gates

- This flow calls `internal/payment/stripe.go::StripeGateway.CreateSession` in subscription mode with recurring monthly line items.
- Subscription SKUs cannot enter a cart, take add-ons, or use shipping options.
- Return and signed-webhook paths share fulfillment and email-delivery claims.
- The adjacent account-deletion flow uses the stored Stripe subscription ID, cancels externally, then marks the local subscription canceled; that end-to-end feature is not mapped here.

## Effects and invariants

- Starting subscription checkout leaves the cart and cart cookie unchanged.
- Fulfillment creates one `commerce_subscriptions` row and one subscription booking confirmation for a checkout.
- Replayed return/webhook processing returns the existing fulfillment rather than duplicating records or email.
- Failed email delivery releases its claim for retry without rolling back durable subscription fulfillment.

## Verification

- **Focused check:** `go test ./internal/web -run '^TestMonthlySubscriptionCheckoutStaysOutsideCart$'`
- **Runtime path:** with a disposable subscription SKU and Stripe test credentials, submit the product form, complete Embedded Checkout, process return and webhook paths, then inspect the account purchase history.
- **Evidence:** no cart cookie or cart mutation occurs; exactly one subscription and confirmation exist with the Stripe subscription ID and monthly product data.

## Gotchas

- `Store.CreateCheckout` lives in `internal/commerce/checkout.go`, not `cart.go` or `fulfillment.go`.
- Stripe session creation is an external effect before durable fulfillment; cleanup must not release state while a live external session can still complete.
- One-time and subscription checkout share return and webhook handlers but have different cart, shipping, line-item, and fulfillment invariants.
