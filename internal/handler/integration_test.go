package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/websocket"
	"github.com/org/ch-api/internal/service"
	"github.com/org/ch-api/internal/store"
	"github.com/org/ch-api/pkg/api"
	"github.com/org/ch-api/pkg/auth"
	"github.com/org/ch-api/pkg/vmm"
)

const testSecret = "integration-test-secret"

// ---------------------------------------------------------------------------
// Token helpers
// ---------------------------------------------------------------------------

func makeToken(sub string, roles []string, exp time.Time) string {
	claims := jwt.MapClaims{
		"sub":   sub,
		"roles": roles,
		"exp":   exp.Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	ss, _ := token.SignedString([]byte(testSecret))
	return ss
}

func makeTokenWithSecret(sub string, roles []string, exp time.Time, secret string) string {
	claims := jwt.MapClaims{
		"sub":   sub,
		"roles": roles,
		"exp":   exp.Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	ss, _ := token.SignedString([]byte(secret))
	return ss
}

func makeTokenNoSub(roles []string, exp time.Time) string {
	claims := jwt.MapClaims{
		"roles": roles,
		"exp":   exp.Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	ss, _ := token.SignedString([]byte(testSecret))
	return ss
}

// ---------------------------------------------------------------------------
// Router helpers
// ---------------------------------------------------------------------------

func newAuthRouter(t *testing.T) (*api.Router, *service.Service, *store.Store) {
	t.Helper()
	st := store.New(testLogger{}, nil)
	svc := service.New(st, testLogger{}, nil)
	svc.SetVMMClient(&mockVMM{})
	authCfg := &auth.Config{
		Secret:      testSecret,
		RBACEnabled: true,
	}
	router := api.NewRouter(testLogger{}, nil, nil, authCfg, nil)
	Register(router, svc, testLogger{}, nil)
	return router, svc, st
}

func seedVMWithDiskFile(t *testing.T, svc *service.Service) (vmID, diskPath string) {
	t.Helper()
	f, err := os.CreateTemp("", "ch-test-disk-*.raw")
	if err != nil {
		t.Fatalf("create temp disk: %v", err)
	}
	_ = f.Close()
	t.Cleanup(func() { os.Remove(f.Name()) })

	vm, err := svc.CreateVM(context.Background(), service.CreateVMRequest{
		Name: "disk-vm",
		Config: vmm.VmConfig{
			CPUs:   &vmm.CPUConfig{BootVCPUs: 1, MaxVCPUs: 1},
			Memory: &vmm.MemoryConfig{Size: 256},
			Kernel: &vmm.KernelConfig{Path: "/boot"},
			Disks:  []vmm.DiskConfig{{Path: f.Name()}},
		},
	})
	if err != nil {
		t.Fatalf("seed vm: %v", err)
	}
	return vm.ID, f.Name()
}

// ---------------------------------------------------------------------------
// System endpoints
// ---------------------------------------------------------------------------

func TestIntegration_System(t *testing.T) {
	router, _, _ := newAuthRouter(t)
	ts := httptest.NewServer(router.Handler())
	defer ts.Close()

	viewerToken := "Bearer " + makeToken("viewer", []string{"viewer"}, time.Now().Add(time.Hour))

	tests := []struct {
		name       string
		method     string
		path       string
		authHeader string
		wantStatus int
	}{
		{"healthz public", "GET", "/healthz", "", 200},
		{"status no auth", "GET", "/api/v1/status", "", 401},
		{"status with auth", "GET", "/api/v1/status", viewerToken, 200},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest(tt.method, ts.URL+tt.path, nil)
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tt.wantStatus {
				body, _ := io.ReadAll(resp.Body)
				t.Fatalf("expected %d, got %d: %s", tt.wantStatus, resp.StatusCode, string(body))
			}
		})
	}
}

// ---------------------------------------------------------------------------
// VMs
// ---------------------------------------------------------------------------

func TestIntegration_CreateVM(t *testing.T) {
	router, _, _ := newAuthRouter(t)
	ts := httptest.NewServer(router.Handler())
	defer ts.Close()

	adminToken := "Bearer " + makeToken("admin", []string{"admin"}, time.Now().Add(time.Hour))
	viewerToken := "Bearer " + makeToken("viewer", []string{"viewer"}, time.Now().Add(time.Hour))
	operatorToken := "Bearer " + makeToken("operator", []string{"operator"}, time.Now().Add(time.Hour))

	validBody := `{"name":"test-vm","cpus":{"boot_vcpus":2,"max_vcpus":4},"memory":{"size":1024},"kernel":{"path":"/boot/vmlinuz"},"disks":[{"path":"/disk.raw"}]}`
	invalidBody := `{"name":"","cpus":{"boot_vcpus":0,"max_vcpus":0},"memory":{"size":32},"kernel":{"path":""},"disks":[]}`
	badCPU := `{"name":"bad-cpu","cpus":{"boot_vcpus":4,"max_vcpus":2},"memory":{"size":1024},"kernel":{"path":"/boot"},"disks":[{"path":"/disk.raw"}]}`

	tests := []struct {
		name       string
		body       string
		authHeader string
		wantStatus int
		wantType   string
		checkFn    func(t *testing.T, body []byte)
	}{
		{"success", validBody, adminToken, 201, "application/json", func(t *testing.T, b []byte) {
			var vm VMResponse
			if err := json.Unmarshal(b, &vm); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if vm.Name != "test-vm" {
				t.Fatalf("expected name test-vm, got %s", vm.Name)
			}
			if vm.Status != "created" {
				t.Fatalf("expected status created, got %s", vm.Status)
			}
			if vm.ID == "" {
				t.Fatal("expected non-empty ID")
			}
		}},
		{"no auth", validBody, "", 401, "application/problem+json", nil},
		{"viewer forbidden", validBody, viewerToken, 403, "application/problem+json", nil},
		{"operator forbidden", validBody, operatorToken, 403, "application/problem+json", nil},
		{"invalid json", "not json", adminToken, 400, "application/problem+json", nil},
		{"validation error", invalidBody, adminToken, 400, "application/problem+json", func(t *testing.T, b []byte) {
			p := decodeProblem(t, strings.NewReader(string(b)))
			if len(p.Errors) == 0 {
				t.Fatal("expected validation errors")
			}
		}},
		{"max vcpus less than boot", badCPU, adminToken, 400, "application/problem+json", func(t *testing.T, b []byte) {
			p := decodeProblem(t, strings.NewReader(string(b)))
			var found bool
			for _, e := range p.Errors {
				if e.Field == "cpus.max_vcpus" {
					found = true
				}
			}
			if !found {
				t.Fatal("expected cpus.max_vcpus error")
			}
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest("POST", ts.URL+"/api/v1/vms", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != tt.wantStatus {
				body, _ := io.ReadAll(resp.Body)
				t.Fatalf("expected %d, got %d: %s", tt.wantStatus, resp.StatusCode, string(body))
			}
			if tt.wantType != "" {
				ct := resp.Header.Get("Content-Type")
				if !strings.Contains(ct, tt.wantType) {
					t.Fatalf("expected content-type %s, got %s", tt.wantType, ct)
				}
			}
			if tt.checkFn != nil {
				body, _ := io.ReadAll(resp.Body)
				tt.checkFn(t, body)
			}
		})
	}
}

func TestIntegration_ListVMs(t *testing.T) {
	router, svc, _ := newAuthRouter(t)
	ts := httptest.NewServer(router.Handler())
	defer ts.Close()

	viewerToken := "Bearer " + makeToken("viewer", []string{"viewer"}, time.Now().Add(time.Hour))

	_, _ = svc.CreateVM(context.Background(), service.CreateVMRequest{
		Name: "vm-1", Config: vmmConfig("/boot1", "/disk1"),
	})
	_, _ = svc.CreateVM(context.Background(), service.CreateVMRequest{
		Name: "vm-2", Config: vmmConfig("/boot2", "/disk2"),
	})

	tests := []struct {
		name       string
		authHeader string
		wantStatus int
		wantCount  int
	}{
		{"success with items", viewerToken, 200, 2},
		{"no auth", "", 401, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest("GET", ts.URL+"/api/v1/vms", nil)
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != tt.wantStatus {
				body, _ := io.ReadAll(resp.Body)
				t.Fatalf("expected %d, got %d: %s", tt.wantStatus, resp.StatusCode, string(body))
			}
			if tt.wantCount > 0 {
				var vms []VMResponse
				if err := json.NewDecoder(resp.Body).Decode(&vms); err != nil {
					t.Fatalf("decode: %v", err)
				}
				if len(vms) != tt.wantCount {
					t.Fatalf("expected %d vms, got %d", tt.wantCount, len(vms))
				}
			}
		})
	}
}

func TestIntegration_GetVM(t *testing.T) {
	router, svc, _ := newAuthRouter(t)
	ts := httptest.NewServer(router.Handler())
	defer ts.Close()

	vm, _ := svc.CreateVM(context.Background(), service.CreateVMRequest{
		Name: "get-me", Config: vmmConfig("/boot", "/disk"),
	})

	viewerToken := "Bearer " + makeToken("viewer", []string{"viewer"}, time.Now().Add(time.Hour))

	tests := []struct {
		name       string
		path       string
		authHeader string
		wantStatus int
	}{
		{"success", "/api/v1/vms/" + vm.ID, viewerToken, 200},
		{"no auth", "/api/v1/vms/" + vm.ID, "", 401},
		{"not found", "/api/v1/vms/nonexistent", viewerToken, 404},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest("GET", ts.URL+tt.path, nil)
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tt.wantStatus {
				body, _ := io.ReadAll(resp.Body)
				t.Fatalf("expected %d, got %d: %s", tt.wantStatus, resp.StatusCode, string(body))
			}
		})
	}
}

func TestIntegration_DeleteVM(t *testing.T) {
	router, svc, _ := newAuthRouter(t)
	ts := httptest.NewServer(router.Handler())
	defer ts.Close()

	vm, _ := svc.CreateVM(context.Background(), service.CreateVMRequest{
		Name: "delete-me", Config: vmmConfig("/boot", "/disk"),
	})

	adminToken := "Bearer " + makeToken("admin", []string{"admin"}, time.Now().Add(time.Hour))
	viewerToken := "Bearer " + makeToken("viewer", []string{"viewer"}, time.Now().Add(time.Hour))

	tests := []struct {
		name       string
		path       string
		authHeader string
		wantStatus int
	}{
		{"success", "/api/v1/vms/" + vm.ID, adminToken, 204},
		{"no auth", "/api/v1/vms/" + vm.ID, "", 401},
		{"viewer forbidden", "/api/v1/vms/" + vm.ID, viewerToken, 403},
		{"not found", "/api/v1/vms/nonexistent", adminToken, 404},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest("DELETE", ts.URL+tt.path, nil)
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tt.wantStatus {
				body, _ := io.ReadAll(resp.Body)
				t.Fatalf("expected %d, got %d: %s", tt.wantStatus, resp.StatusCode, string(body))
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Lifecycle
// ---------------------------------------------------------------------------

func TestIntegration_Lifecycle(t *testing.T) {
	router, svc, _ := newAuthRouter(t)
	ts := httptest.NewServer(router.Handler())
	defer ts.Close()

	vm, _ := svc.CreateVM(context.Background(), service.CreateVMRequest{
		Name: "lifecycle-vm", Config: vmmConfig("/boot", "/disk"),
	})
	_ = svc.BootVM(context.Background(), vm.ID, "test")

	operatorToken := "Bearer " + makeToken("operator", []string{"operator"}, time.Now().Add(time.Hour))
	viewerToken := "Bearer " + makeToken("viewer", []string{"viewer"}, time.Now().Add(time.Hour))

	tests := []struct {
		name       string
		method     string
		path       string
		authHeader string
		wantStatus int
	}{
		{"boot success", "POST", "/api/v1/vms/" + vm.ID + "/boot", operatorToken, 204},
		{"boot no auth", "POST", "/api/v1/vms/" + vm.ID + "/boot", "", 401},
		{"boot viewer forbidden", "POST", "/api/v1/vms/" + vm.ID + "/boot", viewerToken, 403},
		{"boot not found", "POST", "/api/v1/vms/nonexistent/boot", operatorToken, 404},

		{"pause success", "POST", "/api/v1/vms/" + vm.ID + "/pause", operatorToken, 204},
		{"pause no auth", "POST", "/api/v1/vms/" + vm.ID + "/pause", "", 401},
		{"pause viewer forbidden", "POST", "/api/v1/vms/" + vm.ID + "/pause", viewerToken, 403},

		{"resume success", "POST", "/api/v1/vms/" + vm.ID + "/resume", operatorToken, 204},
		{"resume no auth", "POST", "/api/v1/vms/" + vm.ID + "/resume", "", 401},
		{"resume viewer forbidden", "POST", "/api/v1/vms/" + vm.ID + "/resume", viewerToken, 403},

		{"shutdown success", "POST", "/api/v1/vms/" + vm.ID + "/shutdown", operatorToken, 204},
		{"shutdown no auth", "POST", "/api/v1/vms/" + vm.ID + "/shutdown", "", 401},
		{"shutdown viewer forbidden", "POST", "/api/v1/vms/" + vm.ID + "/shutdown", viewerToken, 403},

		{"reboot success", "POST", "/api/v1/vms/" + vm.ID + "/reboot", operatorToken, 204},
		{"reboot no auth", "POST", "/api/v1/vms/" + vm.ID + "/reboot", "", 401},
		{"reboot viewer forbidden", "POST", "/api/v1/vms/" + vm.ID + "/reboot", viewerToken, 403},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest(tt.method, ts.URL+tt.path, nil)
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tt.wantStatus {
				body, _ := io.ReadAll(resp.Body)
				t.Fatalf("expected %d, got %d: %s", tt.wantStatus, resp.StatusCode, string(body))
			}
		})
	}
}

func TestIntegration_LifecycleUserIdentity(t *testing.T) {
	router, svc, st := newAuthRouter(t)
	ts := httptest.NewServer(router.Handler())
	defer ts.Close()

	vm, _ := svc.CreateVM(context.Background(), service.CreateVMRequest{
		Name: "identity-vm", Config: vmmConfig("/boot", "/disk"),
	})

	operatorToken := "Bearer " + makeToken("operator", []string{"operator"}, time.Now().Add(time.Hour))

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/vms/"+vm.ID+"/boot", nil)
	req.Header.Set("Authorization", operatorToken)
	req.Header.Set("X-User-ID", "alice")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != 204 {
		t.Fatalf("expected 204, got %d", resp.StatusCode)
	}

	entries, err := st.VMOperationLog.Read()
	if err != nil {
		t.Fatalf("read op log: %v", err)
	}

	var found bool
	for _, e := range entries {
		if e.VMID == vm.ID && e.Operation == "boot" && e.User == "alice" {
			found = true
			if e.Outcome != "success" {
				t.Fatalf("expected outcome success, got %q", e.Outcome)
			}
		}
	}
	if !found {
		t.Fatal("expected boot operation log entry for user alice")
	}
}

// ---------------------------------------------------------------------------
// Disks
// ---------------------------------------------------------------------------

func TestIntegration_AddDisk(t *testing.T) {
	router, svc, _ := newAuthRouter(t)
	ts := httptest.NewServer(router.Handler())
	defer ts.Close()

	vmID := seedVM(t, svc)

	adminToken := "Bearer " + makeToken("admin", []string{"admin"}, time.Now().Add(time.Hour))
	operatorToken := "Bearer " + makeToken("operator", []string{"operator"}, time.Now().Add(time.Hour))
	validBody := `{"path": "/extra.raw", "readonly": true}`

	tests := []struct {
		name       string
		body       string
		authHeader string
		wantStatus int
	}{
		{"success", validBody, adminToken, 201},
		{"no auth", validBody, "", 401},
		{"operator forbidden", validBody, operatorToken, 403},
		{"invalid json", "not json", adminToken, 400},
		{"validation error", `{"path":""}`, adminToken, 400},
		{"vm not found", validBody, adminToken, 404},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := ts.URL + "/api/v1/vms/" + vmID + "/disks"
			if tt.name == "vm not found" {
				path = ts.URL + "/api/v1/vms/nonexistent/disks"
			}
			req, _ := http.NewRequest("POST", path, strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tt.wantStatus {
				body, _ := io.ReadAll(resp.Body)
				t.Fatalf("expected %d, got %d: %s", tt.wantStatus, resp.StatusCode, string(body))
			}
		})
	}
}

func TestIntegration_RemoveDisk(t *testing.T) {
	router, svc, _ := newAuthRouter(t)
	ts := httptest.NewServer(router.Handler())
	defer ts.Close()

	vmID := seedVM(t, svc)
	adminToken := "Bearer " + makeToken("admin", []string{"admin"}, time.Now().Add(time.Hour))
	operatorToken := "Bearer " + makeToken("operator", []string{"operator"}, time.Now().Add(time.Hour))

	tests := []struct {
		name       string
		path       string
		authHeader string
		wantStatus int
	}{
		{"success", "/api/v1/vms/" + vmID + "/disks/disk-0", adminToken, 200},
		{"no auth", "/api/v1/vms/" + vmID + "/disks/disk-0", "", 401},
		{"operator forbidden", "/api/v1/vms/" + vmID + "/disks/disk-0", operatorToken, 403},
		{"vm not found", "/api/v1/vms/nonexistent/disks/disk-0", adminToken, 404},
		{"disk not found", "/api/v1/vms/" + vmID + "/disks/disk-99", adminToken, 404},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest("DELETE", ts.URL+tt.path, nil)
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tt.wantStatus {
				body, _ := io.ReadAll(resp.Body)
				t.Fatalf("expected %d, got %d: %s", tt.wantStatus, resp.StatusCode, string(body))
			}
		})
	}
}

func TestIntegration_SnapshotDisk(t *testing.T) {
	router, svc, _ := newAuthRouter(t)
	ts := httptest.NewServer(router.Handler())
	defer ts.Close()

	vmIDWithFile, diskPath := seedVMWithDiskFile(t, svc)
	vmIDNoFile := seedVM(t, svc)

	adminToken := "Bearer " + makeToken("admin", []string{"admin"}, time.Now().Add(time.Hour))
	operatorToken := "Bearer " + makeToken("operator", []string{"operator"}, time.Now().Add(time.Hour))

	tests := []struct {
		name       string
		path       string
		authHeader string
		wantStatus int
	}{
		{"success", "/api/v1/vms/" + vmIDWithFile + "/disks/disk-0/snapshot", adminToken, 200},
		{"no auth", "/api/v1/vms/" + vmIDWithFile + "/disks/disk-0/snapshot", "", 401},
		{"operator forbidden", "/api/v1/vms/" + vmIDWithFile + "/disks/disk-0/snapshot", operatorToken, 403},
		{"vm not found", "/api/v1/vms/nonexistent/disks/disk-0/snapshot", adminToken, 404},
		{"disk not found", "/api/v1/vms/" + vmIDWithFile + "/disks/disk-99/snapshot", adminToken, 404},
		{"source file missing", "/api/v1/vms/" + vmIDNoFile + "/disks/disk-0/snapshot", adminToken, 500},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest("POST", ts.URL+tt.path, nil)
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tt.wantStatus {
				body, _ := io.ReadAll(resp.Body)
				t.Fatalf("expected %d, got %d: %s", tt.wantStatus, resp.StatusCode, string(body))
			}
		})
	}

	snapPath := filepath.Join(filepath.Dir(diskPath), fmt.Sprintf("snapshot-%s-disk-0", vmIDWithFile))
	if _, err := os.Stat(snapPath); os.IsNotExist(err) {
		t.Fatal("expected snapshot file to exist")
	}
}

// ---------------------------------------------------------------------------
// Network
// ---------------------------------------------------------------------------

func TestIntegration_AddInterface(t *testing.T) {
	router, svc, _ := newAuthRouter(t)
	ts := httptest.NewServer(router.Handler())
	defer ts.Close()

	vmID := seedVM(t, svc)

	adminToken := "Bearer " + makeToken("admin", []string{"admin"}, time.Now().Add(time.Hour))
	operatorToken := "Bearer " + makeToken("operator", []string{"operator"}, time.Now().Add(time.Hour))
	validBody := `{"tap":"tap0","ip":"10.0.0.2","mac":"02:00:00:00:00:01"}`

	tests := []struct {
		name       string
		body       string
		authHeader string
		wantStatus int
	}{
		{"success", validBody, adminToken, 201},
		{"no auth", validBody, "", 401},
		{"operator forbidden", validBody, operatorToken, 403},
		{"invalid json", "not json", adminToken, 400},
		{"validation error", `{}`, adminToken, 400},
		{"invalid mac", `{"mac":"not-a-mac"}`, adminToken, 400},
		{"invalid ip", `{"ip":"not-an-ip"}`, adminToken, 400},
		{"vm not found", validBody, adminToken, 404},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := ts.URL + "/api/v1/vms/" + vmID + "/interfaces"
			if tt.name == "vm not found" {
				path = ts.URL + "/api/v1/vms/nonexistent/interfaces"
			}
			req, _ := http.NewRequest("POST", path, strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tt.wantStatus {
				body, _ := io.ReadAll(resp.Body)
				t.Fatalf("expected %d, got %d: %s", tt.wantStatus, resp.StatusCode, string(body))
			}
		})
	}
}

func TestIntegration_RemoveInterface(t *testing.T) {
	router, svc, _ := newAuthRouter(t)
	ts := httptest.NewServer(router.Handler())
	defer ts.Close()

	vmID := seedVM(t, svc)
	adminToken := "Bearer " + makeToken("admin", []string{"admin"}, time.Now().Add(time.Hour))
	operatorToken := "Bearer " + makeToken("operator", []string{"operator"}, time.Now().Add(time.Hour))

	// Pre-add an interface
	addReq, _ := http.NewRequest("POST", ts.URL+"/api/v1/vms/"+vmID+"/interfaces", strings.NewReader(`{"tap":"tap0"}`))
	addReq.Header.Set("Authorization", adminToken)
	addReq.Header.Set("Content-Type", "application/json")
	addResp, err := http.DefaultClient.Do(addReq)
	if err != nil {
		t.Fatalf("setup add interface failed: %v", err)
	}
	addResp.Body.Close()

	tests := []struct {
		name       string
		path       string
		authHeader string
		wantStatus int
	}{
		{"success", "/api/v1/vms/" + vmID + "/interfaces/eth0", adminToken, 200},
		{"no auth", "/api/v1/vms/" + vmID + "/interfaces/eth0", "", 401},
		{"operator forbidden", "/api/v1/vms/" + vmID + "/interfaces/eth0", operatorToken, 403},
		{"vm not found", "/api/v1/vms/nonexistent/interfaces/eth0", adminToken, 404},
		{"interface not found", "/api/v1/vms/" + vmID + "/interfaces/eth99", adminToken, 404},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest("DELETE", ts.URL+tt.path, nil)
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tt.wantStatus {
				body, _ := io.ReadAll(resp.Body)
				t.Fatalf("expected %d, got %d: %s", tt.wantStatus, resp.StatusCode, string(body))
			}
		})
	}
}

func TestIntegration_PatchInterface(t *testing.T) {
	router, svc, _ := newAuthRouter(t)
	ts := httptest.NewServer(router.Handler())
	defer ts.Close()

	vmID := seedVM(t, svc)
	adminToken := "Bearer " + makeToken("admin", []string{"admin"}, time.Now().Add(time.Hour))
	operatorToken := "Bearer " + makeToken("operator", []string{"operator"}, time.Now().Add(time.Hour))
	patchBody := `{"ip":"192.168.1.10"}`

	// Pre-add an interface
	addReq, _ := http.NewRequest("POST", ts.URL+"/api/v1/vms/"+vmID+"/interfaces", strings.NewReader(`{"tap":"tap0"}`))
	addReq.Header.Set("Authorization", adminToken)
	addReq.Header.Set("Content-Type", "application/json")
	addResp, err := http.DefaultClient.Do(addReq)
	if err != nil {
		t.Fatalf("setup add interface failed: %v", err)
	}
	addResp.Body.Close()

	tests := []struct {
		name       string
		body       string
		path       string
		authHeader string
		wantStatus int
	}{
		{"success", patchBody, "/api/v1/vms/" + vmID + "/interfaces/eth0", adminToken, 200},
		{"no auth", patchBody, "/api/v1/vms/" + vmID + "/interfaces/eth0", "", 401},
		{"operator forbidden", patchBody, "/api/v1/vms/" + vmID + "/interfaces/eth0", operatorToken, 403},
		{"invalid json", "not json", "/api/v1/vms/" + vmID + "/interfaces/eth0", adminToken, 400},
		{"invalid mac", `{"mac":"bad"}`, "/api/v1/vms/" + vmID + "/interfaces/eth0", adminToken, 400},
		{"vm not found", patchBody, "/api/v1/vms/nonexistent/interfaces/eth0", adminToken, 404},
		{"interface not found", patchBody, "/api/v1/vms/" + vmID + "/interfaces/eth99", adminToken, 404},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest("PATCH", ts.URL+tt.path, strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tt.wantStatus {
				body, _ := io.ReadAll(resp.Body)
				t.Fatalf("expected %d, got %d: %s", tt.wantStatus, resp.StatusCode, string(body))
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Resources
// ---------------------------------------------------------------------------

func TestIntegration_PatchCPU(t *testing.T) {
	router, svc, _ := newAuthRouter(t)
	ts := httptest.NewServer(router.Handler())
	defer ts.Close()

	vmID := seedVM(t, svc)
	operatorToken := "Bearer " + makeToken("operator", []string{"operator"}, time.Now().Add(time.Hour))
	viewerToken := "Bearer " + makeToken("viewer", []string{"viewer"}, time.Now().Add(time.Hour))

	tests := []struct {
		name       string
		body       string
		authHeader string
		wantStatus int
		checkFn    func(t *testing.T, body []byte)
	}{
		{"success", `{"count":4}`, operatorToken, 200, func(t *testing.T, b []byte) {
			var r CPUResponse
			if err := json.Unmarshal(b, &r); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if r.Effective != 4 {
				t.Fatalf("expected effective 4, got %d", r.Effective)
			}
		}},
		{"no auth", `{"count":4}`, "", 401, nil},
		{"viewer forbidden", `{"count":4}`, viewerToken, 403, nil},
		{"invalid json", "not json", operatorToken, 400, nil},
		{"validation error", `{"count":0}`, operatorToken, 400, nil},
		{"vm not found", `{"count":4}`, operatorToken, 404, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := ts.URL + "/api/v1/vms/" + vmID + "/cpu"
			if tt.name == "vm not found" {
				path = ts.URL + "/api/v1/vms/nonexistent/cpu"
			}
			req, _ := http.NewRequest("PATCH", path, strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tt.wantStatus {
				body, _ := io.ReadAll(resp.Body)
				t.Fatalf("expected %d, got %d: %s", tt.wantStatus, resp.StatusCode, string(body))
			}
			if tt.checkFn != nil {
				body, _ := io.ReadAll(resp.Body)
				tt.checkFn(t, body)
			}
		})
	}
}

func TestIntegration_PatchMemory(t *testing.T) {
	router, svc, _ := newAuthRouter(t)
	ts := httptest.NewServer(router.Handler())
	defer ts.Close()

	vmID := seedVM(t, svc)
	operatorToken := "Bearer " + makeToken("operator", []string{"operator"}, time.Now().Add(time.Hour))
	viewerToken := "Bearer " + makeToken("viewer", []string{"viewer"}, time.Now().Add(time.Hour))

	tests := []struct {
		name       string
		body       string
		authHeader string
		wantStatus int
		checkFn    func(t *testing.T, body []byte)
	}{
		{"success", `{"size_mb":2048}`, operatorToken, 200, func(t *testing.T, b []byte) {
			var r MemoryResponse
			if err := json.Unmarshal(b, &r); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if r.EffectiveMB != 2048 {
				t.Fatalf("expected effective 2048, got %d", r.EffectiveMB)
			}
		}},
		{"no auth", `{"size_mb":2048}`, "", 401, nil},
		{"viewer forbidden", `{"size_mb":2048}`, viewerToken, 403, nil},
		{"invalid json", "not json", operatorToken, 400, nil},
		{"validation error", `{"size_mb":32}`, operatorToken, 400, nil},
		{"vm not found", `{"size_mb":2048}`, operatorToken, 404, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := ts.URL + "/api/v1/vms/" + vmID + "/memory"
			if tt.name == "vm not found" {
				path = ts.URL + "/api/v1/vms/nonexistent/memory"
			}
			req, _ := http.NewRequest("PATCH", path, strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tt.wantStatus {
				body, _ := io.ReadAll(resp.Body)
				t.Fatalf("expected %d, got %d: %s", tt.wantStatus, resp.StatusCode, string(body))
			}
			if tt.checkFn != nil {
				body, _ := io.ReadAll(resp.Body)
				tt.checkFn(t, body)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Console
// ---------------------------------------------------------------------------

func TestIntegration_Console(t *testing.T) {
	router, svc, _ := newAuthRouter(t)
	ts := httptest.NewServer(router.Handler())
	defer ts.Close()

	vm, _ := svc.CreateVM(context.Background(), service.CreateVMRequest{
		Name:   "console-vm",
		Config: vmmConfig("/boot", "/disk"),
	})

	viewerToken := "Bearer " + makeToken("viewer", []string{"viewer"}, time.Now().Add(time.Hour))
	noRolesToken := "Bearer " + makeToken("noroles", []string{}, time.Now().Add(time.Hour))

	wsBase := strings.Replace(ts.URL, "http", "ws", 1)

	tests := []struct {
		name       string
		vmID       string
		authHeader string
		wantStatus int
		wantDial   bool
	}{
		{"success", vm.ID, viewerToken, 101, true},
		{"no auth", vm.ID, "", 401, false},
		{"no roles forbidden", vm.ID, noRolesToken, 403, false},
		{"vm not found", "nonexistent", viewerToken, 404, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wsURL := wsBase + "/api/v1/vms/" + tt.vmID + "/console"
			headers := http.Header{}
			if tt.authHeader != "" {
				headers.Set("Authorization", tt.authHeader)
			}
			conn, resp, err := websocket.DefaultDialer.Dial(wsURL, headers)
			if tt.wantDial {
				if err != nil {
					t.Fatalf("dial failed: %v", err)
				}
				defer conn.Close()
				if resp.StatusCode != http.StatusSwitchingProtocols {
					t.Fatalf("expected 101, got %d", resp.StatusCode)
				}
				conn.SetReadDeadline(time.Now().Add(2 * time.Second))
				_, msg, err := conn.ReadMessage()
				if err != nil {
					t.Fatalf("read message: %v", err)
				}
				if !strings.Contains(string(msg), "console line") {
					t.Fatalf("expected console line, got %q", string(msg))
				}
				conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
			} else {
				if err == nil {
					if conn != nil {
						conn.Close()
					}
					t.Fatal("expected dial error")
				}
				if resp != nil && resp.StatusCode != tt.wantStatus {
					t.Fatalf("expected %d, got %d", tt.wantStatus, resp.StatusCode)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Auth cross-cutting
// ---------------------------------------------------------------------------

func TestIntegration_Auth(t *testing.T) {
	router, _, _ := newAuthRouter(t)
	ts := httptest.NewServer(router.Handler())
	defer ts.Close()

	validAdminToken := "Bearer " + makeToken("admin", []string{"admin"}, time.Now().Add(time.Hour))
	expiredToken := "Bearer " + makeToken("expired", []string{"admin"}, time.Now().Add(-time.Hour))
	badSigToken := "Bearer " + makeTokenWithSecret("bad", []string{"admin"}, time.Now().Add(time.Hour), "wrong-secret")
	missingSubToken := "Bearer " + makeTokenNoSub([]string{"admin"}, time.Now().Add(time.Hour))

	tests := []struct {
		name       string
		authHeader string
		wantStatus int
	}{
		{"missing header", "", 401},
		{"invalid format", "Basic dXNlcjpwYXNz", 401},
		{"expired token", expiredToken, 401},
		{"invalid signature", badSigToken, 401},
		{"missing subject", missingSubToken, 401},
		{"valid token", validAdminToken, 200},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest("GET", ts.URL+"/api/v1/vms", nil)
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tt.wantStatus {
				body, _ := io.ReadAll(resp.Body)
				t.Fatalf("expected %d, got %d: %s", tt.wantStatus, resp.StatusCode, string(body))
			}
		})
	}
}
