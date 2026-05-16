# Logging Guide

The application uses [`zerolog`](https://github.com/rs/zerolog) for high-performance structured JSON logging.  Every log line automatically contains the following fields:

| Field       | Source                              | Notes |
|-------------|-------------------------------------|-------|
| `timestamp` | `time.Now()` formatted RFC3339Nano  | Always present |
| `level`     | zerolog level (`info`, `error`, …)  | Always present |
| `caller`    | `file:line` of the call site        | Always present |
| `hostname`  | `os.Hostname()`                     | Always present |
| `trace_id`  | request context (see below)         | Present when `WithContext` is used |

## Quick Start

```go
import "github.com/org/ch-api/pkg/logging"

logger := logging.New("info")

logger.Info("server started", "addr", ":8080", "version", "1.0.0")
logger.Error("database connection failed", "err", err)
```

Example JSON output:

```json
{
  "timestamp": "2024-05-12T14:32:01.123456789Z",
  "level": "info",
  "caller": "cmd/ch-api/main.go:58",
  "hostname": "vm-host-01",
  "message": "server started",
  "addr": ":8080",
  "version": "1.0.0"
}
```

## Levels

Valid level strings (case-insensitive):

- `debug`
- `info`  (default)
- `warn`
- `error`
- `fatal`
- `panic`

Set via the `LOG_LEVEL` environment variable or pass directly to `logging.New`.

## Console (Pretty) Output

During local development set `LOG_FORMAT=console` to emit human-readable lines instead of JSON:

```bash
LOG_FORMAT=console go run ./cmd/ch-api
# 2024-05-12T14:32:01Z INF cmd/ch-api/main.go:58 > server started addr=:8080 version=1.0.0
```

## Adding Persistent Fields

Use `With` to create a child logger that carries extra fields on every line:

```go
svcLogger := logger.With("component", "service", "svc", "vm-manager")
svcLogger.Info("created VM") // automatically includes component and svc
```

## Trace IDs

Trace IDs tie together all logs that belong to a single request.  They are automatically injected when you derive a logger from an HTTP request context.

### HTTP middleware

The application already registers `middleware.TraceIDMiddleware` in `cmd/ch-api/main.go`.  It performs two things:

1. Reads the `X-Request-ID` header from incoming requests (or generates a random UUID if absent).
2. Stores the trace ID in `context.Context` so downstream code can access it.

### Inside handlers

Always derive the logger from the request context:

```go
func MyHandler(w http.ResponseWriter, r *http.Request) {
    log := logger.WithContext(r.Context())
    log.Info("handling request", "method", r.Method, "path", r.URL.Path)
    // ^ every line includes trace_id
}
```

Example output:

```json
{
  "timestamp": "2024-05-12T14:32:01.123456789Z",
  "level": "info",
  "caller": "internal/handler/handler.go:20",
  "hostname": "vm-host-01",
  "trace_id": "a1b2c3d4e5f67890",
  "message": "handling request",
  "method": "GET",
  "path": "/api/v1/status"
}
```

### Manual trace ID injection

For background workers or gRPC handlers (where HTTP middleware does not run), create a context manually:

```go
ctx := logging.WithTraceID(context.Background(), logging.GenerateTraceID())
log := logger.WithContext(ctx)
log.Info("background job started")
```

### Propagating trace IDs between services

When making outbound HTTP calls, copy the trace ID from the incoming request context into the `X-Request-ID` header of the outgoing request:

```go
traceID := logging.TraceIDFromContext(ctx)
req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
req.Header.Set("X-Request-ID", traceID)
```

## Context Helper Reference

| Function | Purpose |
|----------|---------|
| `logging.GenerateTraceID()` | Create a new random trace ID |
| `logging.WithTraceID(ctx, id)` | Attach a trace ID to a `context.Context` |
| `logging.TraceIDFromContext(ctx)` | Retrieve the trace ID from a context |

## Design Decisions

1. **Interface over concrete type** — `logging.Logger` is an interface. Callers depend on the interface, not on zerolog. This keeps the door open to swapping the underlying library without touching business logic.
2. **No global logger** — Every component receives its logger via dependency injection. This makes unit tests trivial: pass a `NoOp` implementation or capture logs in a buffer.
3. **Caller skip tuning** — `zerolog.CallerSkipFrameCount` is set to `3` in `init()` so that `caller` points to the business code that invoked `logger.Info()`, not to the zerolog internals or the `logging` wrapper.
4. **JSON by default** — Production environments consume logs with `jq`, Loki, or Splunk. JSON is the default; pretty console output is strictly opt-in via `LOG_FORMAT=console`.