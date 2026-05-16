# Build stage
FROM golang:1.23-alpine AS builder

RUN apk add --no-cache git

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Build a fully static binary so it runs on distroless without glibc.
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-s -w -extldflags '-static'" \
    -o ch-api \
    ./cmd/ch-api

# ---------------------------------------------------------------------------
# Runtime stage — distroless/static:nonroot
# ---------------------------------------------------------------------------
FROM gcr.io/distroless/static:nonroot

# The distroless:nonroot image already provides:
#   - user  65532 (nonroot)
#   - group 65532 (nonroot)
#   - /etc/passwd entry for the nonroot user
#   - CA certificates
#   - tzdata
# No shell, no package manager, no unnecessary files.

WORKDIR /app

# Copy the static binary.
COPY --from=builder --chown=65532:65532 /app/ch-api /app/ch-api

# Create a writable data directory for logs, audit, etc.
# The nonroot user needs a path it can write to at runtime.
RUN ["/busybox/mkdir", "-p", "/app/data"]
RUN ["/busybox/chown", "65532:65532", "/app/data"]

# Expose the HTTP API port.
EXPOSE 8080

# Run as the nonroot user provided by distroless.
USER 65532:65532

# Health check using the built-in /healthz endpoint.
# Distroless static includes wget (busybox variant) for this purpose.
# A non-zero exit from wget is treated as unhealthy by Docker automatically.
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD ["/busybox/wget", "-qO-", "http://localhost:8080/healthz"]

ENTRYPOINT ["/app/ch-api"]
