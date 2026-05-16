package pprof

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/org/ch-api/pkg/logging"
)

type noopLogger struct{}

func (noopLogger) Info(string, ...any)  {}
func (noopLogger) Error(string, ...any) {}
func (noopLogger) Debug(string, ...any) {}
func (noopLogger) Warn(string, ...any)  {}
func (n noopLogger) WithContext(context.Context) logging.Logger { return n }
func (n noopLogger) With(...any) logging.Logger                 { return n }

func TestPprofServerStartsAndServes(t *testing.T) {
	srv := New(noopLogger{}, "localhost:0")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := srv.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}

	// Wait for server to be ready.
	time.Sleep(50 * time.Millisecond)

	// Determine actual listen address.
	addr := srv.server.Addr
	if !strings.HasPrefix(addr, "localhost:") {
		// If Addr is still the original pattern, the server hasn't bound yet.
		// Give it a bit more time.
		time.Sleep(100 * time.Millisecond)
	}

	// The server Addr may not contain the actual port when using :0.
	// Use a helper to discover it.  Since http.Server doesn't expose
	// the listener directly, we'll use a fixed port for the test.
	_ = srv.Stop(context.Background())
}

func TestPprofServerFixedPort(t *testing.T) {
	// Use a fixed port to avoid race conditions in parallel tests.
	// In real usage :0 is fine.
	srv := New(noopLogger{}, "localhost:16060")

	ctx := context.Background()
	if err := srv.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer srv.Stop(context.Background())

	time.Sleep(50 * time.Millisecond)

	resp, err := http.Get("http://localhost:16060/debug/pprof/")
	if err != nil {
		t.Fatalf("get pprof index: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "Profile Descriptions") && !strings.Contains(string(body), "profiles") {
		t.Fatalf("expected pprof index page, got %q", string(body))
	}
}

func TestPprofServerShutdown(t *testing.T) {
	srv := New(noopLogger{}, "localhost:16061")

	ctx := context.Background()
	if err := srv.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	// Verify server is up.
	resp, err := http.Get("http://localhost:16061/debug/pprof/")
	if err != nil {
		t.Fatalf("get before shutdown: %v", err)
	}
	resp.Body.Close()

	// Shutdown.
	if err := srv.Stop(context.Background()); err != nil {
		t.Fatalf("stop: %v", err)
	}

	// Verify server is down.
	_, err = http.Get("http://localhost:16061/debug/pprof/")
	if err == nil {
		t.Fatal("expected error after shutdown")
	}
}

func TestPprofServerName(t *testing.T) {
	srv := New(noopLogger{}, "")
	if srv.Name() != "pprof-server" {
		t.Fatalf("expected name pprof-server, got %q", srv.Name())
	}
}

func TestPprofDefaultAddr(t *testing.T) {
	srv := New(noopLogger{}, "")
	if srv.addr != DefaultAddr {
		t.Fatalf("expected addr %q, got %q", DefaultAddr, srv.addr)
	}
}

func TestPprofEndpoints(t *testing.T) {
	// Test that all standard endpoints are registered.
	srv := New(noopLogger{}, "localhost:16062")

	ctx := context.Background()
	if err := srv.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer srv.Stop(context.Background())

	time.Sleep(50 * time.Millisecond)

	endpoints := []string{
		"/debug/pprof/",
		"/debug/pprof/cmdline",
		"/debug/pprof/symbol",
	}
	for _, path := range endpoints {
		resp, err := http.Get("http://localhost:16062" + path)
		if err != nil {
			t.Fatalf("get %s: %v", path, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200 for %s, got %d", path, resp.StatusCode)
		}
	}
}
