# Releasing

This document describes how to create, publish, and verify a Cloud Hypervisor
API release using GoReleaser and cosign.

## Overview

Releases are fully automated via [GoReleaser](https://goreleaser.com/) and
triggered by pushing a Git tag.  The pipeline:

1. Runs tests (`go test -race ./...`)
2. Cross-compiles for `linux/amd64` and `linux/arm64`
3. Creates tar archives with the binary + systemd unit + docs
4. Generates SHA-256 checksums
5. Generates SPDX SBOMs with Syft
6. Signs artifacts with cosign (keyless via GitHub OIDC)
7. Builds and pushes multi-platform Docker images to GHCR
8. Publishes a GitHub Release with an auto-generated changelog

## Prerequisites

### For maintainers (creating releases)

- GoReleaser Pro or v2:
  ```bash
  go install github.com/goreleaser/goreleaser/v2@latest
  ```
- cosign (for local signing):
  ```bash
  go install github.com/sigstore/cosign/v2/cmd/cosign@latest
  ```
- syft (for SBOM generation):
  ```bash
  go install github.com/anchore/syft/cmd/syft@latest
  ```
- Docker Buildx (for multi-platform images):
  ```bash
  docker buildx create --use --name ch-builder
  ```
- GitHub token with `repo` and `write:packages` scopes
- Git commit history using [Conventional Commits](https://www.conventionalcommits.org/)

### For users (verifying releases)

- cosign:
  ```bash
  go install github.com/sigstore/cosign/v2/cmd/cosign@latest
  ```

## Creating a Release

### 1. Prepare the release

Ensure the main branch is clean and all CI checks pass:

```bash
git checkout main
git pull origin main
make test
make lint
```

### 2. Update version references (optional)

If the version is hardcoded anywhere (e.g., `docs/swagger.yaml`), update it:

```bash
# Example: bump the version in docs
sed -i 's/version: .*/version: 1.2.0/' docs/swagger.yaml
git add docs/swagger.yaml
git commit -m "chore(release): bump version to 1.2.0"
```

### 3. Tag the release

Follow [Semantic Versioning](https://semver.org/):

```bash
# Patch release (bug fixes)
git tag -a v1.2.1 -m "chore(release): v1.2.1"

# Minor release (new features)
git tag -a v1.3.0 -m "chore(release): v1.3.0"

# Major release (breaking changes)
git tag -a v2.0.0 -m "chore(release): v2.0.0"
```

### 4. Push the tag

```bash
git push origin v1.2.1
```

### 5. GoReleaser runs automatically (GitHub Actions)

When the tag is pushed, the `.github/workflows/release.yml` workflow triggers
GoReleaser with the GitHub OIDC identity for keyless signing.

### 6. Or run locally (for testing)

```bash
# Snapshot build (no release, no signing)
goreleaser release --snapshot --clean

# Full release (requires GITHUB_TOKEN env var and cosign setup)
export GITHUB_TOKEN=$(gh auth token)
goreleaser release --clean
```

## Release Artifacts

Each release produces the following assets on the GitHub Release page:

| Asset | Description |
|-------|-------------|
| `ch-api-vX.Y.Z-linux-amd64.tar.gz` | Linux amd64 binary + systemd unit + docs |
| `ch-api-vX.Y.Z-linux-arm64.tar.gz` | Linux arm64 binary + systemd unit + docs |
| `ch-api_vX.Y.Z_checksums.txt` | SHA-256 checksums for all archives |
| `ch-api_vX.Y.Z_sbom.spdx.json` | SPDX SBOM for supply-chain security |
| `*.tar.gz.sig` | cosign signature |
| `*.tar.gz.pem` | cosign certificate (Fulcio) |

Plus Docker manifest tags:
- `ghcr.io/org/ch-api:vX.Y.Z`
- `ghcr.io/org/ch-api:vX`
- `ghcr.io/org/ch-api:vX.Y`
- `ghcr.io/org/ch-api:latest`

## Verifying a Release

### Checksum verification

```bash
# Download the release tarball and checksums file
curl -LO https://github.com/org/ch-api/releases/download/v1.2.1/ch-api-v1.2.1-linux-amd64.tar.gz
curl -LO https://github.com/org/ch-api/releases/download/v1.2.1/ch-api_v1.2.1_checksums.txt

# Verify
sha256sum --check ch-api_v1.2.1_checksums.txt
```

### Signature verification (keyless)

For releases built on GitHub Actions with keyless signing:

```bash
# Download signature and certificate
curl -LO https://github.com/org/ch-api/releases/download/v1.2.1/ch-api-v1.2.1-linux-amd64.tar.gz.sig
curl -LO https://github.com/org/ch-api/releases/download/v1.2.1/ch-api-v1.2.1-linux-amd64.tar.gz.pem

# Verify with cosign
cosign verify-blob \
  --signature ch-api-v1.2.1-linux-amd64.tar.gz.sig \
  --certificate ch-api-v1.2.1-linux-amd64.tar.gz.pem \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-identity-regexp '^https://github.com/org/ch-api/.github/workflows/' \
  ch-api-v1.2.1-linux-amd64.tar.gz
```

### Signature verification (key-based)

If your organization uses a static cosign key pair:

```bash
# Download the public key (published in the repo or via KMS)
curl -LO https://github.com/org/ch-api/cosign.pub

# Verify
cosign verify-blob \
  --key cosign.pub \
  --signature ch-api-v1.2.1-linux-amd64.tar.gz.sig \
  ch-api-v1.2.1-linux-amd64.tar.gz
```

## Changelog

GoReleaser generates the changelog automatically from conventional commits.

### Commit format

```
feat: add VM snapshot endpoint
fix: handle missing kernel path gracefully
perf: reduce memory allocations in handler
docs: update systemd installation guide
chore: bump Go to 1.23
test: add fuzz target for CreateVMRequest
```

### Changelog groups

| Group | Pattern | Order |
|-------|---------|-------|
| Breaking Changes | `break!`, `breaking!` | 0 |
| Features | `feat:` | 1 |
| Bug Fixes | `fix:` | 2 |
| Security | `sec:`, `security:` | 3 |
| Performance | `perf:` | 4 |
| Documentation | `docs:` | 5 |
| Tests | `test:` | 6 |
| Build & CI | `build:`, `ci:` | 7 |
| Refactoring | `refactor:` | 8 |
| Other | Everything else | 999 |

Excluded: `docs:`, `test:`, `ci:`, `chore:`, `style:`, `refactor:`, merge commits.

## Rollback

If a release is broken:

```bash
# 1. Delete the GitHub Release (retain the tag)
gh release delete v1.2.1 --yes

# 2. Delete the Docker manifest tags
docker buildx imagetools rm ghcr.io/org/ch-api:v1.2.1

# 3. Push a fix, tag a new patch release
git tag -a v1.2.2 -m "fix: resolve regression in v1.2.1"
git push origin v1.2.2
```

## Troubleshooting

### "cosign: command not found"

Install cosign:

```bash
go install github.com/sigstore/cosign/v2/cmd/cosign@latest
```

### "GITHUB_TOKEN is not set"

Generate a token:

```bash
gh auth login
export GITHUB_TOKEN=$(gh auth token)
```

### "docker: no builder found"

Create a Buildx builder:

```bash
docker buildx create --use --name ch-builder
```

### "goreleaser: snapshot version used"

GoReleaser uses snapshot mode when the working tree is dirty or no tag is
present.  Ensure you are on a clean checkout with the tag checked out:

```bash
git status  # should be clean
git checkout v1.2.1
```

## Makefile Integration

The `Makefile` includes a `snapshot` target for local testing:

```bash
make snapshot   # Build snapshot artifacts without releasing
```

And a `release-dry-run` target:

```bash
make release-dry-run  # Simulate a full release locally
```

## Further Reading

- [GoReleaser documentation](https://goreleaser.com/customization/)
- [cosign documentation](https://docs.sigstore.dev/cosign/overview/)
- [Conventional Commits](https://www.conventionalcommits.org/)
- [Semantic Versioning](https://semver.org/)
- [SPDX SBOM](https://spdx.dev/)
