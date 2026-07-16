# Oregon Dev Foundry

Site and contact service for [oregondevfoundry.com](https://oregondevfoundry.com).

The direction combines the decisive, maker-led identity of “The Product Foundry” with the operational `Diagnose / Design / Ship / Operate` process from “The Workshop.” The contact form posts to a small dependency-free Node server, which delivers messages through Mailgun.

## Service setup

1. Add and verify a Mailgun sending domain, preferably `mg.oregondevfoundry.com`.
2. Add Mailgun's DNS records at the domain provider and wait until Mailgun reports the domain verified.
3. Create a Cloudflare Turnstile widget for the deployed hostname and copy its site and secret keys.
4. Copy `.env.example` to `.env` and set the Mailgun and Turnstile credentials, sending domain, recipient, and sender.
5. If using a Mailgun sandbox domain, authorize `CONTACT_TO` in Mailgun first. Production sending should use the verified custom domain.

The Mailgun key and Turnstile secret exist only on the server. Neither is sent to the browser or baked into the image; only the public Turnstile site key is exposed.

```sh
cp .env.example .env
$EDITOR .env
just test
just run
```

The form includes browser and server validation, Cloudflare Turnstile verification, a hidden honeypot, a 16 KB body limit, bounded provider requests, sanitized provider errors, and a direct-email fallback.

## Container workflow

The production image uses Node on Alpine, defaults to port `8080`, runs as the non-root `node` user, and exposes `/healthz` for container health checks. The backend is intentionally narrow and swappable: the frontend knows only `POST /api/contact`.

```sh
# Test and build
just test
just build

# Run on the configured default port, or specify one
just run
just run 3000

# Start detached, verify it, inspect logs, and stop it
just up 3000
just check 3000
just logs
just down
```

The same port is configured inside the container and published on the host. Ports below `1024` are inappropriate because the image deliberately runs unprivileged.

To tag and push to a registry, set `IMAGE`:

```sh
IMAGE=ghcr.io/bkroeze/oregon-dev-foundry:latest just push
```

Local defaults can live in `.env` alongside the Mailgun settings:

```dotenv
IMAGE=ghcr.io/bkroeze/oregon-dev-foundry:latest
PORT=8090
```

No application dependencies or package-install step are required. The site uses Google Fonts with local font fallbacks.

---

Last updated: July 12, 2026
