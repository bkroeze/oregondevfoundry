set dotenv-load := true
set shell := ["bash", "-euo", "pipefail", "-c"]

image := env_var_or_default("IMAGE", "ghcr.io/bkroeze/oregon-dev-foundry:latest")
default_port := env_var_or_default("PORT", "8090")
container_name := "oregon-dev-foundry"

# Show available commands.
default:
    @just --list

# Build the production container. Override the tag with IMAGE=registry/name:tag.
build:
    docker build --pull -t "{{image}}" .

# Run the backend/contact endpoint test suite.
test:
    node --test

# Push IMAGE to its configured registry (example: IMAGE=ghcr.io/owner/oregon-dev-foundry:latest just push).
push: build
    #!/usr/bin/env bash
    if [[ "{{image}}" != */* ]]; then
      echo "IMAGE must include a registry/repository before it can be pushed." >&2
      exit 2
    fi
    docker push "{{image}}"

# Run the container locally; e.g. `just run 3000`.
run port=default_port: build
    docker run --rm --name "{{container_name}}" --env-file .env -e PORT="{{port}}" -p "{{port}}:{{port}}" "{{image}}"

# Run the container detached; e.g. `just up 3000`.
up port=default_port: build
    docker rm -f "{{container_name}}" >/dev/null 2>&1 || true
    docker run -d --name "{{container_name}}" --env-file .env -e PORT="{{port}}" -p "{{port}}:{{port}}" "{{image}}"
    @echo "Oregon Dev Foundry: http://127.0.0.1:{{port}}"

# Stop and remove the detached local container.
down:
    docker rm -f "{{container_name}}"

# Show logs from the detached container.
logs:
    docker logs -f "{{container_name}}"

# Probe the detached container's health endpoint.
check port=default_port:
    curl --fail --silent --show-error "http://127.0.0.1:{{port}}/healthz"
