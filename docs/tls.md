# TLS Support

The API server can serve traffic over HTTPS with either manually supplied
certificates or automatic certificate provisioning via Let's Encrypt (ACME).

## Configuration

TLS is disabled by default.  Enable it via configuration:

```yaml
server:
  port: "8443"

tls:
  enabled: true
  cert_file: "/etc/ch-api/tls/cert.pem"
  key_file: "/etc/ch-api/tls/key.pem"
```

Or via environment variables:

```bash
export CH_API_TLS_ENABLED=true
export CH_API_TLS_CERT_FILE=/etc/ch-api/tls/cert.pem
export CH_API_TLS_KEY_FILE=/etc/ch-api/tls/key.pem
```

## Manual Certificates

Provide the paths to a PEM-encoded certificate and its corresponding private
key.  The certificate can be a single server certificate or a chain
(certificate followed by intermediate CAs).

```yaml
tls:
  enabled: true
  cert_file: "/etc/ch-api/tls/cert.pem"
  key_file: "/etc/ch-api/tls/key.pem"
```

### Generating a Self-Signed Certificate

For development or internal use:

```bash
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 \
  -keyout key.pem -out cert.pem -days 365 -nodes \
  -subj "/CN=localhost" -addext "subjectAltName=DNS:localhost,IP:127.0.0.1"
```

## Automatic ACME (Let's Encrypt)

Enable automatic certificate provisioning.  The server will obtain and renew
certificates from Let's Encrypt using the ACME v2 protocol.

```yaml
tls:
  enabled: true
  acme_enabled: true
  acme_email: "admin@example.com"
  acme_domains:
    - "api.example.com"
  acme_cache: "data/certs"
```

Or via environment variables:

```bash
export CH_API_TLS_ENABLED=true
export CH_API_TLS_ACME_ENABLED=true
export CH_API_TLS_ACME_EMAIL=admin@example.com
export CH_API_TLS_ACME_DOMAINS=api.example.com
export CH_API_TLS_ACME_CACHE=data/certs
```

### ACME Requirements

* The server must be reachable on **port 80** for HTTP-01 challenges.
* The configured domains must resolve to the server's public IP.
* The `acme_email` is used for Let's Encrypt account registration and expiry
  notifications.

### ACME HTTP-01 Challenge Server

When ACME is enabled the application starts a secondary HTTP server on
**port 80** that handles ACME HTTP-01 challenges automatically.  No manual
intervention is required.

### ACME Cache

Obtained certificates are cached on disk so that restart does not trigger
re-issuance.  The default cache directory is `data/certs`.  Ensure this
directory is writable and persists across restarts.

## TLS Hardening

The following hardening is applied unconditionally when TLS is enabled:

| Setting | Value | Rationale |
|---------|-------|-----------|
| **Minimum version** | TLS 1.2 | TLS 1.0 and 1.1 are deprecated (RFC 8996). |
| **Cipher suites** | Only AEAD with forward secrecy | CBC and RSA key exchange are excluded. |
| **Server preference** | Enabled | Server selects cipher suite, not client. |

### Allowed Cipher Suites

* `TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256`
* `TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256`
* `TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384`
* `TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384`
* `TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305`
* `TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305`

## Port Recommendation

| Mode | Recommended Port | Notes |
|------|-----------------|-------|
| Plain HTTP | `8080` | Default when TLS is disabled. |
| TLS (manual) | `8443` | Common alternative-HTTPS port. |
| TLS (ACME) | `443` | Required for Let's Encrypt in production. |

When ACME is enabled you should bind to port `443` so that Let's Encrypt
can serve HTTPS directly:

```yaml
server:
  port: "443"
```

## Disabling TLS

TLS is disabled by default.  To explicitly disable it:

```yaml
tls:
  enabled: false
```

## Lifecycle Integration

The TLS configuration is evaluated at startup.  If the certificate files are
missing or invalid the server exits immediately with a clear error message.
When ACME is enabled the server starts the HTTP-01 challenge handler before
opening the HTTPS listener.

Both the main HTTPS server and the ACME challenge handler participate in
graceful shutdown when the process receives `SIGINT` or `SIGTERM`.
