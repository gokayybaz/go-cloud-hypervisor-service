# VMM Client Guide

The `pkg/vmm` package provides a Go client for the [Cloud Hypervisor](https://www.cloudhypervisor.org/) REST API. It supports both **HTTP** and **Unix domain socket** transports, configurable timeouts, exponential backoff retry logic, structured error types with operation context, and trace-ID-aware logging of every VM lifecycle transition.

## Quick Start

```go
import "github.com/org/ch-api/pkg/vmm"

// Connect via Unix socket (typical for local CH instances)
client := vmm.New(vmm.Config{
    Transport:      vmm.TransportUnixSock,
    Address:        "/var/run/ch-api/ch.sock",
    RequestTimeout: 30 * time.Second,
})
defer client.Close()

ctx := context.Background()

// Ping the VMM
if err := client.Ping(ctx); err != nil {
    log.Fatal(err)
}

// Create a VM
cfg := &vmm.VmConfig{
    CPUs: &vmm.CPUConfig{BootVCPUs: 2, MaxVCPUs: 4},
    Memory: &vmm.MemoryConfig{Size: 2 * 1024 * 1024 * 1024},
    Kernel: &vmm.KernelConfig{Path: "/boot/vmlinux"},
}
if err := client.Create(ctx, cfg); err != nil {
    log.Fatal(err)
}

// Boot it
if err := client.Boot(ctx); err != nil {
    log.Fatal(err)
}

// Get VM info
info, err := client.Info(ctx)
if err != nil {
    log.Fatal(err)
}
fmt.Printf("VM state: %s, memory: %d bytes\n", info.State, info.Memory)
```

## Configuration

```go
type Config struct {
    // Transport selects how to reach the CH API.
    Transport TransportType // TransportHTTP or TransportUnixSock

    // Address is either host:port (HTTP) or absolute socket path (Unix).
    Address string

    // RequestTimeout is the per-request deadline. Zero = 30s default.
    RequestTimeout time.Duration

    // RetryPolicy controls automatic retries. Zero = sensible defaults.
    RetryPolicy RetryPolicy

    // Logger is an optional structured logger.  When provided every
    // lifecycle transition is logged with the trace_id from context.
    Logger logging.Logger
}
```

### Retry Policy

```go
type RetryPolicy struct {
    MaxRetries int
    BaseDelay  time.Duration
    MaxDelay   time.Duration
    Multiplier float64
}
```

Defaults:

| Field       | Default | Description                                    |
|-------------|---------|------------------------------------------------|
| MaxRetries  | 3       | Maximum retry attempts (0 = disable retries)   |
| BaseDelay   | 250ms   | Initial delay before first retry               |
| MaxDelay    | 5s      | Cap on delay between retries                   |
| Multiplier  | 2.0     | Exponential factor (delay *= Multiplier)       |

**Retry behaviour:**
- Retries on: connection errors, timeouts, HTTP 429, 500, 502, 503, 504
- Does NOT retry on: 4xx client errors (except 429), context cancellation
- Uses **full jitter**: delay is a random value in `[0, exponential_delay)` to avoid thundering herd

### HTTP Transport Example

```go
client := vmm.New(vmm.Config{
    Transport:      vmm.TransportHTTP,
    Address:        "localhost:8080",
    RequestTimeout: 10 * time.Second,
    RetryPolicy: vmm.RetryPolicy{
        MaxRetries: 5,
        BaseDelay:  100 * time.Millisecond,
        MaxDelay:   2 * time.Second,
        Multiplier: 2.0,
    },
})
```

## API Operations

| Method     | CH Endpoint              | Description            |
|------------|--------------------------|------------------------|
| `Ping`     | `GET /api/v1/vmm.ping`   | Health check           |
| `Version`  | `GET /api/v1/vmm.version`| CH build version       |
| `Create`   | `PUT /api/v1/vm.create`  | Create VM from config  |
| `Boot`     | `PUT /api/v1/vm.boot`    | Boot the VM            |
| `Shutdown` | `PUT /api/v1/vm.shutdown`| Graceful shutdown      |
| `Reboot`   | `PUT /api/v1/vm.reboot`  | Reboot VM              |
| `Pause`    | `PUT /api/v1/vm.pause`   | Pause VM               |
| `Resume`   | `PUT /api/v1/vm.resume`  | Resume paused VM       |
| `Delete`   | `DELETE /api/v1/vm`      | Delete VM              |
| `Info`     | `GET /api/v1/vm.info`    | VM state & resources   |

## Logging Lifecycle Transitions

When a `logging.Logger` is provided in `Config`, every lifecycle method emits structured log lines:

```
{"level":"info","message":"vmm transition","op":"Create","state":"starting","vcpus":2,"memory":2147483648}
{"level":"info","message":"vmm transition","op":"Create","state":"succeeded"}
```

If the operation fails:

```
{"level":"error","message":"vmm transition","op":"Boot","state":"failed","err":"vmm.api[Boot] (HTTP 500): ..."}
```

Retry attempts are logged at `debug` level:

```
{"level":"debug","message":"vmm retry","op":"Ping","attempt":1,"delay":"250ms"}
```

### Trace ID propagation

If the context carries a `trace_id` (injected by `middleware.TraceIDMiddleware`), it is automatically included in every log line:

```go
ctx := logging.WithTraceID(context.Background(), "abc123")
if err := client.Boot(ctx); err != nil {
    // logs include "trace_id":"abc123"
}
```

## Error Handling

All errors returned by the client are of type `*vmm.Error`, which exposes:

```go
type Error struct {
    // Op records the high-level operation that failed, e.g. "Create", "Boot".
    Op string

    // Code classifies the failure mode.
    Code ErrorCode

    // Status is the HTTP status code when Code is ErrCodeAPI or ErrCodeNotFound.
    Status int

    // Message is a human-readable description.
    Message string

    // Body is the raw response body for API errors.
    Body string

    // Cause is the underlying error, if any.
    Cause error
}
```

### Error string format

```
vmm.api[Create] (HTTP 500): PUT /api/v1/vm.create: ...
vmm.connection[Boot]: dial failed
vmm.timeout: request timed out
```

### Inspecting errors

```go
err := client.Boot(ctx)
if err != nil {
    if vmm.IsCode(err, vmm.ErrCodeNotFound) {
        // VM does not exist — create it first
    }
    if vmm.IsCode(err, vmm.ErrCodeRetryExhausted) {
        // CH is unresponsive after all retries
    }
    if vmm.IsCode(err, vmm.ErrCodeTimeout) {
        // Request or network timeout
    }
    if vmm.IsOp(err, "Boot") {
        // Any Boot-specific failure
    }
}
```

### Convenience helpers

| Function | Purpose |
|----------|---------|
| `vmm.IsCode(err, code)` | Checks error code |
| `vmm.IsOp(err, op)` | Checks which operation failed |
| `vmm.IsNotFound(err)` | Checks for 404 or `ErrCodeNotFound` |
| `vmm.IsCreateFailed(err)` | True if `Op == "Create"` |
| `vmm.IsBootFailed(err)` | True if `Op == "Boot"` |
| `vmm.IsPauseFailed(err)` | True if `Op == "Pause"` |
| `vmm.IsResumeFailed(err)` | True if `Op == "Resume"` |
| `vmm.IsShutdownFailed(err)` | True if `Op == "Shutdown"` |
| `vmm.IsRebootFailed(err)` | True if `Op == "Reboot"` |
| `vmm.IsDeleteFailed(err)` | True if `Op == "Delete"` |

### Error codes

| Code            | When it occurs                                              |
|-----------------|-------------------------------------------------------------|
| `connection`    | Dial failure, broken pipe, refused connection               |
| `timeout`       | Request deadline exceeded, network timeout                  |
| `api`           | CH returned HTTP 4xx/5xx                                    |
| `not_found`     | Specialisation of `api` for HTTP 404                        |
| `retry_exhausted`| All retries consumed without success                       |
| `invalid_request`| Failed to marshal request body or build HTTP request        |
| `unknown`       | Catch-all for unclassified failures                         |

## Design Decisions

1. **Standard library only** — The client uses only `net/http`, `context`, `encoding/json`, and other stdlib packages. No external HTTP client libraries are needed.

2. **Unix socket support** — For Unix socket transport, a custom `DialContext` on the `http.Transport` redirects all connections to the Unix socket path. The HTTP `Host` header is a dummy `localhost`; the custom dialer ignores it.

3. **Full jitter backoff** — Exponential backoff alone can cause thundering herd when many clients retry simultaneously. Full jitter (random delay in `[0, exp_delay)`) smooths the retry distribution.

4. **Drain-and-close on retry** — When retrying after an HTTP 5xx response, the response body is fully consumed and the connection closed before the retry. This prevents connection pool exhaustion.

5. **Context-aware** — Every method accepts a `context.Context`. Cancellation or deadline expiry during a retry backoff is honoured immediately.

6. **Structured errors with operation context** — `*Error` carries both a machine-readable `Code` (failure mode) and an `Op` (which operation failed). This lets callers distinguish "Boot failed because VM not found" from "Delete failed because VM not found" without parsing error messages.

7. **Trace-ID-aware logging** — When a `logging.Logger` is configured, the client derives a contextual logger via `logger.WithContext(ctx)` for every lifecycle transition. This automatically injects `trace_id` into log lines when the context carries one.

## Types

The `VmConfig` struct (and its nested types) mirrors a practical subset of the Cloud Hypervisor REST API schema:

- `VmConfig` — top-level VM configuration
- `CPUConfig` / `CPUTopology` — vCPU layout
- `MemoryConfig` / `MemoryZoneConfig` — guest RAM
- `KernelConfig` — kernel image path
- `DiskConfig` — block devices
- `NetConfig` — network interfaces
- `ConsoleConfig` — serial / virtio-console
- `PayloadConfig` — firmware payload
- `BalloonConfig` — memory balloon
- `RngConfig` — entropy device

These structs are tagged for JSON marshalling and can be round-tripped through `json.Marshal` / `json.Unmarshal`.

## Limitations

- The client does not yet support **vhost-user** socket passthrough (the types are present but the transport layer does not handle it).
- **mTLS / TLS** is not implemented; the HTTP transport is plain TCP.
- The `vm.info` response type is a subset; fields added in newer CH versions may require type updates.
- Retry body replay is not implemented. Because the CH API uses small JSON bodies, the client rebuilds the request body on each retry. For large payloads, a `io.Seeker` body would be needed.