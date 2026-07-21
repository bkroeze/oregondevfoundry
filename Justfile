set dotenv-load
set shell := ["bash", "-euo", "pipefail", "-c"]

image := env_var_or_default("IMAGE", "ghcr.io/bkroeze/oregon-dev-foundry:latest")
templ := "go run github.com/a-h/templ/cmd/templ@v0.3.977"

# Show available recipes.
default:
    @just --list

# Generate Go source from templ files.
generate:
    {{ templ }} generate

# Regenerate templates on changes and restart the app.
dev:
    {{ templ }} generate --watch --cmd="go run ./cmd/server" --proxy="http://127.0.0.1:{{ env_var_or_default("PORT", "8080") }}"

# Build the production server binary.
build: generate
    mkdir -p bin
    CGO_ENABLED=0 go build -trimpath -o bin/oregon-dev-foundry ./cmd/server

# Run the server locally (environment is loaded from .env when present).
run: generate
    go run ./cmd/server

# Manage users: `just users list|show|create|update|delete [username] [display-name] [role] [has-purchases]`.
users operation="list" username="" display_name="" role="user" has_purchases="false":
    #!/usr/bin/env bash
    operation={{ quote(operation) }}
    username={{ quote(username) }}
    display_name={{ quote(display_name) }}
    role={{ quote(role) }}
    has_purchases={{ quote(has_purchases) }}
    case "$operation" in
      list) exec go run ./cmd/users list ;;
      show) exec go run ./cmd/users show --username "$username" ;;
      create) exec go run ./cmd/users create --username "$username" --display-name "$display_name" --role "$role" --has-purchases="$has_purchases" --password-stdin ;;
      update) exec go run ./cmd/users update --username "$username" --display-name "$display_name" --role "$role" --has-purchases="$has_purchases" ;;
      delete) exec go run ./cmd/users delete --username "$username" --confirm "$username" ;;
      *) printf 'error: "operation must be list, show, create, update, or delete"\n' ; exit 2 ;;
    esac

# Run all tests; injected fakes ensure tests never send mail.
test:
    go test -race ./...

# Format Go and templ source.
fmt:
    {{ templ }} fmt .
    gofmt -w cmd internal

# Run Go static analysis.
lint:
    go vet ./...

# Verify generation, formatting, tests, vet, and a production build.
check:
    #!/usr/bin/env bash
    before="$(mktemp -d)"
    trap 'rm -rf "$before"' EXIT
    {{ templ }} fmt .
    gofmt -w cmd internal
    rm -f internal/templates/*_templ.go
    {{ templ }} generate
    cp internal/templates/*_templ.go "$before/"
    rm -f internal/templates/*_templ.go
    {{ templ }} generate
    for f in internal/templates/*_templ.go; do cmp "$before/$(basename "$f")" "$f"; done
    go test -race ./...
    go vet ./...
    mkdir -p bin
    CGO_ENABLED=0 go build -trimpath -o bin/oregon-dev-foundry ./cmd/server

# Build the production container image.
docker-build:
    docker build --pull -t "{{ image }}" .

# Push IMAGE to its configured registry.
docker-push: docker-build
    #!/usr/bin/env bash
    if [[ "{{ image }}" != */* ]]; then
      echo "IMAGE must include a registry/repository before it can be pushed." >&2
      exit 2
    fi
    docker push "{{ image }}"
