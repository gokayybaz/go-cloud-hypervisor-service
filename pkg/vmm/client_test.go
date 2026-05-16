package vmm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/org/ch-api/pkg/logging"
)

// ---------------------------------------------------------------------------
// captureLogger is a test helper that records every log line.
// ---------------------------------------------------------------------------

type captureLogger struct {
	lines []logLine
}

type logLine struct {
	level string
	msg   string
	args  map[string]any
}

func (c *captureLogger) capture(msg string, args ...any) {
	m := make(map[string]any)
	for i := 0; i < len(args)-1; i += 2 {
		if k, ok := args[i].(string); ok {
			m[k] = args[i+1]
		}
	}
	c.lines = append(c.lines, logLine{msg: msg, args: m})
}

func (c *captureLogger) Info(msg string, args ...any)  { c.capture(msg, args...) }
func (c *captureLogger) Error(msg string, args ...any) { c.capture(msg, args...) }
func (c *captureLogger) Debug(msg string, args ...any) { c.capture(msg, args...) }
func (c *captureLogger) Warn(msg string, args ...any)  { c.capture(msg, args...) }
func (c *captureLogger) WithContext(_ context.Context) logging.Logger { return c }
func (c *captureLogger) With(...any) logging.Logger                    { return c }

func TestNewClientDefaults(t *testing.T) {
	c := New(Config{
		Transport: TransportHTTP,
		Address:   "localhost:8080",
	})
	if c.cfg.RetryPolicy.MaxRetries != 3 {
		t.Fatalf("expected default 3 retries, got %d", c.cfg.RetryPolicy.MaxRetries)
	}
	if c.cfg.RequestTimeout != 30*time.Second {
		t.Fatalf("expected default 30s timeout, got %v", c.cfg.RequestTimeout)
	}
}

func TestClientString(t *testing.T) {
	unix := New(Config{Transport: TransportUnixSock, Address: "/tmp/ch.sock"})
	if unix.String() != "vmm.Client(unix:///tmp/ch.sock)" {
		t.Fatalf("unexpected unix string: %s", unix.String())
	}

	http := New(Config{Transport: TransportHTTP, Address: "localhost:8080"})
	if http.String() != "vmm.Client(http://localhost:8080)" {
		t.Fatalf("unexpected http string: %s", http.String())
	}
}

func TestClientPing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/vmm.ping" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(Config{
		Transport:      TransportHTTP,
		Address:        strings.TrimPrefix(srv.URL, "http://"),
		RequestTimeout: 5 * time.Second,
	})

	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("ping: %v", err)
	}
}

func TestClientVersion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"build_version": "35.0"})
	}))
	defer srv.Close()

	c := New(Config{
		Transport:      TransportHTTP,
		Address:        strings.TrimPrefix(srv.URL, "http://"),
		RequestTimeout: 5 * time.Second,
	})

	v, err := c.Version(context.Background())
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	if v != "35.0" {
		t.Fatalf("expected version 35.0, got %s", v)
	}
}

func TestClientCreate(t *testing.T) {
	var received VmConfig
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/api/v1/vm.create" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Errorf("decode body: %v", err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := New(Config{
		Transport:      TransportHTTP,
		Address:        strings.TrimPrefix(srv.URL, "http://"),
		RequestTimeout: 5 * time.Second,
	})

	cfg := &VmConfig{
		CPUs:   &CPUConfig{BootVCPUs: 2, MaxVCPUs: 4},
		Memory: &MemoryConfig{Size: 1024 * 1024 * 1024},
		Kernel: &KernelConfig{Path: "/boot/vmlinux"},
	}
	if err := c.Create(context.Background(), cfg); err != nil {
		t.Fatalf("create: %v", err)
	}
	if received.CPUs.BootVCPUs != 2 {
		t.Fatalf("expected 2 vcpus, got %d", received.CPUs.BootVCPUs)
	}
}

func TestClientRetryOn5xx(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(Config{
		Transport:      TransportHTTP,
		Address:        strings.TrimPrefix(srv.URL, "http://"),
		RequestTimeout: 5 * time.Second,
		RetryPolicy: RetryPolicy{
			MaxRetries: 5,
			BaseDelay:  50 * time.Millisecond,
			MaxDelay:   200 * time.Millisecond,
			Multiplier: 2.0,
		},
	})

	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("ping after retries: %v", err)
	}
	if attempts != 3 {
		t.Fatalf("expected 3 attempts, got %d", attempts)
	}
}

func TestClientRetryExhausted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	c := New(Config{
		Transport:      TransportHTTP,
		Address:        strings.TrimPrefix(srv.URL, "http://"),
		RequestTimeout: 5 * time.Second,
		RetryPolicy: RetryPolicy{
			MaxRetries: 2,
			BaseDelay:  10 * time.Millisecond,
			MaxDelay:   50 * time.Millisecond,
			Multiplier: 2.0,
		},
	})

	err := c.Ping(context.Background())
	if err == nil {
		t.Fatal("expected error after retries exhausted")
	}
	if !IsCode(err, ErrCodeRetryExhausted) {
		t.Fatalf("expected retry_exhausted, got %v", err)
	}
	if !IsOp(err, "Ping") {
		t.Fatalf("expected Op=Ping, got %v", err)
	}
}

func TestClientTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(Config{
		Transport:      TransportHTTP,
		Address:        strings.TrimPrefix(srv.URL, "http://"),
		RequestTimeout: 50 * time.Millisecond,
		RetryPolicy:    RetryPolicy{MaxRetries: 0},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := c.Ping(ctx)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !IsCode(err, ErrCodeTimeout) {
		t.Fatalf("expected timeout error, got %v", err)
	}
}

func TestClientNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		io.WriteString(w, `{"message":"vm not found"}`)
	}))
	defer srv.Close()

	c := New(Config{
		Transport:      TransportHTTP,
		Address:        strings.TrimPrefix(srv.URL, "http://"),
		RequestTimeout: 5 * time.Second,
		RetryPolicy:    RetryPolicy{MaxRetries: 0},
	})

	err := c.Delete(context.Background())
	if err == nil {
		t.Fatal("expected not found error")
	}
	if !IsCode(err, ErrCodeNotFound) {
		t.Fatalf("expected not_found, got %v", err)
	}
	if !IsDeleteFailed(err) {
		t.Fatalf("expected IsDeleteFailed, got %v", err)
	}
}

func TestBackoffDelay(t *testing.T) {
	policy := RetryPolicy{
		BaseDelay:  100 * time.Millisecond,
		MaxDelay:   1 * time.Second,
		Multiplier: 2.0,
	}

	d0 := backoffDelay(0, policy)
	if d0 < 0 || d0 > 100*time.Millisecond {
		t.Fatalf("attempt 0 delay out of range: %v", d0)
	}

	d1 := backoffDelay(1, policy)
	if d1 < 0 || d1 > 200*time.Millisecond {
		t.Fatalf("attempt 1 delay out of range: %v", d1)
	}

	d2 := backoffDelay(2, policy)
	if d2 < 0 || d2 > 400*time.Millisecond {
		t.Fatalf("attempt 2 delay out of range: %v", d2)
	}

	d10 := backoffDelay(10, policy)
	if d10 < 0 || d10 > policy.MaxDelay {
		t.Fatalf("attempt 10 delay out of range: %v", d10)
	}
}

func TestShouldRetryStatus(t *testing.T) {
	retryable := []int{429, 500, 502, 503, 504}
	for _, code := range retryable {
		if !shouldRetryStatus(code) {
			t.Fatalf("expected %d to be retryable", code)
		}
	}

	notRetryable := []int{200, 400, 401, 403, 404, 422}
	for _, code := range notRetryable {
		if shouldRetryStatus(code) {
			t.Fatalf("expected %d to NOT be retryable", code)
		}
	}
}

func TestIsRetryableError(t *testing.T) {
	if !isRetryableError(errors.New("connection refused")) {
		t.Fatal("generic error should be retryable")
	}
	if isRetryableError(context.DeadlineExceeded) {
		t.Fatal("DeadlineExceeded should NOT be retryable")
	}
	if isRetryableError(context.Canceled) {
		t.Fatal("Canceled should NOT be retryable")
	}
}

func TestUnixSocketTransport(t *testing.T) {
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

	c := New(Config{
		Transport:      TransportUnixSock,
		Address:        sockPath,
		RequestTimeout: 5 * time.Second,
		RetryPolicy:    RetryPolicy{MaxRetries: 0},
	})

	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("ping via unix socket: %v", err)
	}
}

func TestErrorString(t *testing.T) {
	e := NewError("Create", ErrCodeAPI, 500, "server error", "body", nil)
	want := "vmm.api[Create] (HTTP 500): server error"
	if e.Error() != want {
		t.Fatalf("error string mismatch: got %q, want %q", e.Error(), want)
	}

	e2 := NewError("Boot", ErrCodeConnection, 0, "dial failed", "", fmt.Errorf("refused"))
	want2 := "vmm.connection[Boot]: dial failed"
	if e2.Error() != want2 {
		t.Fatalf("error string mismatch: got %q, want %q", e2.Error(), want2)
	}

	e3 := NewError("", ErrCodeTimeout, 0, "timed out", "", nil)
	want3 := "vmm.timeout: timed out"
	if e3.Error() != want3 {
		t.Fatalf("error string mismatch: got %q, want %q", e3.Error(), want3)
	}
}

func TestIsCode(t *testing.T) {
	e := NewError("Create", ErrCodeAPI, 404, "not found", "", nil)
	if !IsCode(e, ErrCodeAPI) {
		t.Fatal("IsCode should match")
	}
	if IsCode(e, ErrCodeTimeout) {
		t.Fatal("IsCode should not match different code")
	}
	if IsCode(nil, ErrCodeAPI) {
		t.Fatal("IsCode(nil) should be false")
	}
}

func TestIsOp(t *testing.T) {
	e := NewError("Boot", ErrCodeAPI, 500, "fail", "", nil)
	if !IsOp(e, "Boot") {
		t.Fatal("IsOp should match")
	}
	if IsOp(e, "Create") {
		t.Fatal("IsOp should not match different op")
	}
	if IsOp(nil, "Boot") {
		t.Fatal("IsOp(nil) should be false")
	}
}

func TestIsNotFound(t *testing.T) {
	e := NewError("Delete", ErrCodeNotFound, 404, "vm not found", "", nil)
	if !IsNotFound(e) {
		t.Fatal("IsNotFound should be true for ErrCodeNotFound")
	}
}

func TestOperationSpecificHelpers(t *testing.T) {
	cases := []struct {
		op      string
		check   func(error) bool
		name    string
	}{
		{"Create", IsCreateFailed, "IsCreateFailed"},
		{"Boot", IsBootFailed, "IsBootFailed"},
		{"Pause", IsPauseFailed, "IsPauseFailed"},
		{"Resume", IsResumeFailed, "IsResumeFailed"},
		{"Shutdown", IsShutdownFailed, "IsShutdownFailed"},
		{"Reboot", IsRebootFailed, "IsRebootFailed"},
		{"Delete", IsDeleteFailed, "IsDeleteFailed"},
	}
	for _, c := range cases {
		err := NewError(c.op, ErrCodeAPI, 500, "fail", "", nil)
		if !c.check(err) {
			t.Fatalf("%s should return true for op=%s", c.name, c.op)
		}
		other := NewError("Other", ErrCodeAPI, 500, "fail", "", nil)
		if c.check(other) {
			t.Fatalf("%s should return false for op=Other", c.name)
		}
	}
}

func TestBaseURL(t *testing.T) {
	if baseURL(Config{Transport: TransportUnixSock, Address: "/tmp/ch.sock"}) != "http://localhost" {
		t.Fatal("unix baseURL should be localhost placeholder")
	}
	if baseURL(Config{Transport: TransportHTTP, Address: "localhost:8080"}) != "http://localhost:8080" {
		t.Fatal("http baseURL should include scheme")
	}
}

func TestVmConfigSerialization(t *testing.T) {
	cfg := VmConfig{
		CPUs: &CPUConfig{
			BootVCPUs: 2,
			MaxVCPUs:  4,
		},
		Memory: &MemoryConfig{Size: 2 * 1024 * 1024 * 1024},
		Kernel: &KernelConfig{Path: "/boot/vmlinux"},
		Disks: []DiskConfig{
			{Path: "/disk.img", Readonly: false},
		},
		Net: []NetConfig{
			{Mac: "52:54:00:12:34:56"},
		},
	}

	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded VmConfig
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.CPUs.BootVCPUs != 2 {
		t.Fatalf("expected 2 vcpus, got %d", decoded.CPUs.BootVCPUs)
	}
	if decoded.Memory.Size != cfg.Memory.Size {
		t.Fatalf("memory size mismatch")
	}
	if decoded.Kernel.Path != "/boot/vmlinux" {
		t.Fatalf("kernel path mismatch")
	}
	if len(decoded.Disks) != 1 || decoded.Disks[0].Path != "/disk.img" {
		t.Fatal("disk config mismatch")
	}
	if len(decoded.Net) != 1 || decoded.Net[0].Mac != "52:54:00:12:34:56" {
		t.Fatal("net config mismatch")
	}
}

// ---------------------------------------------------------------------------
// Logging tests
// ---------------------------------------------------------------------------

func TestClientLogsTransitions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	log := &captureLogger{}
	c := New(Config{
		Transport:      TransportHTTP,
		Address:        strings.TrimPrefix(srv.URL, "http://"),
		RequestTimeout: 5 * time.Second,
		RetryPolicy:    RetryPolicy{MaxRetries: 0},
		Logger:         log,
	})

	ctx := context.Background()
	if err := c.Ping(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}

	if len(log.lines) < 2 {
		t.Fatalf("expected at least 2 log lines, got %d", len(log.lines))
	}
	if log.lines[0].args["op"] != "Ping" || log.lines[0].args["state"] != "starting" {
		t.Fatalf("expected starting log, got %+v", log.lines[0])
	}
	if log.lines[len(log.lines)-1].args["op"] != "Ping" || log.lines[len(log.lines)-1].args["state"] != "succeeded" {
		t.Fatalf("expected succeeded log, got %+v", log.lines[len(log.lines)-1])
	}
}

func TestClientLogsFailureWithTraceID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	log := &captureLogger{}
	c := New(Config{
		Transport:      TransportHTTP,
		Address:        strings.TrimPrefix(srv.URL, "http://"),
		RequestTimeout: 5 * time.Second,
		RetryPolicy:    RetryPolicy{MaxRetries: 0},
		Logger:         log,
	})

	ctx := logging.WithTraceID(context.Background(), "abc123")
	err := c.Boot(ctx)
	if err == nil {
		t.Fatal("expected error")
	}

	foundFailed := false
	for _, line := range log.lines {
		if line.args["state"] == "failed" && line.args["op"] == "Boot" {
			foundFailed = true
		}
	}
	if !foundFailed {
		t.Fatalf("expected failed log line, got %+v", log.lines)
	}
}

func TestClientLogsRetryAttempts(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	log := &captureLogger{}
	c := New(Config{
		Transport:      TransportHTTP,
		Address:        strings.TrimPrefix(srv.URL, "http://"),
		RequestTimeout: 5 * time.Second,
		RetryPolicy: RetryPolicy{
			MaxRetries: 3,
			BaseDelay:  10 * time.Millisecond,
			MaxDelay:   50 * time.Millisecond,
			Multiplier: 2.0,
		},
		Logger: log,
	})

	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("ping: %v", err)
	}

	// Should have retry debug logs.
	foundRetry := false
	for _, line := range log.lines {
		if line.msg == "vmm retry" {
			foundRetry = true
		}
	}
	if !foundRetry {
		t.Fatalf("expected retry debug logs, got %+v", log.lines)
	}
}

// ---------------------------------------------------------------------------
// Missing client method tests
// ---------------------------------------------------------------------------

func TestClientClose(t *testing.T) {
	c := New(Config{
		Transport: TransportHTTP,
		Address:   "localhost:8080",
	})
	if err := c.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}

func TestClientInfo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/vm.info" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		json.NewEncoder(w).Encode(VmInfo{
			Config: VmConfig{Kernel: &KernelConfig{Path: "/boot"}},
			State:  "Running",
			Memory: 2 * 1024 * 1024 * 1024,
			Cpus:   4,
		})
	}))
	defer srv.Close()

	c := New(Config{
		Transport:      TransportHTTP,
		Address:        strings.TrimPrefix(srv.URL, "http://"),
		RequestTimeout: 5 * time.Second,
		RetryPolicy:    RetryPolicy{MaxRetries: 0},
	})

	info, err := c.Info(context.Background())
	if err != nil {
		t.Fatalf("info: %v", err)
	}
	if info.State != "Running" {
		t.Fatalf("expected state Running, got %s", info.State)
	}
	if info.Memory != 2*1024*1024*1024 {
		t.Fatalf("expected memory 2GiB, got %d", info.Memory)
	}
	if info.Cpus != 4 {
		t.Fatalf("expected 4 cpus, got %d", info.Cpus)
	}
}

func TestClientInfoError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := New(Config{
		Transport:      TransportHTTP,
		Address:        strings.TrimPrefix(srv.URL, "http://"),
		RequestTimeout: 5 * time.Second,
		RetryPolicy:    RetryPolicy{MaxRetries: 0},
	})

	_, err := c.Info(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !IsOp(err, "Info") {
		t.Fatalf("expected Op=Info, got %v", err)
	}
}

func TestClientShutdown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/api/v1/vm.shutdown" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(Config{
		Transport:      TransportHTTP,
		Address:        strings.TrimPrefix(srv.URL, "http://"),
		RequestTimeout: 5 * time.Second,
		RetryPolicy:    RetryPolicy{MaxRetries: 0},
	})

	if err := c.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}

func TestClientShutdownError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := New(Config{
		Transport:      TransportHTTP,
		Address:        strings.TrimPrefix(srv.URL, "http://"),
		RequestTimeout: 5 * time.Second,
		RetryPolicy:    RetryPolicy{MaxRetries: 0},
	})

	err := c.Shutdown(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !IsShutdownFailed(err) {
		t.Fatalf("expected IsShutdownFailed, got %v", err)
	}
}

func TestClientReboot(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/api/v1/vm.reboot" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(Config{
		Transport:      TransportHTTP,
		Address:        strings.TrimPrefix(srv.URL, "http://"),
		RequestTimeout: 5 * time.Second,
		RetryPolicy:    RetryPolicy{MaxRetries: 0},
	})

	if err := c.Reboot(context.Background()); err != nil {
		t.Fatalf("reboot: %v", err)
	}
}

func TestClientRebootError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := New(Config{
		Transport:      TransportHTTP,
		Address:        strings.TrimPrefix(srv.URL, "http://"),
		RequestTimeout: 5 * time.Second,
		RetryPolicy:    RetryPolicy{MaxRetries: 0},
	})

	err := c.Reboot(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !IsRebootFailed(err) {
		t.Fatalf("expected IsRebootFailed, got %v", err)
	}
}

func TestClientPause(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/api/v1/vm.pause" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(Config{
		Transport:      TransportHTTP,
		Address:        strings.TrimPrefix(srv.URL, "http://"),
		RequestTimeout: 5 * time.Second,
		RetryPolicy:    RetryPolicy{MaxRetries: 0},
	})

	if err := c.Pause(context.Background()); err != nil {
		t.Fatalf("pause: %v", err)
	}
}

func TestClientPauseError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := New(Config{
		Transport:      TransportHTTP,
		Address:        strings.TrimPrefix(srv.URL, "http://"),
		RequestTimeout: 5 * time.Second,
		RetryPolicy:    RetryPolicy{MaxRetries: 0},
	})

	err := c.Pause(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !IsPauseFailed(err) {
		t.Fatalf("expected IsPauseFailed, got %v", err)
	}
}

func TestClientResume(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/api/v1/vm.resume" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(Config{
		Transport:      TransportHTTP,
		Address:        strings.TrimPrefix(srv.URL, "http://"),
		RequestTimeout: 5 * time.Second,
		RetryPolicy:    RetryPolicy{MaxRetries: 0},
	})

	if err := c.Resume(context.Background()); err != nil {
		t.Fatalf("resume: %v", err)
	}
}

func TestClientResumeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := New(Config{
		Transport:      TransportHTTP,
		Address:        strings.TrimPrefix(srv.URL, "http://"),
		RequestTimeout: 5 * time.Second,
		RetryPolicy:    RetryPolicy{MaxRetries: 0},
	})

	err := c.Resume(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !IsResumeFailed(err) {
		t.Fatalf("expected IsResumeFailed, got %v", err)
	}
}

func TestClientVersionError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := New(Config{
		Transport:      TransportHTTP,
		Address:        strings.TrimPrefix(srv.URL, "http://"),
		RequestTimeout: 5 * time.Second,
		RetryPolicy:    RetryPolicy{MaxRetries: 0},
	})

	_, err := c.Version(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !IsOp(err, "Version") {
		t.Fatalf("expected Op=Version, got %v", err)
	}
}

func TestClientCreateError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	c := New(Config{
		Transport:      TransportHTTP,
		Address:        strings.TrimPrefix(srv.URL, "http://"),
		RequestTimeout: 5 * time.Second,
		RetryPolicy:    RetryPolicy{MaxRetries: 0},
	})

	err := c.Create(context.Background(), &VmConfig{
		CPUs:   &CPUConfig{BootVCPUs: 1, MaxVCPUs: 1},
		Memory: &MemoryConfig{Size: 256},
		Kernel: &KernelConfig{Path: "/boot"},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !IsCreateFailed(err) {
		t.Fatalf("expected IsCreateFailed, got %v", err)
	}
}

func TestClientBootError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := New(Config{
		Transport:      TransportHTTP,
		Address:        strings.TrimPrefix(srv.URL, "http://"),
		RequestTimeout: 5 * time.Second,
		RetryPolicy:    RetryPolicy{MaxRetries: 0},
	})

	err := c.Boot(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !IsBootFailed(err) {
		t.Fatalf("expected IsBootFailed, got %v", err)
	}
}

func TestClientDeleteError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := New(Config{
		Transport:      TransportHTTP,
		Address:        strings.TrimPrefix(srv.URL, "http://"),
		RequestTimeout: 5 * time.Second,
		RetryPolicy:    RetryPolicy{MaxRetries: 0},
	})

	err := c.Delete(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !IsDeleteFailed(err) {
		t.Fatalf("expected IsDeleteFailed, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// wrapError tests
// ---------------------------------------------------------------------------

func TestWrapErrorDeadlineExceeded(t *testing.T) {
	c := New(Config{})
	err := c.wrapError("Ping", context.DeadlineExceeded, 0, "")
	if !IsCode(err, ErrCodeTimeout) {
		t.Fatalf("expected timeout, got %v", err)
	}
}

func TestWrapErrorCanceled(t *testing.T) {
	c := New(Config{})
	err := c.wrapError("Ping", context.Canceled, 0, "")
	if !IsCode(err, ErrCodeTimeout) {
		t.Fatalf("expected timeout, got %v", err)
	}
}

func TestWrapErrorNetTimeout(t *testing.T) {
	c := New(Config{})
	netErr := &netError{timeout: true}
	err := c.wrapError("Ping", netErr, 0, "")
	if !IsCode(err, ErrCodeTimeout) {
		t.Fatalf("expected timeout, got %v", err)
	}
}

func TestWrapErrorURLError(t *testing.T) {
	c := New(Config{})
	urlErr := &url.Error{Op: "Post", URL: "http://x", Err: errors.New("refused")}
	err := c.wrapError("Ping", urlErr, 0, "")
	if !IsCode(err, ErrCodeConnection) {
		t.Fatalf("expected connection, got %v", err)
	}
}

func TestWrapErrorURLTimeout(t *testing.T) {
	c := New(Config{})
	// url.Error.Timeout() returns true when the underlying error is context.DeadlineExceeded
	urlErr := &url.Error{Op: "Post", URL: "http://x", Err: context.DeadlineExceeded}
	err := c.wrapError("Ping", urlErr, 0, "")
	if !IsCode(err, ErrCodeTimeout) {
		t.Fatalf("expected timeout, got %v", err)
	}
}

func TestWrapErrorGeneric(t *testing.T) {
	c := New(Config{})
	err := c.wrapError("Ping", errors.New("something broke"), 0, "")
	if !IsCode(err, ErrCodeUnknown) {
		t.Fatalf("expected unknown, got %v", err)
	}
}

func TestWrapErrorWithStatus(t *testing.T) {
	c := New(Config{})
	err := c.wrapError("Ping", errors.New("bad"), 404, "body")
	if !IsCode(err, ErrCodeNotFound) {
		t.Fatalf("expected not_found, got %v", err)
	}
}

type netError struct {
	timeout   bool
	temporary bool
}

func (e *netError) Error() string   { return "net error" }
func (e *netError) Timeout() bool   { return e.timeout }
func (e *netError) Temporary() bool { return e.temporary }

// ---------------------------------------------------------------------------
// Error tests
// ---------------------------------------------------------------------------

func TestErrorUnwrap(t *testing.T) {
	cause := errors.New("root cause")
	e := NewError("Boot", ErrCodeAPI, 500, "fail", "", cause)
	if e.Unwrap() != cause {
		t.Fatal("Unwrap should return root cause")
	}
	if !errors.Is(e, cause) {
		t.Fatal("errors.Is should follow the chain")
	}
}

func TestErrorStringVariations(t *testing.T) {
	e1 := NewError("Boot", ErrCodeAPI, 500, "server error", "", nil)
	want1 := "vmm.api[Boot] (HTTP 500): server error"
	if e1.Error() != want1 {
		t.Fatalf("got %q, want %q", e1.Error(), want1)
	}

	e2 := NewError("Boot", ErrCodeAPI, 0, "fail", "", nil)
	want2 := "vmm.api[Boot]: fail"
	if e2.Error() != want2 {
		t.Fatalf("got %q, want %q", e2.Error(), want2)
	}

	e3 := NewError("", ErrCodeTimeout, 0, "timed out", "", nil)
	want3 := "vmm.timeout: timed out"
	if e3.Error() != want3 {
		t.Fatalf("got %q, want %q", e3.Error(), want3)
	}

	e4 := NewError("", ErrCodeAPI, 500, "fail", "", nil)
	want4 := "vmm.api (HTTP 500): fail"
	if e4.Error() != want4 {
		t.Fatalf("got %q, want %q", e4.Error(), want4)
	}
}

// ---------------------------------------------------------------------------
// noopLogger tests
// ---------------------------------------------------------------------------

func TestNoopLogger(t *testing.T) {
	var l noopLogger
	l.Info("info")
	l.Error("error")
	l.Debug("debug")
	l.Warn("warn")
	l2 := l.WithContext(context.Background())
	l3 := l2.With("key", "value")
	if l3 == nil {
		t.Fatal("noopLogger.With should return non-nil")
	}
}

// ---------------------------------------------------------------------------
// backoffDelay tests
// ---------------------------------------------------------------------------

func TestBackoffDelayZeroDefaults(t *testing.T) {
	// When policy has zero values, defaults should kick in.
	policy := RetryPolicy{}
	d := backoffDelay(0, policy)
	if d < 0 || d > 250*time.Millisecond {
		t.Fatalf("delay out of range with zero defaults: %v", d)
	}
}

// ---------------------------------------------------------------------------
// doJSON error path tests
// ---------------------------------------------------------------------------

func TestDoJSONMarshalError(t *testing.T) {
	// A type that cannot be marshaled to JSON.
	badBody := make(chan int)

	c := New(Config{
		Transport:      TransportHTTP,
		Address:        "localhost:1",
		RequestTimeout: 5 * time.Second,
		RetryPolicy:    RetryPolicy{MaxRetries: 0},
	})

	err := c.doJSON(context.Background(), "Create", http.MethodPut, "/api/v1/vm.create", badBody, nil)
	if err == nil {
		t.Fatal("expected marshal error")
	}
	if !IsCode(err, ErrCodeInvalidRequest) {
		t.Fatalf("expected invalid_request, got %v", err)
	}
}

func TestDoJSONUnmarshalError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("not json"))
	}))
	defer srv.Close()

	c := New(Config{
		Transport:      TransportHTTP,
		Address:        strings.TrimPrefix(srv.URL, "http://"),
		RequestTimeout: 5 * time.Second,
		RetryPolicy:    RetryPolicy{MaxRetries: 0},
	})

	var dst map[string]string
	err := c.doJSON(context.Background(), "Version", http.MethodGet, "/api/v1/vmm.version", nil, &dst)
	if err == nil {
		t.Fatal("expected unmarshal error")
	}
	if !IsCode(err, ErrCodeInvalidRequest) {
		t.Fatalf("expected invalid_request, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// doWithRetry network error tests
// ---------------------------------------------------------------------------

func TestDoWithRetryConnectionError(t *testing.T) {
	// Connect to a closed port to force connection refused.
	c := New(Config{
		Transport:      TransportHTTP,
		Address:        "localhost:1",
		RequestTimeout: 1 * time.Second,
		RetryPolicy: RetryPolicy{
			MaxRetries: 2,
			BaseDelay:  10 * time.Millisecond,
			MaxDelay:   50 * time.Millisecond,
			Multiplier: 2.0,
		},
	})

	err := c.Ping(context.Background())
	if err == nil {
		t.Fatal("expected connection error")
	}
	if !IsCode(err, ErrCodeRetryExhausted) {
		t.Fatalf("expected retry_exhausted, got %v", err)
	}
}

func TestDoWithRetryContextCancelled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	c := New(Config{
		Transport:      TransportHTTP,
		Address:        strings.TrimPrefix(srv.URL, "http://"),
		RequestTimeout: 5 * time.Second,
		RetryPolicy: RetryPolicy{
			MaxRetries: 5,
			BaseDelay:  1 * time.Second,
			MaxDelay:   2 * time.Second,
			Multiplier: 2.0,
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	err := c.Ping(ctx)
	if err == nil {
		t.Fatal("expected error")
	}
	if !IsCode(err, ErrCodeTimeout) {
		t.Fatalf("expected timeout (cancelled), got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Unix socket comprehensive tests
// ---------------------------------------------------------------------------

func TestUnixSocketAllOperations(t *testing.T) {
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
	mux.HandleFunc("/api/v1/vmm.version", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"build_version": "35.0"})
	})
	mux.HandleFunc("/api/v1/vm.info", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(VmInfo{State: "Running"})
	})
	mux.HandleFunc("/api/v1/vm.create", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/api/v1/vm.boot", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/api/v1/vm.shutdown", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/api/v1/vm.reboot", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/api/v1/vm.pause", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/api/v1/vm.resume", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/api/v1/vm", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	go http.Serve(listener, mux)

	c := New(Config{
		Transport:      TransportUnixSock,
		Address:        sockPath,
		RequestTimeout: 5 * time.Second,
		RetryPolicy:    RetryPolicy{MaxRetries: 0},
	})

	ctx := context.Background()

	if err := c.Ping(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}

	v, err := c.Version(ctx)
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	if v != "35.0" {
		t.Fatalf("expected version 35.0, got %s", v)
	}

	info, err := c.Info(ctx)
	if err != nil {
		t.Fatalf("info: %v", err)
	}
	if info.State != "Running" {
		t.Fatalf("expected state Running, got %s", info.State)
	}

	if err := c.Create(ctx, &VmConfig{
		CPUs:   &CPUConfig{BootVCPUs: 1, MaxVCPUs: 1},
		Memory: &MemoryConfig{Size: 256},
		Kernel: &KernelConfig{Path: "/boot"},
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := c.Boot(ctx); err != nil {
		t.Fatalf("boot: %v", err)
	}
	if err := c.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if err := c.Reboot(ctx); err != nil {
		t.Fatalf("reboot: %v", err)
	}
	if err := c.Pause(ctx); err != nil {
		t.Fatalf("pause: %v", err)
	}
	if err := c.Resume(ctx); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if err := c.Delete(ctx); err != nil {
		t.Fatalf("delete: %v", err)
	}
}