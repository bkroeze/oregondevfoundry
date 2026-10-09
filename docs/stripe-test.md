# Stripe test checkout

This walkthrough exercises the local one-time purchase flow with Stripe test data. It uses the repository fixture at [`fixtures/test-product.json`](../fixtures/test-product.json), Stripe Embedded Checkout, the Stripe CLI, and an isolated SQLite database.

The fixture creates **Test Product** at `/shop/test-product` with two one-cent variations (`Red` and `Blue`) and free shipping. Its SKUs omit `inventory`, so they have unlimited inventory; this guide does not validate finite-stock reservation or release.

## Prerequisites

- Go 1.24 or newer
- `just`
- The [Stripe CLI](https://docs.stripe.com/cli)
- Access to a Stripe test-mode account or sandbox
- Working non-production Mailgun credentials if you want the webhook to finish email delivery

The application requires the Mailgun and Turnstile variables in `.env.example` even when testing only checkout. Use disposable or development credentials. Do not commit `.env`, API keys, webhook secrets, or real card details.

## 1. Configure an isolated local environment

Copy the example configuration if you do not already have a local `.env`:

```sh
cp .env.example .env
$EDITOR .env
```

Set these values in `.env`:

```dotenv
# Keep this checkout's data separate from normal development data.
DATABASE_PATH=/tmp/oregon-dev-foundry-stripe-test.db

# Local HTTP is intentional. Secure cookies require HTTPS.
SESSION_COOKIE_SECURE=false
PUBLIC_BASE_URL=http://localhost:8080

# Use test-mode keys only.
STRIPE_SECRET_KEY=sk_test_...
STRIPE_PUBLISHABLE_KEY=pk_test_...
STRIPE_WEBHOOK_SECRET=whsec_...
```

Also provide the required `MAILGUN_API_KEY`, `MAILGUN_DOMAIN`, `CONTACT_TO`, `CONTACT_FROM`, `TURNSTILE_SITE_KEY`, and `TURNSTILE_SECRET_KEY` values. `ORDER_FROM` is optional and defaults to `CONTACT_FROM`.

`PUBLIC_BASE_URL` must be the origin only: `http://localhost:8080` is valid; a path, query string, or fragment is not. The four checkout values—`STRIPE_SECRET_KEY`, `STRIPE_PUBLISHABLE_KEY`, `STRIPE_WEBHOOK_SECRET`, and `PUBLIC_BASE_URL`—must be configured together.

## 2. Authenticate the Stripe CLI and obtain the local signing secret

Authenticate the CLI, then select the intended test account or sandbox if necessary:

```sh
stripe login
stripe login list
# Optional when more than one account or sandbox is available:
stripe switch context
```

Obtain the CLI listener secret:

```sh
stripe listen --print-secret
```

Copy the printed `whsec_...` value into `STRIPE_WEBHOOK_SECRET` in `.env`. The secret printed by the Stripe CLI is different from a secret for a Dashboard-managed webhook endpoint. Use the secret for the listener that is actually forwarding events.

## 3. Apply the test product fixture

From the repository root, apply the fixture to the isolated database:

```sh
just fixture fixtures/test-product.json
```

Expected output resembles:

```text
applied product "test-product" (id 1): 2 SKUs, 1 shipping options
```

The command is idempotent. Re-running it updates the product, SKUs, and shipping option instead of creating duplicate catalog records. Applying it before the server starts also avoids SQLite contention while the database is being initialized.

## 4. Start the application

In a terminal from the repository root:

```sh
just run
```

The server listens on `http://localhost:8080` by default. Leave this terminal running and watch it for checkout, webhook, and email-delivery errors.

## 5. Forward Stripe events to the application

In a second terminal, start a test-mode listener:

```sh
stripe listen \
  --events=checkout.session.completed,checkout.session.async_payment_succeeded,checkout.session.async_payment_failed,checkout.session.expired \
  --forward-to http://localhost:8080/stripe/webhook
```

Confirm that the listener reports a ready state and that its `whsec_...` secret matches `.env`. Keep this terminal open. The application endpoint is `/stripe/webhook`, not `/webhook`.

## 6. Add the fixture product to the cart

Open [http://localhost:8080/shop/test-product](http://localhost:8080/shop/test-product) in a browser:

1. Choose **Red** or **Blue**.
2. Leave quantity at `1`.
3. Select **Free shipping**.
4. Select **Add to cart**.
5. Review the cart at `/cart`.
6. Enter a name and a disposable test email. Check **Create an account for order access** only if you also want to exercise the single-use invitation email.
7. Select **Continue to payment**.

The cart total should be `$0.01`. The application snapshots the cart and shipping selection, reserves any finite inventory (not applicable to this fixture), and creates the Stripe Session on the server. It then renders Stripe Embedded Checkout with the Session client secret.

## 7. Complete a successful test payment

In the embedded Stripe form, use Stripe's standard successful test card:

| Field | Value |
| --- | --- |
| Card number | `4242 4242 4242 4242` |
| Expiration | Any future date, such as `12/34` |
| CVC | Any three digits |
| Postal code | Any value, such as `97201` |

Use test API keys throughout. Never enter a real card number, even in test mode. Select **Pay**.

The application uses Embedded Checkout with `return_url` set to `/checkout/return?session_id={CHECKOUT_SESSION_ID}` and redirects after completion. The return handler can render the confirmation immediately, but fulfillment is also driven by the webhook so a customer who closes the browser cannot lose the order.

## 8. Verify the result at every boundary

Confirm all of the following:

- The Stripe CLI terminal shows `checkout.session.completed` forwarded to `POST /stripe/webhook` with a successful response.
- The browser reaches `/checkout/return?session_id=...` and shows **Order received**, a confirmation number, the `$0.01` total, and the submitted email.
- The server has created one order for the Checkout Session.
- Stripe test mode shows the Checkout Session and successful PaymentIntent.
- A configured Mailgun sender delivers one order-status email.
- If account creation was selected, one invitation email is delivered and the confirmation page offers **Activate your account**.

The return page and webhook both converge on idempotent fulfillment. Refreshing the return page or receiving the same event again must not create a second order, decrement inventory twice, or send a second confirmation/invitation.

If Mailgun is not configured with credentials that can deliver, durable order fulfillment can complete but the webhook will report an email-delivery failure and Stripe will retry it. For a clean end-to-end result, use a working non-production Mailgun domain and a disposable inbox you control.

## 9. Exercise failure and authentication paths

Start a fresh checkout for each scenario so a previous Session or cart does not affect the result.

### Declined card

Use:

```text
4000 0000 0000 9995
```

Use any future expiration date, three-digit CVC, and postal code. Verify that Checkout displays the failure and no paid order is fulfilled.

### 3D Secure authentication

Use:

```text
4000 0025 0000 3155
```

Complete the test authentication challenge and verify that the successful flow still produces the webhook and confirmation.

### Delayed payment methods

If the selected test-mode payment methods include a delayed method, exercise both its eventual success and failure values from [Stripe's testing guide](https://docs.stripe.com/testing). Verify that:

- `checkout.session.async_payment_succeeded` fulfills only after payment succeeds.
- `checkout.session.async_payment_failed` releases the pending checkout's reserved inventory and does not create an order.

The supplied fixture has unlimited inventory, so use a catalog SKU with a finite `inventory` value when validating those inventory assertions.

## Best-practice checklist

This local flow follows Stripe's recommended Checkout shape:

- Use test-mode or sandbox keys and Stripe test values; never use real card details or test mode for load testing.
- Keep price, quantity, currency, buyer identity, and checkout metadata server-side. This application builds the Session from its database checkout snapshot and attaches the internal `checkout_id` as metadata.
- Verify the `Stripe-Signature` header against the raw request body. The Go handler reads the body before signature verification.
- Fulfill from webhooks, not only from the browser return page. The handler accepts `checkout.session.completed` and asynchronous success/failure events.
- Make fulfillment idempotent. The application claims webhook event IDs and stores unique checkout/order records before delivery work.
- Handle expired or failed pending Sessions so reserved inventory is released.
- Keep the CLI signing secret separate from any Dashboard endpoint secret. For a deployed endpoint, configure its own Dashboard-generated secret instead of the local CLI secret.
- Keep secret keys in environment variables or a secret manager. Do not commit `.env`.

## Troubleshooting

| Symptom | Check |
| --- | --- |
| `checkout is not configured` or a startup validation error | Confirm all four Stripe variables are non-empty and `PUBLIC_BASE_URL` is an absolute origin. |
| `invalid webhook` / signature verification failure | Confirm the listener is forwarding to `/stripe/webhook` and that `.env` contains the secret printed by that CLI listener, not a Dashboard secret. Restart the app after changing `.env`. |
| Checkout page does not load | Confirm `STRIPE_PUBLISHABLE_KEY` is a `pk_test_...` key, JavaScript can load `https://js.stripe.com/v3/`, and the browser can reach the local server. |
| Return page says `Payment processing` | Keep `stripe listen` running and inspect both terminals for the forwarded event or a delivery error. |
| Order appears but the webhook is non-2xx | Inspect Mailgun configuration and delivery logs. Durable fulfillment happens before purchaser email delivery, and a failed delivery is retried. |
| Fixture product is missing | Re-run `just fixture fixtures/test-product.json` against the same `DATABASE_PATH` used by the server. |

## References

- [Stripe testing](https://docs.stripe.com/testing)
- [Stripe CLI](https://docs.stripe.com/cli)
- [Stripe CLI webhook forwarding](https://docs.stripe.com/cli/listen)
- [Stripe Checkout fulfillment](https://docs.stripe.com/checkout/fulfillment.md?payment-ui=embedded-page)
- [Stripe Embedded Checkout redirect behavior](https://docs.stripe.com/payments/checkout/custom-success-page.md?payment-ui=embedded-page)
- [Stripe webhook signature verification](https://docs.stripe.com/webhooks/signature)
- [Stripe Checkout Session API](https://docs.stripe.com/api/checkout/sessions/create)
