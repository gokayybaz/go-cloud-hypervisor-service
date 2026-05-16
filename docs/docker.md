# Docker

This document describes how to build, run, and push the Cloud Hypervisor API
Docker image.

## Image Overview

| Attribute | Value |
|-----------|-------|
| Base image (runtime) | `gcr.io/distroless/static:nonroot` |
| Base image (builder) | `golang:1.23-alpine` |
| User | `65532:65532` (nonroot) |
| Binary | Statically-linked, CGO disabled |
| Ports | `8080` (HTTP API) |
| Health check | `GET /healthz` every 30s |
| Shell | None (distroless) |
| Size (typical) | ~25–30 MiB |

## Why distroless?

The runtime stage uses Google's [distroless](https://github.com/GoogleContainerTools/distroless)
`static:nonroot` image instead of Alpine or Debian.  Benefits:

- **Minimal attack surface** — no shell, package manager, or utilities an
  attacker can exploit
- **Small footprint** — the static image is ~2 MiB; the final image is mostly
  the Go binary
- **Non-root by default** — user `65532` is baked into the image
- **Reproducible** — fewer moving parts mean fewer sources of drift

## Build

### Local build

```bash
docker build -t ch-api:latest .
```

### Multi-platform build (Buildx)

```bash
# Create a Buildx builder (once)
docker buildx create --use --name ch-builder

# Build for multiple platforms
docker buildx build \
  --platform linux/amd64,linux/arm64 \
  -t ch-api:latest \
  .
```

### With version tag

```bash
VERSION=$(git describe --tags --always --dirty)
docker build -t ch-api:${VERSION} -t ch-api:latest .
```

## Run

### Quick start

```bash
docker run -d \
  --name ch-api \
  -p 8080:8080 \
  -v ch-api-data:/app/data \
  ch-api:latest
```

### With a custom configuration file

```bash
# Create a config file on the host
mkdir -p ~/.config/ch-api
cat > ~/.config/ch-api/config.yaml <<'EOF'
server:
  port: "8080"
log:
  level: "info"
  format: "json"
auth:
  enabled: false
EOF

docker run -d \
  --name ch-api \
  -p 8080:8080 \
  -v ~/.config/ch-api/config.yaml:/etc/ch-api/config.yaml:ro \
  -v ch-api-data:/app/data \
  -e CH_API_CONFIG=/etc/ch-api/config.yaml \
  ch-api:latest
```

### With Cloud Hypervisor socket passthrough

```bash
docker run -d \
  --name ch-api \
  -p 8080:8080 \
  -v /var/run/ch-api:/var/run/ch-api:rw \
  -v ch-api-data:/app/data \
  -e CH_API_CLOUD_HYPERVISOR_SOCKET_PATH=/var/run/ch-api/ch.sock \
  ch-api:latest
```

### Check health

```bash
# Docker's built-in health check
docker inspect --format='{{.State.Health.Status}}' ch-api

# Manual check
curl http://localhost:8080/healthz
```

### View logs

```bash
# Follow logs
docker logs -f ch-api

# Last 100 lines
docker logs --tail 100 ch-api
```

## Push

### To Docker Hub

```bash
# Tag with registry prefix
DOCKER_USER=yourusername
docker tag ch-api:latest ${DOCKER_USER}/ch-api:latest
docker tag ch-api:latest ${DOCKER_USER}/ch-api:$(git describe --tags --always)

# Log in and push
docker login -u ${DOCKER_USER}
docker push ${DOCKER_USER}/ch-api:latest
docker push ${DOCKER_USER}/ch-api:$(git describe --tags --always)
```

### To GHCR (GitHub Container Registry)

```bash
GHCR_USER=yourusername
GHCR_REPO=yourrepo
IMAGE=ghcr.io/${GHCR_USER}/${GHCR_REPO}/ch-api

# Log in with a GitHub personal access token (PAT)
echo $GHCR_PAT | docker login ghcr.io -u ${GHCR_USER} --password-stdin

# Tag and push
docker tag ch-api:latest ${IMAGE}:latest
docker tag ch-api:latest ${IMAGE}:$(git describe --tags --always)
docker push ${IMAGE}:latest
docker push ${IMAGE}:$(git describe --tags --always)
```

### Multi-platform push

```bash
VERSION=$(git describe --tags --always --dirty)
REGISTRY=ghcr.io/yourusername/yourrepo

docker buildx build \
  --platform linux/amd64,linux/arm64 \
  -t ${REGISTRY}/ch-api:latest \
  -t ${REGISTRY}/ch-api:${VERSION} \
  --push \
  .
```

## Dockerfile Reference

### Builder stage

| Layer | Description |
|-------|-------------|
| `FROM golang:1.23-alpine` | Go toolchain with Alpine Linux |
| `go mod download` | Cache module dependencies |
| `go build` | Static binary with `-ldflags="-s -w -extldflags '-static'"` |

### Runtime stage

| Layer | Description |
|-------|-------------|
| `FROM gcr.io/distroless/static:nonroot` | Minimal image with non-root user `65532` |
| `COPY --chown=65532:65532` | Binary owned by nonroot |
| `USER 65532:65532` | Drop to non-root at runtime |
| `HEALTHCHECK` | `wget http://localhost:8080/healthz` every 30s |
| `ENTRYPOINT` | `/app/ch-api` (no shell wrapper needed) |

### Security features

| Feature | Implementation |
|---------|----------------|
| Non-root user | `USER 65532:65532` (distroless built-in) |
| No shell | distroless `static` has no `/bin/sh` |
| No package manager | No `apk`, `apt`, `yum`, etc. |
| Minimal filesystem | Only `/busybox/wget`, CA certs, and tzdata |
| Read-only root | The image has no writable paths except `/app/data` |
| Static binary | `CGO_ENABLED=0` eliminates glibc dependency |

## Troubleshooting

### "exec user process caused: no such file or directory"

This means the binary is not fully static.  Ensure `CGO_ENABLED=0` and
`-extldflags '-static'` are both set in the build command.

### Health check fails

The health check uses distroless's built-in `wget` (busybox variant).  If the
service takes longer than 10 seconds to start, the first few health checks may
fail.  Adjust `--start-period` in the `HEALTHCHECK` instruction if needed.

### Cannot write to /app/data

The `nonroot` user (`65532`) needs write access to `/app/data`.  The Dockerfile
creates and `chown`s this directory at build time.  If you mount an external
volume, ensure it is writable by user `65532`:

```bash
docker run -d \
  -v $(pwd)/data:/app/data \
  --user 65532:65532 \
  ch-api:latest
```

Or pre-create the host directory with the correct ownership:

```bash
mkdir -p ./data
sudo chown -R 65532:65532 ./data
docker run -d -v $(pwd)/data:/app/data ch-api:latest
```

### Image size is large

Ensure you are not copying unnecessary files into the build context.  The
`.dockerignore` file should exclude:

- `.git`
- `docs/`
- `*.md`
- Build artifacts (`ch-api`, `ch-api-linux-amd64`)
- IDE files (`.vscode`, `.idea`)

### Running with Docker Compose

```yaml
version: "3.9"
services:
  ch-api:
    image: ch-api:latest
    build:
      context: .
      dockerfile: Dockerfile
    ports:
      - "8080:8080"
    volumes:
      - ch-api-data:/app/data
      - ./config.yaml:/etc/ch-api/config.yaml:ro
    environment:
      - CH_API_CONFIG=/etc/ch-api/config.yaml
    restart: unless-stopped
    healthcheck:
      test: ["CMD", "/busybox/wget", "-qO-", "http://localhost:8080/healthz"]
      interval: 30s
      timeout: 5s
      retries: 3
      start_period: 10s

volumes:
  ch-api-data:
```

## Further Reading

- [distroless on GitHub](https://github.com/GoogleContainerTools/distroless) —
  Google's minimal container images
- [Dockerfile reference](https://docs.docker.com/engine/reference/builder/)
- [Buildx documentation](https://docs.docker.com/build/building/multi-platform/)
- [HEALTHCHECK](https://docs.docker.com/engine/reference/builder/#healthcheck)
