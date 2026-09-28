# Contact inquiry

Visitors can submit a validated inquiry from the public site, pass spam checks, and receive either an HTMX form update or a full-page response while the message is delivered through Mailgun.

## User entry points

- Landing-page `#contact` form and service-page links to `/#contact`.
- `GET /api/contact-config` returns the public Turnstile site key.
- `POST /api/contact` accepts the ordinary HTML form and the HTMX-enhanced form.

## Primary source path

- **Entry:** `internal/web/handler.go::Handler.submitContact` — bounds and parses the form, applies the honeypot, coordinates validation, verification, delivery, and rendering.
- **Effect boundary:** `internal/contact/contact.go::MailgunSender.Send` — sends the accepted inquiry to Mailgun with the visitor address as reply-to.

## Variants and gates

- `internal/contact/contact.go::Validate` enforces the public message-field contract before spam checks or delivery.
- `TurnstileVerifier.Verify` calls Cloudflare and requires the `contact` action; the public site key comes from `/api/contact-config`.
- After ordinary field validation succeeds, a non-empty `website` honeypot produces the normal success response without calling Turnstile or Mailgun.
- HTMX submissions render only `templates.ContactForm`; ordinary submissions render the complete page.
- HTMX responses use HTTP 200 even for displayed validation/provider failures so the fragment swaps; full-page responses preserve the meaningful HTTP status.

## Effects and invariants

- Valid input plus successful Turnstile verification causes exactly one sender call.
- Invalid input never sends mail.
- Contact bodies are capped at 16 KiB before form parsing.
- Provider details are logged server-side but never exposed in the public error response.

## Verification

- **Focused check:** `go test ./internal/web -run '^TestContact(HTMXSuccessUsesInjectedDependencies|ValidationDoesNotSendMail|ProviderErrorsAreSafe)$'`
- **Runtime path:** start the site with disposable provider configuration, submit `#contact` once with JavaScript/HTMX and once as an ordinary form.
- **Evidence:** the form shows the same user-facing success or safe failure state in both modes; a read-only provider/fake-sender observation confirms whether delivery occurred.

## Gotchas

- `internal/notifications` sends commerce mail and is not the contact-mail implementation.
- The HTMX 200 status is deliberate; assert the rendered form state rather than expecting the full-page status.
- `internal/web/static/script.js` reinitializes Turnstile after the contact fragment is swapped.
