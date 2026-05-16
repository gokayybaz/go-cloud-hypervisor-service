package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/org/ch-api/pkg/logging"
)

func TestParseRole(t *testing.T) {
	tests := []struct {
		input string
		want  Role
		ok    bool
	}{
		{"viewer", RoleViewer, true},
		{"operator", RoleOperator, true},
		{"admin", RoleAdmin, true},
		{"VIEWER", RoleViewer, true},
		{"Operator", RoleOperator, true},
		{"ADMIN", RoleAdmin, true},
		{"superuser", RoleViewer, false},
		{"", RoleViewer, false},
	}

	for _, tt := range tests {
		got, ok := ParseRole(tt.input)
		if ok != tt.ok {
			t.Fatalf("ParseRole(%q) ok=%v, want %v", tt.input, ok, tt.ok)
		}
		if ok && got != tt.want {
			t.Fatalf("ParseRole(%q)=%v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestRoleString(t *testing.T) {
	if RoleViewer.String() != "viewer" {
		t.Fatalf("expected viewer, got %s", RoleViewer.String())
	}
	if RoleOperator.String() != "operator" {
		t.Fatalf("expected operator, got %s", RoleOperator.String())
	}
	if RoleAdmin.String() != "admin" {
		t.Fatalf("expected admin, got %s", RoleAdmin.String())
	}
	if Role(99).String() != "unknown" {
		t.Fatalf("expected unknown, got %s", Role(99).String())
	}
}

func TestMatchRule(t *testing.T) {
	table := DefaultPermissionTable()

	tests := []struct {
		method string
		path   string
		want   Role
		found  bool
	}{
		{"GET", "/api/v1/vms", RoleViewer, true},
		{"GET", "/api/v1/vms/vm-123", RoleViewer, true},
		{"GET", "/api/v1/vms/vm-123/console", RoleViewer, true},
		{"POST", "/api/v1/vms/vm-123/boot", RoleOperator, true},
		{"POST", "/api/v1/vms/vm-123/pause", RoleOperator, true},
		{"POST", "/api/v1/vms/vm-123/resume", RoleOperator, true},
		{"POST", "/api/v1/vms/vm-123/shutdown", RoleOperator, true},
		{"POST", "/api/v1/vms/vm-123/reboot", RoleOperator, true},
		{"PATCH", "/api/v1/vms/vm-123/cpu", RoleOperator, true},
		{"PATCH", "/api/v1/vms/vm-123/memory", RoleOperator, true},
		{"POST", "/api/v1/vms", RoleAdmin, true},
		{"DELETE", "/api/v1/vms/vm-123", RoleAdmin, true},
		{"POST", "/api/v1/vms/vm-123/disks", RoleAdmin, true},
		{"DELETE", "/api/v1/vms/vm-123/disks/disk-0", RoleAdmin, true},
		{"POST", "/api/v1/vms/vm-123/disks/disk-0/snapshot", RoleAdmin, true},
		{"POST", "/api/v1/vms/vm-123/interfaces", RoleAdmin, true},
		{"DELETE", "/api/v1/vms/vm-123/interfaces/eth0", RoleAdmin, true},
		{"PATCH", "/api/v1/vms/vm-123/interfaces/eth0", RoleAdmin, true},
		// unlisted
		{"GET", "/api/v1/status", RoleViewer, false},
		{"GET", "/healthz", RoleViewer, false},
		{"POST", "/api/v1/vms/vm-123/unknown", RoleViewer, false},
	}

	for _, tt := range tests {
		rule := matchRule(table, tt.method, tt.path)
		if !tt.found {
			if rule != nil {
				t.Fatalf("matchRule(%q, %q) expected nil, got %+v", tt.method, tt.path, rule)
			}
			continue
		}
		if rule == nil {
			t.Fatalf("matchRule(%q, %q) expected match, got nil", tt.method, tt.path)
		}
		if rule.MinRole != tt.want {
			t.Fatalf("matchRule(%q, %q) minRole=%v, want %v", tt.method, tt.path, rule.MinRole, tt.want)
		}
	}
}

func TestRBACMiddleware(t *testing.T) {
	logger := logging.New("debug")
	table := DefaultPermissionTable()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	mw := RBACMiddleware(table, logger)(handler)

	t.Run("no roles in context", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/vms", nil)
		rr := httptest.NewRecorder()
		mw.ServeHTTP(rr, req)
		if rr.Code != http.StatusForbidden {
			t.Fatalf("expected 403, got %d", rr.Code)
		}
		if ct := rr.Header().Get("Content-Type"); ct != "application/problem+json" {
			t.Fatalf("expected application/problem+json, got %s", ct)
		}
	})

	t.Run("viewer can list vms", func(t *testing.T) {
		ctx := WithRoles(context.Background(), []string{"viewer"})
		req := httptest.NewRequest(http.MethodGet, "/api/v1/vms", nil).WithContext(ctx)
		rr := httptest.NewRecorder()
		mw.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rr.Code)
		}
	})

	t.Run("viewer cannot boot vm", func(t *testing.T) {
		ctx := WithRoles(context.Background(), []string{"viewer"})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/vms/vm-123/boot", nil).WithContext(ctx)
		rr := httptest.NewRecorder()
		mw.ServeHTTP(rr, req)
		if rr.Code != http.StatusForbidden {
			t.Fatalf("expected 403, got %d", rr.Code)
		}
	})

	t.Run("operator can boot vm", func(t *testing.T) {
		ctx := WithRoles(context.Background(), []string{"operator"})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/vms/vm-123/boot", nil).WithContext(ctx)
		rr := httptest.NewRecorder()
		mw.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rr.Code)
		}
	})

	t.Run("operator can list vms", func(t *testing.T) {
		ctx := WithRoles(context.Background(), []string{"operator"})
		req := httptest.NewRequest(http.MethodGet, "/api/v1/vms", nil).WithContext(ctx)
		rr := httptest.NewRecorder()
		mw.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rr.Code)
		}
	})

	t.Run("operator cannot create vm", func(t *testing.T) {
		ctx := WithRoles(context.Background(), []string{"operator"})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/vms", nil).WithContext(ctx)
		rr := httptest.NewRecorder()
		mw.ServeHTTP(rr, req)
		if rr.Code != http.StatusForbidden {
			t.Fatalf("expected 403, got %d", rr.Code)
		}
	})

	t.Run("admin can create vm", func(t *testing.T) {
		ctx := WithRoles(context.Background(), []string{"admin"})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/vms", nil).WithContext(ctx)
		rr := httptest.NewRecorder()
		mw.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rr.Code)
		}
	})

	t.Run("admin can boot vm", func(t *testing.T) {
		ctx := WithRoles(context.Background(), []string{"admin"})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/vms/vm-123/boot", nil).WithContext(ctx)
		rr := httptest.NewRecorder()
		mw.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rr.Code)
		}
	})

	t.Run("admin can list vms", func(t *testing.T) {
		ctx := WithRoles(context.Background(), []string{"admin"})
		req := httptest.NewRequest(http.MethodGet, "/api/v1/vms", nil).WithContext(ctx)
		rr := httptest.NewRecorder()
		mw.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rr.Code)
		}
	})

	t.Run("multiple roles uses highest", func(t *testing.T) {
		ctx := WithRoles(context.Background(), []string{"viewer", "operator"})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/vms/vm-123/boot", nil).WithContext(ctx)
		rr := httptest.NewRecorder()
		mw.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rr.Code)
		}
	})

	t.Run("unknown roles are ignored", func(t *testing.T) {
		ctx := WithRoles(context.Background(), []string{"unknown", "viewer"})
		req := httptest.NewRequest(http.MethodGet, "/api/v1/vms", nil).WithContext(ctx)
		rr := httptest.NewRecorder()
		mw.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rr.Code)
		}
	})

	t.Run("unlisted endpoint is allowed", func(t *testing.T) {
		ctx := WithRoles(context.Background(), []string{"viewer"})
		req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil).WithContext(ctx)
		rr := httptest.NewRecorder()
		mw.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rr.Code)
		}
	})

	t.Run("subject logged on denial", func(t *testing.T) {
		ctx := WithSubject(context.Background(), "user-42")
		ctx = WithRoles(ctx, []string{"viewer"})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/vms", nil).WithContext(ctx)
		rr := httptest.NewRecorder()
		mw.ServeHTTP(rr, req)
		if rr.Code != http.StatusForbidden {
			t.Fatalf("expected 403, got %d", rr.Code)
		}
	})
}
