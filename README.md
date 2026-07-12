# Oregon Dev Foundry

Static layout exploration for [oregondevfoundry.com](https://oregondevfoundry.com).

The direction combines the decisive, maker-led identity of “The Product Foundry” with the operational `Diagnose / Design / Ship / Operate` process from “The Workshop.”

## Container workflow

The production image uses unprivileged Nginx on Alpine. It defaults to port `8080`, runs as a non-root user, and exposes `/healthz` for container-platform health checks.

```sh
# Build the local image
just build

# Run on the default port (8080)
just run

# Run on a specified port
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
IMAGE=ghcr.io/owner/oregon-dev-foundry:latest just push
```

`just push` builds before pushing and refuses the default local-only image name. Authenticate to the registry first—for GHCR, `docker login ghcr.io`.

You can also set defaults in a local `.env` file:

```dotenv
IMAGE=ghcr.io/owner/oregon-dev-foundry:latest
PORT=3000
```

No application build step or external JavaScript dependencies are required. The site uses Google Fonts with local font fallbacks.

---

Last updated: July 12, 2026
