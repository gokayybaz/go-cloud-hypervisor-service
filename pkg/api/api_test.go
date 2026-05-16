package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/gokaybaz/go-cloud-hypervisor-service/pkg/logging"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

type noopLogger struct{}

func (noopLogger) Info(string, ...any)  {}
func (noopLogger) Error(string, ...any) {}
func (noopLogger) Debug(string, ...any) {}
func (noopLogger) Warn(string, ...any)  {}
func (n noopLogger) WithContext(context.Context) logging.Logger { return n }
func (n noopLogger) With(...any) logging.Logger                 { return n }

type captureLogger struct {
	msgs []logMsg
}

type logMsg struct {
	level string
	msg   string
	args  map[string]any
}

func (c *captureLogger) Info(msg string, args ...any)  { c.record("info", msg, args...) }
func (c *captureLogger) Error(msg string, args ...any) { c.record("error", msg, args...) }
func (c *captureLogger) Debug(msg string, args ...any) { c.record("debug", msg, args...) }
func (c *captureLogger) Warn(msg string, args ...any)  { c.record("warn", msg, args...) }

func (c *captureLogger) WithContext(ctx context.Context) logging.Logger {
	traceID := logging.TraceIDFromContext(ctx)
	if traceID == "" {
		return c
	}
	return &captureLoggerWithFields{parent: c, fields: map[string]any{"trace_id": traceID}}
}

func (c *captureLogger) With(fields ...any) logging.Logger {
	m := make(map[string]any)
	for i := 0; i < len(fields)-1; i += 2 {
		if k, ok := fields[i].(string); ok {
			m[k] = fields[i+1]
		}
	}
	return &captureLoggerWithFields{parent: c, fields: m}
}

func (c *captureLogger) record(level, msg string, args ...any) {
	m := make(map[string]any)
	for i := 0; i < len(args)-1; i += 2 {
		if k, ok := args[i].(string); ok {
			m[k] = args[i+1]
		}
	}
	c.msgs = append(c.msgs, logMsg{level: level, msg: msg, args: m})
}

type captureLoggerWithFields struct {
	parent *captureLogger
	fields map[string]any
}

func (c *captureLoggerWithFields) Info(msg string, args ...any)  { c.record("info", msg, args...) }
func (c *captureLoggerWithFields) Error(msg string, args ...any) { c.record("error", msg, args...) }
func (c *captureLoggerWithFields) Debug(msg string, args ...any) { c.record("debug", msg, args...) }
func (c *captureLoggerWithFields) Warn(msg string, args ...any)  { c.record("warn", msg, args...) }
func (c *captureLoggerWithFields) WithContext(ctx context.Context) logging.Logger {
	traceID := logging.TraceIDFromContext(ctx)
	if traceID == "" {
		return c
	}
	cp := &captureLoggerWithFields{parent: c.parent, fields: make(map[string]any, len(c.fields)+1)}
	for k, v := range c.fields {
		cp.fields[k] = v
	}
	cp.fields["trace_id"] = traceID
	return cp
}
func (c *captureLoggerWithFields) With(fields ...any) logging.Logger {
	m := make(map[string]any, len(c.fields))
	for k, v := range c.fields {
		m[k] = v
	}
	for i := 0; i < len(fields)-1; i += 2 {
		if k, ok := fields[i].(string); ok {
			m[k] = fields[i+1]
		}
	}
	return &captureLoggerWithFields{parent: c.parent, fields: m}
}

func (c *captureLoggerWithFields) record(level, msg string, args ...any) {
	m := make(map[string]any, len(c.fields))
	for k, v := range c.fields {
		m[k] = v
	}
	for i := 0; i < len(args)-1; i += 2 {
		if k, ok := args[i].(string); ok {
			m[k] = args[i+1]
		}
	}
	c.parent.msgs = append(c.parent.msgs, logMsg{level: level, msg: msg, args: m})
}

// ---------------------------------------------------------------------------
// Router construction
// ---------------------------------------------------------------------------

func TestNewRouterCreatesV1Subrouter(t *testing.T) {
	router := NewRouter(noopLogger{}, nil, nil, nil, nil)
	v1 := router.V1()
	v1.Get("/test", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("v1 ok"))
	})

	ts := httptest.NewServer(router.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/test")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "v1 ok" {
		t.Fatalf("expected 'v1 ok', got %q", string(body))
	}
}

func TestNewRouterRootPath(t *testing.T) {
	router := NewRouter(noopLogger{}, nil, nil, nil, nil)
	router.Root().Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	ts := httptest.NewServer(router.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestV1RouteNotFound(t *testing.T) {
	router := NewRouter(noopLogger{}, nil, nil, nil, nil)
	ts := httptest.NewServer(router.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/nonexistent")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// Trace-ID middleware
// ---------------------------------------------------------------------------

func TestTraceIDInjectedFromHeader(t *testing.T) {
	router := NewRouter(noopLogger{}, nil, nil, nil, nil)
	var captured string
	router.V1().Get("/trace", func(w http.ResponseWriter, r *http.Request) {
		captured = logging.TraceIDFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	ts := httptest.NewServer(router.Handler())
	defer ts.Close()

	req, _ := http.NewRequest("GET", ts.URL+"/api/v1/trace", nil)
	req.Header.Set("X-Request-ID", "my-trace-123")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if captured != "my-trace-123" {
		t.Fatalf("expected trace_id 'my-trace-123', got %q", captured)
	}
	if resp.Header.Get("X-Request-ID") != "my-trace-123" {
		t.Fatalf("expected response header X-Request-ID 'my-trace-123', got %q", resp.Header.Get("X-Request-ID"))
	}
}

func TestTraceIDGeneratedWhenMissing(t *testing.T) {
	router := NewRouter(noopLogger{}, nil, nil, nil, nil)
	var captured string
	router.V1().Get("/trace", func(w http.ResponseWriter, r *http.Request) {
		captured = logging.TraceIDFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	ts := httptest.NewServer(router.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/trace")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if captured == "" {
		t.Fatal("expected generated trace_id, got empty")
	}
	if resp.Header.Get("X-Request-ID") == "" {
		t.Fatal("expected response header X-Request-ID to be set")
	}
}

// ---------------------------------------------------------------------------
// Access logging middleware
// ---------------------------------------------------------------------------

func TestAccessLoggerRecordsRequest(t *testing.T) {
	log := &captureLogger{}
	router := NewRouter(log, nil, nil, nil, nil)
	router.V1().Get("/resource", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte("created"))
	})

	ts := httptest.NewServer(router.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/resource")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	var found bool
	for _, m := range log.msgs {
		if m.msg == "http access" {
			found = true
			if m.args["method"] != "GET" {
				t.Fatalf("expected method GET, got %v", m.args["method"])
			}
			if m.args["path"] != "/api/v1/resource" {
				t.Fatalf("expected path /api/v1/resource, got %v", m.args["path"])
			}
			if m.args["status"] != http.StatusCreated {
				t.Fatalf("expected status %d, got %v", http.StatusCreated, m.args["status"])
			}
			if m.args["bytes_written"] != 7 {
				t.Fatalf("expected bytes_written 7, got %v", m.args["bytes_written"])
			}
			// Duration should be present and non-negative.
			if _, ok := m.args["duration_ms"]; !ok {
				t.Fatal("expected duration_ms in access log")
			}
		}
	}
	if !found {
		t.Fatal("expected 'http access' log entry")
	}
}

func TestAccessLoggerIncludesTraceID(t *testing.T) {
	log := &captureLogger{}
	router := NewRouter(log, nil, nil, nil, nil)
	router.V1().Get("/resource", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	ts := httptest.NewServer(router.Handler())
	defer ts.Close()

	req, _ := http.NewRequest("GET", ts.URL+"/api/v1/resource", nil)
	req.Header.Set("X-Request-ID", "trace-abc")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	var found bool
	for _, m := range log.msgs {
		if m.msg == "http access" {
			found = true
			if m.args["trace_id"] != "trace-abc" {
				t.Fatalf("expected trace_id 'trace-abc', got %v", m.args["trace_id"])
			}
		}
	}
	if !found {
		t.Fatal("expected 'http access' log entry with trace_id")
	}
}

// ---------------------------------------------------------------------------
// Panic recovery middleware
// ---------------------------------------------------------------------------

func TestPanicRecoveryReturns500(t *testing.T) {
	router := NewRouter(noopLogger{}, nil, nil, nil, nil)
	router.V1().Get("/panic", func(w http.ResponseWriter, r *http.Request) {
		panic("intentional panic")
	})

	ts := httptest.NewServer(router.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/panic")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "Internal Server Error") {
		t.Fatalf("expected 'Internal Server Error' in body, got %q", string(body))
	}
}

func TestPanicRecoveryLogsError(t *testing.T) {
	log := &captureLogger{}
	router := NewRouter(log, nil, nil, nil, nil)
	router.V1().Get("/panic", func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	})

	ts := httptest.NewServer(router.Handler())
	defer ts.Close()

	http.Get(ts.URL + "/api/v1/panic")

	var found bool
	for _, m := range log.msgs {
		if m.level == "error" && m.msg == "panic recovered" {
			found = true
			if m.args["panic"] != "boom" {
				t.Fatalf("expected panic 'boom', got %v", m.args["panic"])
			}
			if m.args["stack"] == "" {
				t.Fatal("expected non-empty stack trace")
			}
		}
	}
	if !found {
		t.Fatal("expected panic recovery log entry")
	}
}

func TestPanicRecoveryDoesNotCrashServer(t *testing.T) {
	router := NewRouter(noopLogger{}, nil, nil, nil, nil)
	router.V1().Get("/panic", func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	})
	router.V1().Get("/ok", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})

	ts := httptest.NewServer(router.Handler())
	defer ts.Close()

	// First request panics.
	resp1, _ := http.Get(ts.URL + "/api/v1/panic")
	if resp1 != nil {
		resp1.Body.Close()
	}

	// Second request should still work.
	resp2, err := http.Get(ts.URL + "/api/v1/ok")
	if err != nil {
		t.Fatalf("second request failed: %v", err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on second request, got %d", resp2.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// Timeout middleware
// ---------------------------------------------------------------------------

func TestTimeoutReturns504(t *testing.T) {
	// Use a very short timeout.
	log := &captureLogger{}
	root := chi.NewRouter()
	root.Use(PanicRecovery(log))
	root.Use(AccessLogger(log))
	root.Use(traceIDMiddleware)
	root.Use(Timeout(50 * time.Millisecond))
	root.Get("/slow", func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(200 * time.Millisecond):
			w.Write([]byte("too late"))
		case <-r.Context().Done():
			// Context cancelled; handler may or may not finish before
			// the timeout middleware writes 504.
		}
	})

	ts := httptest.NewServer(root)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/slow")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	// The timeout middleware writes 504 when the deadline is exceeded.
	if resp.StatusCode != http.StatusGatewayTimeout {
		t.Fatalf("expected 504, got %d", resp.StatusCode)
	}
}

func TestTimeoutAllowsFastRequest(t *testing.T) {
	root := chi.NewRouter()
	root.Use(Timeout(5 * time.Second))
	root.Get("/fast", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("quick"))
	})

	ts := httptest.NewServer(root)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/fast")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "quick" {
		t.Fatalf("expected 'quick', got %q", string(body))
	}
}

// ---------------------------------------------------------------------------
// Server builder
// ---------------------------------------------------------------------------

func TestNewServerAddress(t *testing.T) {
	cfg := ServerConfig{Host: "127.0.0.1", Port: "9090"}
	srv := NewServer(cfg, http.NewServeMux())
	if srv.Addr != "127.0.0.1:9090" {
		t.Fatalf("expected 127.0.0.1:9090, got %s", srv.Addr)
	}
}

func TestNewServerEmptyHost(t *testing.T) {
	cfg := ServerConfig{Host: "", Port: "8080"}
	srv := NewServer(cfg, http.NewServeMux())
	if srv.Addr != ":8080" {
		t.Fatalf("expected :8080, got %s", srv.Addr)
	}
}

func TestDefaultServerConfig(t *testing.T) {
	cfg := DefaultServerConfig()
	if cfg.Port != "8080" {
		t.Fatalf("expected port 8080, got %s", cfg.Port)
	}
	if cfg.ReadTimeout != 30*time.Second {
		t.Fatalf("expected 30s read timeout, got %v", cfg.ReadTimeout)
	}
}

// ---------------------------------------------------------------------------
// Middleware ordering
// ---------------------------------------------------------------------------

func TestMiddlewareOrder(t *testing.T) {
	// Verify that access logging happens around the request and can see
	// the trace_id injected by the traceID middleware.
	log := &captureLogger{}
	router := NewRouter(log, nil, nil, nil, nil)
	router.V1().Get("/ordered", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	ts := httptest.NewServer(router.Handler())
	defer ts.Close()

	req, _ := http.NewRequest("GET", ts.URL+"/api/v1/ordered", nil)
	req.Header.Set("X-Request-ID", "order-test")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	// Access log should contain trace_id because TraceID middleware runs
	// before AccessLogger's inner handler.
	var found bool
	for _, m := range log.msgs {
		if m.msg == "http access" {
			found = true
			if m.args["trace_id"] != "order-test" {
				t.Fatalf("expected trace_id in access log, got %v", m.args["trace_id"])
			}
		}
	}
	if !found {
		t.Fatal("expected access log entry")
	}
}

// ---------------------------------------------------------------------------
// Chi route patterns
// ---------------------------------------------------------------------------

func TestChiURLParam(t *testing.T) {
	router := NewRouter(noopLogger{}, nil, nil, nil, nil)
	router.V1().Get("/vms/{id}", func(w http.ResponseWriter, r *http.Request) {
		// chi URLParam is not exported from chi/v5 in a way we can use
		// without importing chi, but the route itself should match.
		w.Write([]byte("vm detail"))
	})

	ts := httptest.NewServer(router.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/vms/vm-123")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "vm detail" {
		t.Fatalf("expected 'vm detail', got %q", string(body))
	}
}

func TestChiMethodNotAllowed(t *testing.T) {
	router := NewRouter(noopLogger{}, nil, nil, nil, nil)
	router.V1().Get("/vms", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("list"))
	})

	ts := httptest.NewServer(router.Handler())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/v1/vms", "application/json", nil)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", resp.StatusCode)
	}
}

func TestChiURLParamValue(t *testing.T) {
	router := NewRouter(noopLogger{}, nil, nil, nil, nil)
	var captured string
	router.V1().Get("/vms/{id}", func(w http.ResponseWriter, r *http.Request) {
		captured = chi.URLParam(r, "id")
		w.Write([]byte(captured))
	})

	ts := httptest.NewServer(router.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/vms/vm-456")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if captured != "vm-456" {
		t.Fatalf("expected 'vm-456', got %q", captured)
	}
}
