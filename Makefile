.PHONY: all help build test lint run install uninstall release release-dry-run snapshot docs swagger clean

# ---------------------------------------------------------------------------
# Variables
# ---------------------------------------------------------------------------

GOPATH     ?= $(shell go env GOPATH)
GOOS       ?= linux
GOARCH     ?= amd64
CGO_ENABLED ?= 0
LDFLAGS     = -s -w -extldflags '-static'

BINARY      = ch-api
BINDIR      = bin
INSTALLDIR  = /usr/local/bin
SYSTEMDDIR  = /etc/systemd/system
DATADIR     = /var/lib/ch-api
CONFIGDIR   = /etc/ch-api
UNITFILE    = systemd/ch-api.service
USER        = ch-api
GROUP       = ch-api

VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
RELEASEDIR  = dist
RELEASETAR  = $(RELEASEDIR)/$(BINARY)-$(VERSION)-$(GOOS)-$(GOARCH).tar.gz

SWAG        = $(GOPATH)/bin/swag
PKGSITE     = $(GOPATH)/bin/pkgsite
LINT        = $(GOPATH)/bin/golangci-lint

# ---------------------------------------------------------------------------
# Default target
# ---------------------------------------------------------------------------

all: build ## Build the application (default target)

# ---------------------------------------------------------------------------
# Self-documenting help
# ---------------------------------------------------------------------------

help: ## Show this help message
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

# ---------------------------------------------------------------------------
# Build
# ---------------------------------------------------------------------------

build: ## Build the ch-api binary for the local platform
	@echo "Building $(BINARY) ($(GOOS)/$(GOARCH))..."
	CGO_ENABLED=$(CGO_ENABLED) GOOS=$(GOOS) GOARCH=$(GOARCH) go build \
		-ldflags="$(LDFLAGS)" \
		-o $(BINDIR)/$(BINARY) \
		./cmd/ch-api

# ---------------------------------------------------------------------------
# Test
# ---------------------------------------------------------------------------

test: ## Run all tests with the race detector
	go test -race ./...

# ---------------------------------------------------------------------------
# Lint
# ---------------------------------------------------------------------------

lint: ## Run golangci-lint on the entire codebase
	@if ! command -v $(LINT) >/dev/null 2>&1; then \
		echo "golangci-lint not found, installing..."; \
		go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest; \
	fi
	$(LINT) run ./...

# ---------------------------------------------------------------------------
# Run
# ---------------------------------------------------------------------------

run: build ## Build and run the ch-api binary locally
	./$(BINDIR)/$(BINARY)

# ---------------------------------------------------------------------------
# Install / Uninstall (systemd)
# ---------------------------------------------------------------------------

install: build ## Install binary, unit file, create user, and enable systemd service
	@echo "Creating user and directories..."
	@id -u $(USER) >/dev/null 2>&1 || sudo useradd --system --no-create-home --shell /usr/sbin/nologin $(USER)
	@sudo mkdir -p $(CONFIGDIR) $(DATADIR)/data
	@sudo chown -R $(USER):$(GROUP) $(DATADIR)
	@sudo chmod 750 $(DATADIR)
	@echo "Installing binary..."
	@sudo cp $(BINDIR)/$(BINARY) $(INSTALLDIR)/$(BINARY)
	@sudo chmod +x $(INSTALLDIR)/$(BINARY)
	@echo "Installing systemd unit file..."
	@sudo cp $(UNITFILE) $(SYSTEMDDIR)/
	@sudo systemctl daemon-reload
	@echo "Enabling and starting service..."
	@sudo systemctl enable --now $(BINARY)
	@echo "Installation complete."
	@echo "Check status: sudo systemctl status $(BINARY)"
	@echo "View logs:    sudo journalctl -u $(BINARY) -f"

uninstall: ## Stop, disable, and remove the systemd service and binary
	@echo "Stopping and disabling service..."
	@sudo systemctl stop $(BINARY) 2>/dev/null || true
	@sudo systemctl disable $(BINARY) 2>/dev/null || true
	@echo "Removing files..."
	@sudo rm -f $(SYSTEMDDIR)/$(BINARY).service
	@sudo rm -f $(INSTALLDIR)/$(BINARY)
	@sudo systemctl daemon-reload
	@echo "Removing data directories..."
	@sudo rm -rf $(DATADIR) $(CONFIGDIR)
	@echo "Removing user..."
	@id -u $(USER) >/dev/null 2>&1 && sudo userdel $(USER) 2>/dev/null || true
	@echo "Uninstallation complete."

# ---------------------------------------------------------------------------
# Release
# ---------------------------------------------------------------------------

release: ## Build release artifacts for linux/amd64 and linux/arm64 (manual)
	@echo "Building release $(VERSION)..."
	@mkdir -p $(RELEASEDIR)
	@echo "  linux/amd64"
	@CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
		-ldflags="$(LDFLAGS) -X main.version=$(VERSION)" \
		-o $(RELEASEDIR)/$(BINARY)-linux-amd64 \
		./cmd/ch-api
	@echo "  linux/arm64"
	@CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build \
		-ldflags="$(LDFLAGS) -X main.version=$(VERSION)" \
		-o $(RELEASEDIR)/$(BINARY)-linux-arm64 \
		./cmd/ch-api
	@echo "  Creating tarballs..."
	@tar czf $(RELEASEDIR)/$(BINARY)-$(VERSION)-linux-amd64.tar.gz \
		-C $(RELEASEDIR) $(BINARY)-linux-amd64 \
		-C $(PWD) systemd/ch-api.service README.md LICENSE 2>/dev/null || \
		tar czf $(RELEASEDIR)/$(BINARY)-$(VERSION)-linux-amd64.tar.gz \
		-C $(RELEASEDIR) $(BINARY)-linux-amd64 \
		-C $(PWD) systemd/ch-api.service
	@tar czf $(RELEASEDIR)/$(BINARY)-$(VERSION)-linux-arm64.tar.gz \
		-C $(RELEASEDIR) $(BINARY)-linux-arm64 \
		-C $(PWD) systemd/ch-api.service README.md LICENSE 2>/dev/null || \
		tar czf $(RELEASEDIR)/$(BINARY)-$(VERSION)-linux-arm64.tar.gz \
		-C $(RELEASEDIR) $(BINARY)-linux-arm64 \
		-C $(PWD) systemd/ch-api.service
	@rm -f $(RELEASEDIR)/$(BINARY)-linux-amd64 $(RELEASEDIR)/$(BINARY)-linux-arm64
	@echo "Release artifacts:"
	@ls -lh $(RELEASEDIR)/*.tar.gz

release-dry-run: ## Simulate a GoReleaser release without publishing
	@echo "Running GoReleaser dry-run (snapshot)..."
	@goreleaser release --snapshot --clean

snapshot: ## Build snapshot artifacts with GoReleaser (no signing, no release)
	@echo "Building snapshot artifacts..."
	@goreleaser release --snapshot --clean

# ---------------------------------------------------------------------------
# Documentation
# ---------------------------------------------------------------------------

docs: ## Generate and open HTML documentation with pkgsite
	@if ! command -v $(PKGSITE) >/dev/null 2>&1; then \
		echo "pkgsite not found, installing..."; \
		go install golang.org/x/pkgsite/cmd/pkgsite@latest; \
	fi
	@echo "Starting pkgsite on http://localhost:8080 ..."
	$(PKGSITE) -open .

swagger: ## Generate OpenAPI spec from inline annotations (swag)
	@if ! command -v $(SWAG) >/dev/null 2>&1; then \
		echo "swag not found, installing..."; \
		go install github.com/swaggo/swag/cmd/swag@latest; \
	fi
	$(SWAG) init -g cmd/ch-api/main.go

# ---------------------------------------------------------------------------
# Clean
# ---------------------------------------------------------------------------

clean: ## Remove build artifacts and release directory
	rm -rf $(BINDIR)/ $(RELEASEDIR)/
