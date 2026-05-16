# API Documentation

## Overview

The HTTP server is built on the [chi](https://github.com/go-chi/chi) router (v5). All versioned REST endpoints live under `/api/v1/`. The root router can be used for non-versioned paths such as `/healthz` and `/metrics`.

## Router Setup

```go
import (
    "github.com/org/ch-api/pkg/api"
    "github.com/org/ch-api/pkg/audit"
    "github.com/org/ch-api/pkg/logging"
    "github.com/org/ch-api/pkg/metrics"
)

logger := logging.New("info")
auditor, _ := audit.New("data/audit")
mr := metrics.New()
router := api.NewRouter(logger, auditor, mr, nil, nil)

// Register versioned routes.
router.V1().Get("/vms", listVMsHandler)
router.V1().Post("/vms", createVMHandler)
router.V1().Get("/vms/{id}", getVMHandler)

// Register non-versioned routes.
router.Root().Get("/healthz", healthHandler)

// Create and start the server.
cfg := api.DefaultServerConfig()
cfg.Port = "8080"
srv := api.NewServer(cfg, router.Handler())
srv.ListenAndServe()
```

## Middleware Stack

The middleware is applied in the following order (outermost first):

**Root router:**
```
PanicRecovery → TraceID → Audit → AccessLogger → Metrics → Timeout → Handler
```

**`/api/v1` sub-router (when auth is enabled):**
```
Auth → RBAC → Handler
```

Each middleware is described below.

### 1. PanicRecovery

**File:** `pkg/api/router.go`

Catches panics from any downstream handler or middleware. Logs the panic value and full stack trace via the structured logger, then writes a `500 Internal Server Error` response. The server remains operational — subsequent requests are not affected.

**Log fields emitted on panic:**
- `panic` — the recovered value (`fmt.Sprintf("%v", rec)`)
- `stack` — full goroutine stack trace (`debug.Stack()`)
- `method` — HTTP method of the request that panicked
- `path` — request path

### 2. TraceID

**File:** `pkg/api/router.go` (reuses logic from `pkg/middleware/middleware.go`)

Injects a `trace_id` into the request context for distributed tracing.

**Behaviour:**
- Reads the `X-Request-ID` header from the incoming request.
- If the header is absent, generates a random 16-byte hex trace ID.
- Stores the trace ID in the request context via `logging.WithTraceID`.
- Sets the `X-Request-ID` header on the response so the client can correlate logs.

**Downstream usage:**
Handlers and services call `logger.WithContext(r.Context())` to obtain a child logger that automatically emits `trace_id` on every log line.

### 3. AccessLogger

**File:** `pkg/api/router.go`

Records a single structured access log line for every request after it completes.

**Log fields emitted:**
- `method` — HTTP method (`GET`, `POST`, etc.)
- `path` — request path
- `remote_addr` — client address
- `status` — HTTP status code
- `duration_ms` — total request duration in milliseconds
- `bytes_written` — response body size in bytes
- `trace_id` — propagated from the TraceID middleware (via request context)

**Design note:** TraceID is placed *before* AccessLogger in the stack so that the access log can read the trace_id from the request context.

### 4. Timeout

**File:** `pkg/api/router.go`

Enforces a per-request deadline (default 30 seconds). If the handler does not complete within the limit, the client receives `504 Gateway Timeout` and the request context is cancelled.

**Behaviour:**
- Creates a `context.WithTimeout` from the request context.
- Runs the handler in a separate goroutine so the timeout can be monitored.
- If the deadline is exceeded before the handler finishes, writes `504 Gateway Timeout`.
- If the handler panics in the goroutine, the panic is recovered and re-raised in the parent goroutine so that the PanicRecovery middleware can handle it.

**Handler responsibility:** Well-behaved handlers should check `r.Context().Done()` and return early when cancelled.

## Server Configuration

`api.ServerConfig` exposes tunable timeouts:

| Field | Default | Description |
|-------|---------|-------------|
| `Host` | `""` (all interfaces) | Bind address |
| `Port` | `"8080"` | Listen port |
| `ReadTimeout` | `30s` | Maximum duration for reading the entire request |
| `WriteTimeout` | `30s` | Maximum duration before timing out writes |
| `IdleTimeout` | `120s` | Maximum duration to wait for the next request on keep-alive |

## Error Responses

All error responses follow [RFC 7807 Problem Details](https://tools.ietf.org/html/rfc7807) with `Content-Type: application/problem+json`.

**Example validation error (400):**

```json
{
  "type": "https://github.com/org/ch-api/errors/invalid-request",
  "title": "Invalid Request",
  "status": 400,
  "detail": "request body validation failed",
  "instance": "/api/v1/vms",
  "errors": [
    {"field": "name", "message": "name is required"},
    {"field": "cpus.boot_vcpus", "message": "must be >= 1"},
    {"field": "memory.size", "message": "must be >= 64 MB"}
  ]
}
```

**Example not found (404):**

```json
{
  "type": "https://github.com/org/ch-api/errors/not-found",
  "title": "Not Found",
  "status": 404,
  "detail": "vm \"vm-xyz\" not found",
  "instance": "/api/v1/vms/vm-xyz"
}
```

## Endpoints

### GET /healthz

Health check endpoint.

**Response:** `200 OK` — body: `ok`

### GET /api/v1/status

Service status endpoint.

**Response:** `200 OK` — body: `running`

### POST /api/v1/vms

Create a new VM.

**Request body (JSON):**

```json
{
  "name": "my-vm",
  "cpus": {
    "boot_vcpus": 2,
    "max_vcpus": 4
  },
  "memory": {
    "size": 1024
  },
  "kernel": {
    "path": "/boot/vmlinuz"
  },
  "disks": [
    {"path": "/disk.raw", "readonly": false, "direct": false}
  ],
  "net": [
    {"tap": "tap0", "ip": "10.0.0.2", "mac": "02:00:00:00:00:01"}
  ],
  "console": {"mode": "Tty"},
  "serial": {"mode": "Off"}
}
```

**Validation rules:**

| Field | Rule |
|-------|------|
| `name` | Required, non-empty |
| `cpus` | Required |
| `cpus.boot_vcpus` | Required, >= 1 |
| `cpus.max_vcpus` | Required, >= `boot_vcpus` |
| `memory` | Required |
| `memory.size` | Required, >= 64 (MB) |
| `kernel` | Required |
| `kernel.path` | Required, non-empty |
| `disks` | Required, at least one element |
| `disks[*].path` | Required, non-empty |
| `net` | Optional |
| `net[*]` | At least one of `tap`, `ip`, or `mac` must be present |

**Response:** `201 Created` — `VMResponse` JSON

### GET /api/v1/vms

List all VMs.

**Response:** `200 OK` — array of `VMResponse` JSON

### GET /api/v1/vms/{id}

Get a single VM by ID.

**Response:** `200 OK` — `VMResponse` JSON

**Error:** `404 Not Found` if the VM does not exist.

### DELETE /api/v1/vms/{id}

Delete a VM by ID.

**Response:** `204 No Content`

**Error:** `404 Not Found` if the VM does not exist.

---

### POST /api/v1/vms/{id}/boot

Boot the VM.

**Response:** `204 No Content`

**Error:** `404 Not Found` if the VM does not exist. `500 Internal Server Error` if the VMM call fails.

### POST /api/v1/vms/{id}/pause

Pause the VM.

**Response:** `204 No Content`

### POST /api/v1/vms/{id}/resume

Resume a paused VM.

**Response:** `204 No Content`

### POST /api/v1/vms/{id}/shutdown

Shut down the VM.

**Response:** `204 No Content`

### POST /api/v1/vms/{id}/reboot

Reboot the VM.

**Response:** `204 No Content`

**Operation Logging:** Every lifecycle call is recorded in the VM operation log with `timestamp`, `vm_id`, `operation`, `user` (from `X-User-ID` header, defaults to `anonymous`), `outcome` (`success`/`error`), and `message`.

---

### POST /api/v1/vms/{id}/disks

Attach a disk to the VM.

**Request body:**

```json
{"path": "/extra.raw", "readonly": false, "direct": false}
```

**Response:** `201 Created` — `DiskResponse` JSON

### DELETE /api/v1/vms/{id}/disks/{disk_id}

Detach a disk by index (e.g. `disk-0`).

**Response:** `200 OK` — current disk list

### POST /api/v1/vms/{id}/disks/{disk_id}/snapshot

Create a snapshot of the disk.

**Response:** `200 OK` — current disk list

---

### POST /api/v1/vms/{id}/interfaces

Add a network interface.

**Request body:**

```json
{"tap": "tap0", "ip": "10.0.0.2/24", "mac": "02:00:00:00:00:01"}
```

**Validation:** MAC is checked with `net.ParseMAC`. IP/CIDR is checked with `net.ParseCIDR` or `net.ParseIP`.

**Response:** `201 Created` — `InterfaceResponse` JSON

### DELETE /api/v1/vms/{id}/interfaces/{iface_id}

Remove a network interface by ID (e.g. `eth0`).

**Response:** `200 OK` — current interface list

### PATCH /api/v1/vms/{id}/interfaces/{iface_id}

Update fields of an existing interface. Only provided fields are overwritten.

**Request body:**

```json
{"ip": "192.168.1.10"}
```

**Response:** `200 OK` — current interface list

---

### PATCH /api/v1/vms/{id}/cpu

Resize vCPU count.

**Request body:**

```json
{"count": 4}
```

**Response:** `200 OK`

```json
{"requested": 4, "effective": 4}
```

**Clamping:** If the requested value exceeds `Limits`, it is clamped and a warning is logged.

### PATCH /api/v1/vms/{id}/memory

Resize memory in MB.

**Request body:**

```json
{"size_mb": 2048}
```

**Response:** `200 OK`

```json
{"requested_mb": 2048, "effective_mb": 2048}
```

**Clamping:** Values below 64 MB are rejected. Values outside min/max limits are clamped with a warning log. Values are aligned to 4 MB.

---

### GET /api/v1/vms/{id}/console

Stream VM serial console output over WebSocket.

**Protocol:** WebSocket (`ws://` or `wss://`)

**Behaviour:**
- Upgrades the HTTP connection to WebSocket.
- Streams periodic console text messages.
- Handles client disconnect gracefully (GoingAway, AbnormalClosure).
- Logs session start and end with duration and lines sent.

**Response:** WebSocket text messages.

**Error:** `404 Not Found` if the VM does not exist (before WebSocket upgrade).

## Data Types

### VMResponse

```json
{
  "id": "vm-1715467200000000000",
  "name": "my-vm",
  "status": "created",
  "created_at": "2026-05-12T00:00:00.000000000Z",
  "config": {
    "cpus": {"boot_vcpus": 2, "max_vcpus": 4},
    "memory": {"size": 1024},
    "kernel": {"path": "/boot/vmlinuz"},
    "disks": [{"path": "/disk.raw"}],
    "net": [{"tap": "tap0"}]
  }
}
```

## Route Patterns

chi supports expressive URL patterns:

- Static segments: `/vms`
- URL parameters: `/vms/{id}` (read via `chi.URLParam(r, "id")`)
- Wildcards: `/files/{path:*}`

HTTP method helpers are available: `Get`, `Post`, `Put`, `Patch`, `Delete`, `Head`, `Options`.
