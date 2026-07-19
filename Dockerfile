FROM golang:1.24-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/oregon-dev-foundry ./cmd/server && \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/users ./cmd/users

FROM alpine:3.22

RUN mkdir -p /data && chown 10001:10001 /data

COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build --chown=10001:10001 /out/oregon-dev-foundry /usr/local/bin/oregon-dev-foundry
COPY --from=build --chown=10001:10001 /out/users /usr/local/bin/users

ENV PORT=8080 DATABASE_PATH=/data/oregon-dev-foundry.db SESSION_COOKIE_SECURE=true
USER 10001:10001
EXPOSE 8080
VOLUME ["/data"]

HEALTHCHECK --interval=30s --timeout=3s --start-period=3s --retries=3 \
  CMD wget -q -O /dev/null "http://127.0.0.1:${PORT}/healthz" || exit 1

ENTRYPOINT ["/usr/local/bin/oregon-dev-foundry"]
