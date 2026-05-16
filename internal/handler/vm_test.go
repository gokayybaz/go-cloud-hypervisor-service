package handler

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gokaybaz/go-cloud-hypervisor-service/internal/service"
	"github.com/gokaybaz/go-cloud-hypervisor-service/internal/store"
	"github.com/gokaybaz/go-cloud-hypervisor-service/pkg/api"
	"github.com/gokaybaz/go-cloud-hypervisor-service/pkg/api/problem"
	"github.com/gokaybaz/go-cloud-hypervisor-service/pkg/logging"
	"github.com/gokaybaz/go-cloud-hypervisor-service/pkg/vmm"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

type testLogger struct{}

func (testLogger) Info(string, ...any)  {}
func (testLogger) Error(string, ...any) {}
func (testLogger) Debug(string, ...any) {}
func (testLogger) Warn(string, ...any)  {}
func (n testLogger) WithContext(context.Context) logging.Logger { return n }
func (n testLogger) With(...any) logging.Logger                 { return n }

func newTestRouter(t *testing.T) (*api.Router, *service.Service) {
	t.Helper()
	st := store.New(testLogger{}, nil)
	svc := service.New(st, testLogger{}, nil)
	router := api.NewRouter(testLogger{}, nil, nil, nil, nil)
	Register(router, svc, testLogger{}, nil)
	return router, svc
}

func decodeProblem(t *testing.T, body io.Reader) *problem.Detail {
	t.Helper()
	var p problem.Detail
	if err := json.NewDecoder(body).Decode(&p); err != nil {
		t.Fatalf("decode problem: %v", err)
	}
	return &p
}

// ---------------------------------------------------------------------------
// POST /vms
// ---------------------------------------------------------------------------

func TestCreateVM(t *testing.T) {
	router, _ := newTestRouter(t)
	ts := httptest.NewServer(router.Handler())
	defer ts.Close()

	reqBody := `{
		"name": "test-vm",
		"cpus": {"boot_vcpus": 2, "max_vcpus": 4},
		"memory": {"size": 1024},
		"kernel": {"path": "/boot/vmlinuz"},
		"disks": [{"path": "/disk.raw"}]
	}`

	resp, err := http.Post(ts.URL+"/api/v1/vms", "application/json", strings.NewReader(reqBody))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 201, got %d: %s", resp.StatusCode, string(body))
	}

	var vm VMResponse
	if err := json.NewDecoder(resp.Body).Decode(&vm); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if vm.Name != "test-vm" {
		t.Fatalf("expected name 'test-vm', got %q", vm.Name)
	}
	if vm.Status != "created" {
		t.Fatalf("expected status 'created', got %q", vm.Status)
	}
	if vm.ID == "" {
		t.Fatal("expected non-empty ID")
	}
	if vm.Config.CPUs == nil || vm.Config.CPUs.BootVCPUs != 2 {
		t.Fatal("expected cpus config")
	}
}

func TestCreateVMInvalidJSON(t *testing.T) {
	router, _ := newTestRouter(t)
	ts := httptest.NewServer(router.Handler())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/v1/vms", "application/json", strings.NewReader("not json"))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
	p := decodeProblem(t, resp.Body)
	if p.Status != http.StatusBadRequest {
		t.Fatalf("expected status %d in problem, got %d", http.StatusBadRequest, p.Status)
	}
	if p.Type == "" {
		t.Fatal("expected problem type")
	}
}

func TestCreateVMValidationErrors(t *testing.T) {
	router, _ := newTestRouter(t)
	ts := httptest.NewServer(router.Handler())
	defer ts.Close()

	reqBody := `{
		"name": "",
		"cpus": {"boot_vcpus": 0, "max_vcpus": 0},
		"memory": {"size": 32},
		"kernel": {"path": ""},
		"disks": []
	}`

	resp, err := http.Post(ts.URL+"/api/v1/vms", "application/json", strings.NewReader(reqBody))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 400, got %d: %s", resp.StatusCode, string(body))
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("expected content-type application/problem+json, got %q", ct)
	}

	p := decodeProblem(t, resp.Body)
	if len(p.Errors) == 0 {
		t.Fatal("expected validation errors")
	}

	fieldMsgs := make(map[string]string)
	for _, e := range p.Errors {
		fieldMsgs[e.Field] = e.Message
	}

	if _, ok := fieldMsgs["name"]; !ok {
		t.Fatal("expected error for name field")
	}
	if _, ok := fieldMsgs["cpus.boot_vcpus"]; !ok {
		t.Fatal("expected error for cpus.boot_vcpus field")
	}
	if _, ok := fieldMsgs["memory.size"]; !ok {
		t.Fatal("expected error for memory.size field")
	}
	if _, ok := fieldMsgs["kernel.path"]; !ok {
		t.Fatal("expected error for kernel.path field")
	}
	if _, ok := fieldMsgs["disks"]; !ok {
		t.Fatal("expected error for disks field")
	}
}

func TestCreateVMMaxVcpusLessThanBoot(t *testing.T) {
	router, _ := newTestRouter(t)
	ts := httptest.NewServer(router.Handler())
	defer ts.Close()

	reqBody := `{
		"name": "bad-cpu",
		"cpus": {"boot_vcpus": 4, "max_vcpus": 2},
		"memory": {"size": 1024},
		"kernel": {"path": "/boot/vmlinuz"},
		"disks": [{"path": "/disk.raw"}]
	}`

	resp, err := http.Post(ts.URL+"/api/v1/vms", "application/json", strings.NewReader(reqBody))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
	p := decodeProblem(t, resp.Body)
	var found bool
	for _, e := range p.Errors {
		if e.Field == "cpus.max_vcpus" {
			found = true
			if !strings.Contains(e.Message, "boot_vcpus") {
				t.Fatalf("expected message referencing boot_vcpus, got %q", e.Message)
			}
		}
	}
	if !found {
		t.Fatal("expected cpus.max_vcpus error")
	}
}

// ---------------------------------------------------------------------------
// GET /vms
// ---------------------------------------------------------------------------

func TestListVMs(t *testing.T) {
	router, svc := newTestRouter(t)
	ts := httptest.NewServer(router.Handler())
	defer ts.Close()

	// Seed two VMs.
	_, _ = svc.CreateVM(context.Background(), service.CreateVMRequest{
		Name: "vm-1",
		Config: vmmConfig("/boot1", "/disk1"),
	})
	_, _ = svc.CreateVM(context.Background(), service.CreateVMRequest{
		Name: "vm-2",
		Config: vmmConfig("/boot2", "/disk2"),
	})

	resp, err := http.Get(ts.URL + "/api/v1/vms")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var vms []VMResponse
	if err := json.NewDecoder(resp.Body).Decode(&vms); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(vms) != 2 {
		t.Fatalf("expected 2 vms, got %d", len(vms))
	}
}

func TestListVMsEmpty(t *testing.T) {
	router, _ := newTestRouter(t)
	ts := httptest.NewServer(router.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/vms")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var vms []VMResponse
	if err := json.NewDecoder(resp.Body).Decode(&vms); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(vms) != 0 {
		t.Fatalf("expected 0 vms, got %d", len(vms))
	}
}

// ---------------------------------------------------------------------------
// GET /vms/{id}
// ---------------------------------------------------------------------------

func TestGetVM(t *testing.T) {
	router, svc := newTestRouter(t)
	ts := httptest.NewServer(router.Handler())
	defer ts.Close()

	vm, _ := svc.CreateVM(context.Background(), service.CreateVMRequest{
		Name: "get-me",
		Config: vmmConfig("/boot", "/disk"),
	})

	resp, err := http.Get(ts.URL + "/api/v1/vms/" + vm.ID)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var got VMResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ID != vm.ID {
		t.Fatalf("expected id %q, got %q", vm.ID, got.ID)
	}
	if got.Name != "get-me" {
		t.Fatalf("expected name 'get-me', got %q", got.Name)
	}
}

func TestGetVMNotFound(t *testing.T) {
	router, _ := newTestRouter(t)
	ts := httptest.NewServer(router.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/vms/nonexistent")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("expected content-type application/problem+json, got %q", ct)
	}
	p := decodeProblem(t, resp.Body)
	if p.Status != http.StatusNotFound {
		t.Fatalf("expected problem status %d, got %d", http.StatusNotFound, p.Status)
	}
}

// ---------------------------------------------------------------------------
// DELETE /vms/{id}
// ---------------------------------------------------------------------------

func TestDeleteVM(t *testing.T) {
	router, svc := newTestRouter(t)
	ts := httptest.NewServer(router.Handler())
	defer ts.Close()

	vm, _ := svc.CreateVM(context.Background(), service.CreateVMRequest{
		Name: "delete-me",
		Config: vmmConfig("/boot", "/disk"),
	})

	req, _ := http.NewRequest("DELETE", ts.URL+"/api/v1/vms/"+vm.ID, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", resp.StatusCode)
	}

	// Verify deletion.
	_, err = svc.GetVM(context.Background(), vm.ID)
	if err == nil {
		t.Fatal("expected vm to be deleted")
	}
}

func TestDeleteVMNotFound(t *testing.T) {
	router, _ := newTestRouter(t)
	ts := httptest.NewServer(router.Handler())
	defer ts.Close()

	req, _ := http.NewRequest("DELETE", ts.URL+"/api/v1/vms/nonexistent", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
	p := decodeProblem(t, resp.Body)
	if p.Status != http.StatusNotFound {
		t.Fatalf("expected problem status %d, got %d", http.StatusNotFound, p.Status)
	}
}

// ---------------------------------------------------------------------------
// Validation edge cases
// ---------------------------------------------------------------------------

func TestCreateVMWithOptionalNet(t *testing.T) {
	router, _ := newTestRouter(t)
	ts := httptest.NewServer(router.Handler())
	defer ts.Close()

	reqBody := `{
		"name": "net-vm",
		"cpus": {"boot_vcpus": 1, "max_vcpus": 2},
		"memory": {"size": 512},
		"kernel": {"path": "/boot/vmlinuz"},
		"disks": [{"path": "/disk.raw"}],
		"net": [{"tap": "tap0", "ip": "10.0.0.2"}]
	}`

	resp, err := http.Post(ts.URL+"/api/v1/vms", "application/json", strings.NewReader(reqBody))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 201, got %d: %s", resp.StatusCode, string(body))
	}
}

func TestCreateVMNetMissingFields(t *testing.T) {
	router, _ := newTestRouter(t)
	ts := httptest.NewServer(router.Handler())
	defer ts.Close()

	reqBody := `{
		"name": "bad-net",
		"cpus": {"boot_vcpus": 1, "max_vcpus": 2},
		"memory": {"size": 512},
		"kernel": {"path": "/boot/vmlinuz"},
		"disks": [{"path": "/disk.raw"}],
		"net": [{}]
	}`

	resp, err := http.Post(ts.URL+"/api/v1/vms", "application/json", strings.NewReader(reqBody))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
	p := decodeProblem(t, resp.Body)
	var found bool
	for _, e := range p.Errors {
		if e.Field == "net[0]" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected net[0] validation error")
	}
}

// ---------------------------------------------------------------------------
// Helper
// ---------------------------------------------------------------------------

func vmmConfig(kernel, disk string) vmm.VmConfig {
	return vmm.VmConfig{
		CPUs: &vmm.CPUConfig{BootVCPUs: 1, MaxVCPUs: 1},
		Memory: &vmm.MemoryConfig{Size: 256},
		Kernel: &vmm.KernelConfig{Path: kernel},
		Disks: []vmm.DiskConfig{{Path: disk}},
	}
}
