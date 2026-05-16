# Testing

This document describes the testing philosophy, tooling, and conventions used
in the Cloud Hypervisor API project.

## Philosophy

Every package that contains non-trivial logic must have unit tests.  Tests are
written using the standard `testing` package and run with `go test`.  The
project targets **80%+ line coverage** for all packages.

## Running Tests

### All packages

```bash
go test ./...
```

### With race detector

```bash
go test -race ./...
```

The race detector is **required** to pass on CI.  It catches data races in
the rate limiter, audit middleware, and any other concurrent code.

### With coverage

```bash
go test -coverprofile=coverage.out ./...
go tool cover -func=coverage.out
```

To view coverage in a browser:

```bash
go tool cover -html=coverage.out
```

### Single package

```bash
go test -v ./pkg/vmm/...
```

### Single test

```bash
go test -v -run TestClientPing ./pkg/vmm/...
```

## Test Conventions

### Naming

- Test functions: `Test<Struct><Method>` or `Test<Function>`
- Table-driven sub-tests: `t.Run("<scenario>", ...)`
- Example: `TestClientPing`, `TestClientRetryOn5xx`

### Table-driven tests

Use table-driven tests for functions with multiple scenarios:

```go
func TestShouldRetryStatus(t *testing.T) {
    cases := []struct {
        code int
        want bool
    }{
        {429, true},
        {500, true},
        {200, false},
        {404, false},
    }
    for _, c := range cases {
        got := shouldRetryStatus(c.code)
        if got != c.want {
            t.Fatalf("shouldRetryStatus(%d) = %v, want %v", c.code, got, c.want)
        }
    }
}
```

### Mock servers

#### HTTP mock server

Use `httptest.NewServer` for HTTP-based tests:

```go
srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    if r.URL.Path != "/api/v1/vmm.ping" {
        t.Errorf("unexpected path: %s", r.URL.Path)
    }
    w.WriteHeader(http.StatusOK)
}))
defer srv.Close()
```

#### Unix socket mock server

For packages that communicate over Unix domain sockets, create a real Unix
socket listener:

```go
dir := t.TempDir()
sockPath := dir + "/ch.sock"

listener, err := net.Listen("unix", sockPath)
if err != nil {
    t.Fatalf("listen unix: %v", err)
}
defer listener.Close()

mux := http.NewServeMux()
mux.HandleFunc("/api/v1/vmm.ping", func(w http.ResponseWriter, r *http.Request) {
    w.WriteHeader(http.StatusOK)
})
go http.Serve(listener, mux)
```

The client is configured with `Transport: TransportUnixSock` and
`Address: sockPath`.

### Testing error paths

Always test both success and failure paths.  For HTTP clients this means:

- Success (200 OK)
- Client errors (400 Bad Request)
- Server errors (500 Internal Server Error)
- Not found (404)
- Network errors (connection refused)
- Timeouts (context deadline exceeded)
- Retry exhaustion

### Testing concurrency

When testing code that uses goroutines or shared state:

1. Run with `-race` to detect data races.
2. Use `sync.WaitGroup` to synchronize goroutines in tests.
3. Use channels to observe side effects from goroutines.

Example from the rate limiter tests:

```go
func TestLimiterCleanup(t *testing.T) {
    l := NewLimiter(cfg, logger)
    defer l.Stop()

    // Make a request to create a window entry.
    // ...

    // Wait for cleanup to run.
    time.Sleep(window + 100*time.Millisecond)

    l.mu.Lock()
    after := len(l.windows)
    l.mu.Unlock()
    if after != 0 {
        t.Fatalf("expected 0 entries after cleanup, got %d", after)
    }
}
```

### Test helpers

Use `t.Helper()` in helper functions so that failure messages point to the
calling test line:

```go
func generateTestCert(t *testing.T) (certFile, keyFile string) {
    t.Helper()
    // ...
}
```

## Coverage by Package

| Package | Coverage | Notes |
|---------|----------|-------|
| `pkg/vmm` | **98.1%** | HTTP + Unix socket mock servers; retry, error, and transport paths |
| `pkg/api` | ~85% | Router construction, middleware ordering, panic recovery, timeout |
| `pkg/auth` | ~95% | JWT validation, RBAC role hierarchy, context injection |
| `pkg/ratelimit` | ~90% | Sliding window, dual-key (IP + token), cleanup |
| `pkg/tls` | ~95% | Manual certs, ACME manager, cipher suite validation |
| `pkg/preflight` | ~85% | Kernel version, capabilities, socket permissions |
| `internal/config` | ~80% | Defaults, env var binding, file loading |
| `pkg/lifecycle` | ~80% | Start/stop sequences, timeout propagation |
| `pkg/logging` | ~80% | Trace ID generation, context propagation |
| `pkg/metrics` | ~80% | Registry, middleware, error counters |
| `pkg/resources` | ~85% | Limit clamping, alignment |
| `pkg/storage` | ~85% | File copy, checksums |
| `pkg/network` | ~85% | CIDR parsing, MAC validation |
| `pkg/image` | ~85% | Format detection, checksum validation |
| `pkg/eventlog` | ~80% | File rotation, syslog fallback |
| `pkg/pprof` | ~80% | Handler registration |

## Known Gaps

- `pkg/audit` — middleware is tested indirectly via handler tests.  Direct
  SQLite assertion tests would increase coverage.
- `cmd/ch-api` — no unit tests for `main()`.  Integration tests are the
  preferred approach for the binary entry point.

## CI Checklist

Before merging any PR:

1. `go test ./...` passes
2. `go test -race ./...` passes
3. `go vet ./...` is clean
4. `go build ./cmd/ch-api` succeeds
5. All new code has tests
6. Coverage for modified packages remains above 80%
