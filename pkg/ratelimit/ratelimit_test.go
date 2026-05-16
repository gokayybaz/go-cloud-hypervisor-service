package ratelimit

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/org/ch-api/pkg/auth"
	"github.com/org/ch-api/pkg/logging"
)

func TestClientIP(t *testing.T) {
	tests := []struct {
		name       string
		headers    map[string]string
		remoteAddr string
		want       string
	}{
		{
			name:       "RemoteAddr only",
			remoteAddr: "192.168.1.1:12345",
			want:       "192.168.1.1",
		},
		{
			name:       "X-Forwarded-For",
			headers:    map[string]string{"X-Forwarded-For": "10.0.0.1"},
			remoteAddr: "192.168.1.1:12345",
			want:       "10.0.0.1",
		},
		{
			name:       "X-Forwarded-For multiple",
			headers:    map[string]string{"X-Forwarded-For": "10.0.0.1, 10.0.0.2, 10.0.0.3"},
			remoteAddr: "192.168.1.1:12345",
			want:       "10.0.0.1",
		},
		{
			name:       "X-Real-Ip",
			headers:    map[string]string{"X-Real-Ip": "10.0.0.2"},
			remoteAddr: "192.168.1.1:12345",
			want:       "10.0.0.2",
		},
		{
			name:       "X-Forwarded-For takes precedence over X-Real-Ip",
			headers:    map[string]string{"X-Forwarded-For": "10.0.0.1", "X-Real-Ip": "10.0.0.2"},
			remoteAddr: "192.168.1.1:12345",
			want:       "10.0.0.1",
		},
		{
			name:       "IPv6 RemoteAddr",
			remoteAddr: "[::1]:12345",
			want:       "::1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}
			req.RemoteAddr = tt.remoteAddr
			got := clientIP(req)
			if got != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, got)
			}
		})
	}
}

func TestLimiterDisabled(t *testing.T) {
	logger := logging.New("debug")
	cfg := Config{Enabled: false, Window: time.Minute, IPLimit: 1}
	l := NewLimiter(cfg, logger)
	defer l.Stop()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mw := l.Middleware()(handler)

	for i := 0; i < 10; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rr := httptest.NewRecorder()
		mw.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rr.Code)
		}
	}
}

func TestLimiterPerIP(t *testing.T) {
	logger := logging.New("debug")
	cfg := Config{Enabled: true, Window: time.Minute, IPLimit: 3}
	l := NewLimiter(cfg, logger)
	defer l.Stop()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mw := l.Middleware()(handler)

	// 3 requests should succeed.
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rr := httptest.NewRecorder()
		mw.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("request %d: expected 200, got %d", i+1, rr.Code)
		}
	}

	// 4th request from same IP should be rate limited.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", rr.Code)
	}
	retryAfter := rr.Header().Get("Retry-After")
	if retryAfter == "" {
		t.Fatal("expected Retry-After header")
	}
	if !strings.Contains(rr.Body.String(), "rate limit exceeded") {
		t.Fatalf("expected rate limit message in body, got %q", rr.Body.String())
	}

	// Request from different IP should succeed.
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.RemoteAddr = "10.0.0.2:12345"
	rr2 := httptest.NewRecorder()
	mw.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Fatalf("different ip: expected 200, got %d", rr2.Code)
	}
}

func TestLimiterPerToken(t *testing.T) {
	logger := logging.New("debug")
	cfg := Config{
		Enabled:       true,
		Window:        time.Minute,
		IPLimit:       100, // high IP limit so token limit is the bottleneck
		ViewerLimit:   2,
		OperatorLimit: 5,
		AdminLimit:    10,
	}
	l := NewLimiter(cfg, logger)
	defer l.Stop()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mw := l.Middleware()(handler)

	// Viewer token: 2 allowed, 3rd blocked.
	viewerCtx := auth.WithSubject(context.Background(), "viewer-user")
	viewerCtx = auth.WithRoles(viewerCtx, []string{"viewer"})

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(viewerCtx)
		rr := httptest.NewRecorder()
		mw.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("viewer request %d: expected 200, got %d", i+1, rr.Code)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(viewerCtx)
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("viewer: expected 429, got %d", rr.Code)
	}

	// Different viewer should have their own limit.
	viewer2Ctx := auth.WithSubject(context.Background(), "viewer-2")
	viewer2Ctx = auth.WithRoles(viewer2Ctx, []string{"viewer"})
	req2 := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(viewer2Ctx)
	rr2 := httptest.NewRecorder()
	mw.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Fatalf("viewer2: expected 200, got %d", rr2.Code)
	}

	// Operator token: 5 allowed.
	opCtx := auth.WithSubject(context.Background(), "op-user")
	opCtx = auth.WithRoles(opCtx, []string{"operator"})
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(opCtx)
		rr := httptest.NewRecorder()
		mw.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("op request %d: expected 200, got %d", i+1, rr.Code)
		}
	}
	req = httptest.NewRequest(http.MethodGet, "/", nil).WithContext(opCtx)
	rr = httptest.NewRecorder()
	mw.ServeHTTP(rr, req)
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("op: expected 429, got %d", rr.Code)
	}

	// Admin token: 10 allowed.
	adminCtx := auth.WithSubject(context.Background(), "admin-user")
	adminCtx = auth.WithRoles(adminCtx, []string{"admin"})
	for i := 0; i < 10; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(adminCtx)
		rr := httptest.NewRecorder()
		mw.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("admin request %d: expected 200, got %d", i+1, rr.Code)
		}
	}
	req = httptest.NewRequest(http.MethodGet, "/", nil).WithContext(adminCtx)
	rr = httptest.NewRecorder()
	mw.ServeHTTP(rr, req)
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("admin: expected 429, got %d", rr.Code)
	}
}

func TestLimiterHighestRoleWins(t *testing.T) {
	logger := logging.New("debug")
	cfg := Config{
		Enabled:       true,
		Window:        time.Minute,
		IPLimit:       100,
		ViewerLimit:   2,
		OperatorLimit: 5,
		AdminLimit:    10,
	}
	l := NewLimiter(cfg, logger)
	defer l.Stop()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mw := l.Middleware()(handler)

	// User with both viewer and admin roles gets admin limit (10).
	ctx := auth.WithSubject(context.Background(), "multi-role")
	ctx = auth.WithRoles(ctx, []string{"viewer", "admin"})

	for i := 0; i < 10; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)
		rr := httptest.NewRecorder()
		mw.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("request %d: expected 200, got %d", i+1, rr.Code)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", rr.Code)
	}
}

func TestLimiterPerTokenFallsBackToIPLimit(t *testing.T) {
	logger := logging.New("debug")
	cfg := Config{
		Enabled:       true,
		Window:        time.Minute,
		IPLimit:       3,
		ViewerLimit:   0, // unlimited
		OperatorLimit: 0,
		AdminLimit:    0,
	}
	l := NewLimiter(cfg, logger)
	defer l.Stop()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mw := l.Middleware()(handler)

	// Authenticated user with unlimited token limit but low IP limit.
	ctx := auth.WithSubject(context.Background(), "user-1")
	ctx = auth.WithRoles(ctx, []string{"admin"})

	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)
		rr := httptest.NewRecorder()
		mw.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("request %d: expected 200, got %d", i+1, rr.Code)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 (IP limit), got %d", rr.Code)
	}
}

func TestLimiterSlidingWindow(t *testing.T) {
	logger := logging.New("debug")
	window := 100 * time.Millisecond
	cfg := Config{Enabled: true, Window: window, IPLimit: 2}
	l := NewLimiter(cfg, logger)
	defer l.Stop()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mw := l.Middleware()(handler)

	// Exhaust the limit.
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rr := httptest.NewRecorder()
		mw.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("request %d: expected 200, got %d", i+1, rr.Code)
		}
	}

	// Next request blocked.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", rr.Code)
	}

	// Wait for the window to roll over.
	time.Sleep(window + 10*time.Millisecond)

	// Request should now succeed (prev count decays).
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	rr = httptest.NewRecorder()
	mw.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("after window: expected 200, got %d", rr.Code)
	}
}

func TestLimiterCleanup(t *testing.T) {
	logger := logging.New("debug")
	window := 50 * time.Millisecond
	cfg := Config{Enabled: true, Window: window, IPLimit: 2}
	l := NewLimiter(cfg, logger)
	defer l.Stop()

	// Make a request to create a window entry.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	l.Middleware()(handler).ServeHTTP(rr, req)

	l.mu.Lock()
	before := len(l.windows)
	l.mu.Unlock()
	if before == 0 {
		t.Fatal("expected window entries after request")
	}

	// Wait for cleanup to run.
	time.Sleep(window + 100*time.Millisecond)

	l.mu.Lock()
	after := len(l.windows)
	l.mu.Unlock()
	if after != 0 {
		t.Fatalf("expected 0 window entries after cleanup, got %d", after)
	}
}

func TestLimiterRetryAfterHeader(t *testing.T) {
	logger := logging.New("debug")
	cfg := Config{Enabled: true, Window: time.Minute, IPLimit: 1}
	l := NewLimiter(cfg, logger)
	defer l.Stop()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mw := l.Middleware()(handler)

	// First request succeeds.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	// Second request is blocked.
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	rr = httptest.NewRecorder()
	mw.ServeHTTP(rr, req)
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", rr.Code)
	}

	retryAfter := rr.Header().Get("Retry-After")
	if retryAfter == "" {
		t.Fatal("expected Retry-After header")
	}
	// Should be close to 60 seconds but less.
	if retryAfter == "0" {
		t.Fatal("expected non-zero Retry-After")
	}
}
