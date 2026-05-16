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
	"github.com/gokaybaz/go-cloud-hypervisor-service/pkg/vmm"
)

// ---------------------------------------------------------------------------
// VM Lifecycle Tests
// ---------------------------------------------------------------------------

type mockVMM struct{}

func (m *mockVMM) Boot(ctx context.Context) error    { return nil }
func (m *mockVMM) Pause(ctx context.Context) error   { return nil }
func (m *mockVMM) Resume(ctx context.Context) error  { return nil }
func (m *mockVMM) Shutdown(ctx context.Context) error { return nil }
func (m *mockVMM) Reboot(ctx context.Context) error  { return nil }

func newLifecycleRouter(t *testing.T) (*api.Router, *service.Service, *store.Store) {
	t.Helper()
	st := store.New(testLogger{}, nil)
	svc := service.New(st, testLogger{}, nil)
	svc.SetVMMClient(&mockVMM{})
	router := api.NewRouter(testLogger{}, nil, nil, nil, nil)
	Register(router, svc, testLogger{}, nil)
	return router, svc, st
}

func seedVM(t *testing.T, svc *service.Service) string {
	t.Helper()
	vm, err := svc.CreateVM(context.Background(), service.CreateVMRequest{
		Name: "lifecycle-vm",
		Config: vmm.VmConfig{
			CPUs:   &vmm.CPUConfig{BootVCPUs: 1, MaxVCPUs: 1},
			Memory: &vmm.MemoryConfig{Size: 256},
			Kernel: &vmm.KernelConfig{Path: "/boot"},
			Disks:  []vmm.DiskConfig{{Path: "/disk.raw"}},
		},
	})
	if err != nil {
		t.Fatalf("seed vm: %v", err)
	}
	return vm.ID
}

func TestBootVM(t *testing.T) {
	router, svc, _ := newLifecycleRouter(t)
	ts := httptest.NewServer(router.Handler())
	defer ts.Close()

	id := seedVM(t, svc)
	resp, err := http.Post(ts.URL+"/api/v1/vms/"+id+"/boot", "", nil)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 204, got %d: %s", resp.StatusCode, string(body))
	}

	vm, _ := svc.GetVM(context.Background(), id)
	if vm.Status != "running" {
		t.Fatalf("expected status running, got %q", vm.Status)
	}
}

func TestPauseVM(t *testing.T) {
	router, svc, _ := newLifecycleRouter(t)
	ts := httptest.NewServer(router.Handler())
	defer ts.Close()

	id := seedVM(t, svc)
	_ = svc.BootVM(context.Background(), id, "test")

	resp, err := http.Post(ts.URL+"/api/v1/vms/"+id+"/pause", "", nil)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 204, got %d: %s", resp.StatusCode, string(body))
	}

	vm, _ := svc.GetVM(context.Background(), id)
	if vm.Status != "paused" {
		t.Fatalf("expected status paused, got %q", vm.Status)
	}
}

func TestResumeVM(t *testing.T) {
	router, svc, _ := newLifecycleRouter(t)
	ts := httptest.NewServer(router.Handler())
	defer ts.Close()

	id := seedVM(t, svc)
	_ = svc.BootVM(context.Background(), id, "test")
	_ = svc.PauseVM(context.Background(), id, "test")

	resp, err := http.Post(ts.URL+"/api/v1/vms/"+id+"/resume", "", nil)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 204, got %d: %s", resp.StatusCode, string(body))
	}

	vm, _ := svc.GetVM(context.Background(), id)
	if vm.Status != "running" {
		t.Fatalf("expected status running, got %q", vm.Status)
	}
}

func TestShutdownVM(t *testing.T) {
	router, svc, _ := newLifecycleRouter(t)
	ts := httptest.NewServer(router.Handler())
	defer ts.Close()

	id := seedVM(t, svc)
	_ = svc.BootVM(context.Background(), id, "test")

	resp, err := http.Post(ts.URL+"/api/v1/vms/"+id+"/shutdown", "", nil)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 204, got %d: %s", resp.StatusCode, string(body))
	}

	vm, _ := svc.GetVM(context.Background(), id)
	if vm.Status != "stopped" {
		t.Fatalf("expected status stopped, got %q", vm.Status)
	}
}

func TestRebootVM(t *testing.T) {
	router, svc, _ := newLifecycleRouter(t)
	ts := httptest.NewServer(router.Handler())
	defer ts.Close()

	id := seedVM(t, svc)
	_ = svc.BootVM(context.Background(), id, "test")

	resp, err := http.Post(ts.URL+"/api/v1/vms/"+id+"/reboot", "", nil)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 204, got %d: %s", resp.StatusCode, string(body))
	}

	vm, _ := svc.GetVM(context.Background(), id)
	if vm.Status != "running" {
		t.Fatalf("expected status running, got %q", vm.Status)
	}
}

func TestLifecycleVMNotFound(t *testing.T) {
	router, _, _ := newLifecycleRouter(t)
	ts := httptest.NewServer(router.Handler())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/v1/vms/nonexistent/boot", "", nil)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestLifecycleOperationLog(t *testing.T) {
	router, svc, st := newLifecycleRouter(t)
	ts := httptest.NewServer(router.Handler())
	defer ts.Close()

	id := seedVM(t, svc)

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/vms/"+id+"/boot", nil)
	req.Header.Set("X-User-ID", "alice")
	resp, _ := http.DefaultClient.Do(req)
	if resp != nil {
		resp.Body.Close()
	}

	entries, err := st.VMOperationLog.Read()
	if err != nil {
		t.Fatalf("read op log: %v", err)
	}

	var found bool
	for _, e := range entries {
		if e.VMID == id && e.Operation == "boot" {
			found = true
			if e.User != "alice" {
				t.Fatalf("expected user alice, got %q", e.User)
			}
			if e.Outcome != "success" {
				t.Fatalf("expected outcome success, got %q", e.Outcome)
			}
		}
	}
	if !found {
		t.Fatal("expected boot operation log entry")
	}
}

func TestLifecycleOperationLogAnonymous(t *testing.T) {
	router, svc, st := newLifecycleRouter(t)
	ts := httptest.NewServer(router.Handler())
	defer ts.Close()

	id := seedVM(t, svc)
	resp, _ := http.Post(ts.URL+"/api/v1/vms/"+id+"/shutdown", "", nil)
	if resp != nil {
		resp.Body.Close()
	}

	entries, _ := st.VMOperationLog.Read()
	var found bool
	for _, e := range entries {
		if e.VMID == id && e.Operation == "shutdown" {
			found = true
			if e.User != "anonymous" {
				t.Fatalf("expected user anonymous, got %q", e.User)
			}
		}
	}
	if !found {
		t.Fatal("expected shutdown operation log entry")
	}
}

// ---------------------------------------------------------------------------
// Disk Tests
// ---------------------------------------------------------------------------

func TestAddDisk(t *testing.T) {
	router, svc, _ := newLifecycleRouter(t)
	ts := httptest.NewServer(router.Handler())
	defer ts.Close()

	id := seedVM(t, svc)
	reqBody := `{"path": "/extra.raw", "readonly": true}`
	resp, err := http.Post(ts.URL+"/api/v1/vms/"+id+"/disks", "application/json", strings.NewReader(reqBody))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 201, got %d: %s", resp.StatusCode, string(body))
	}

	var disk DiskResponse
	if err := json.NewDecoder(resp.Body).Decode(&disk); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if disk.Path != "/extra.raw" {
		t.Fatalf("expected path /extra.raw, got %q", disk.Path)
	}
	if !disk.Readonly {
		t.Fatal("expected readonly true")
	}
}

func TestRemoveDisk(t *testing.T) {
	router, svc, _ := newLifecycleRouter(t)
	ts := httptest.NewServer(router.Handler())
	defer ts.Close()

	id := seedVM(t, svc)
	req, _ := http.NewRequest("DELETE", ts.URL+"/api/v1/vms/"+id+"/disks/disk-0", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(body))
	}

	vm, _ := svc.GetVM(context.Background(), id)
	if len(vm.Config.Disks) != 0 {
		t.Fatalf("expected 0 disks, got %d", len(vm.Config.Disks))
	}
}

func TestDiskVMNotFound(t *testing.T) {
	router, _, _ := newLifecycleRouter(t)
	ts := httptest.NewServer(router.Handler())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/v1/vms/nonexistent/disks", "application/json", strings.NewReader(`{"path":"/x.raw"}`))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// Network Tests
// ---------------------------------------------------------------------------

func TestAddInterface(t *testing.T) {
	router, svc, _ := newLifecycleRouter(t)
	ts := httptest.NewServer(router.Handler())
	defer ts.Close()

	id := seedVM(t, svc)
	reqBody := `{"tap": "tap0", "ip": "10.0.0.2", "mac": "02:00:00:00:00:01"}`
	resp, err := http.Post(ts.URL+"/api/v1/vms/"+id+"/interfaces", "application/json", strings.NewReader(reqBody))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 201, got %d: %s", resp.StatusCode, string(body))
	}

	var iface InterfaceResponse
	if err := json.NewDecoder(resp.Body).Decode(&iface); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if iface.Tap != "tap0" {
		t.Fatalf("expected tap tap0, got %q", iface.Tap)
	}
}

func TestAddInterfaceInvalidMAC(t *testing.T) {
	router, svc, _ := newLifecycleRouter(t)
	ts := httptest.NewServer(router.Handler())
	defer ts.Close()

	id := seedVM(t, svc)
	reqBody := `{"mac": "not-a-mac"}`
	resp, err := http.Post(ts.URL+"/api/v1/vms/"+id+"/interfaces", "application/json", strings.NewReader(reqBody))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 400, got %d: %s", resp.StatusCode, string(body))
	}
	p := decodeProblem(t, resp.Body)
	var found bool
	for _, e := range p.Errors {
		if e.Field == "mac" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected mac validation error")
	}
}

func TestAddInterfaceInvalidIP(t *testing.T) {
	router, svc, _ := newLifecycleRouter(t)
	ts := httptest.NewServer(router.Handler())
	defer ts.Close()

	id := seedVM(t, svc)
	reqBody := `{"ip": "not-an-ip"}`
	resp, err := http.Post(ts.URL+"/api/v1/vms/"+id+"/interfaces", "application/json", strings.NewReader(reqBody))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 400, got %d: %s", resp.StatusCode, string(body))
	}
}

func TestRemoveInterface(t *testing.T) {
	router, svc, _ := newLifecycleRouter(t)
	ts := httptest.NewServer(router.Handler())
	defer ts.Close()

	id := seedVM(t, svc)
	// Add an interface first.
	http.Post(ts.URL+"/api/v1/vms/"+id+"/interfaces", "application/json", strings.NewReader(`{"tap":"tap0"}`))

	req, _ := http.NewRequest("DELETE", ts.URL+"/api/v1/vms/"+id+"/interfaces/eth0", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(body))
	}

	vm, _ := svc.GetVM(context.Background(), id)
	if len(vm.Config.Net) != 0 {
		t.Fatalf("expected 0 interfaces, got %d", len(vm.Config.Net))
	}
}

func TestPatchInterface(t *testing.T) {
	router, svc, _ := newLifecycleRouter(t)
	ts := httptest.NewServer(router.Handler())
	defer ts.Close()

	id := seedVM(t, svc)
	http.Post(ts.URL+"/api/v1/vms/"+id+"/interfaces", "application/json", strings.NewReader(`{"tap":"tap0"}`))

	reqBody := `{"ip": "192.168.1.10"}`
	req, _ := http.NewRequest("PATCH", ts.URL+"/api/v1/vms/"+id+"/interfaces/eth0", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(body))
	}

	vm, _ := svc.GetVM(context.Background(), id)
	if vm.Config.Net[0].IP != "192.168.1.10" {
		t.Fatalf("expected IP 192.168.1.10, got %q", vm.Config.Net[0].IP)
	}
}

// ---------------------------------------------------------------------------
// Resource Tests
// ---------------------------------------------------------------------------

func TestPatchCPU(t *testing.T) {
	router, svc, _ := newLifecycleRouter(t)
	ts := httptest.NewServer(router.Handler())
	defer ts.Close()

	id := seedVM(t, svc)
	reqBody := `{"count": 4}`
	req, _ := http.NewRequest("PATCH", ts.URL+"/api/v1/vms/"+id+"/cpu", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(body))
	}

	var result CPUResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if result.Requested != 4 || result.Effective != 4 {
		t.Fatalf("expected requested=4 effective=4, got %+v", result)
	}

	vm, _ := svc.GetVM(context.Background(), id)
	if vm.Config.CPUs.BootVCPUs != 4 {
		t.Fatalf("expected 4 vcpus, got %d", vm.Config.CPUs.BootVCPUs)
	}
}

func TestPatchMemory(t *testing.T) {
	router, svc, _ := newLifecycleRouter(t)
	ts := httptest.NewServer(router.Handler())
	defer ts.Close()

	id := seedVM(t, svc)
	reqBody := `{"size_mb": 2048}`
	req, _ := http.NewRequest("PATCH", ts.URL+"/api/v1/vms/"+id+"/memory", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(body))
	}

	var result MemoryResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if result.RequestedMB != 2048 || result.EffectiveMB != 2048 {
		t.Fatalf("expected requested=2048 effective=2048, got %+v", result)
	}

	vm, _ := svc.GetVM(context.Background(), id)
	if vm.Config.Memory.Size != 2048 {
		t.Fatalf("expected 2048 MB, got %d", vm.Config.Memory.Size)
	}
}

func TestPatchMemoryBelowMinimum(t *testing.T) {
	router, svc, _ := newLifecycleRouter(t)
	ts := httptest.NewServer(router.Handler())
	defer ts.Close()

	id := seedVM(t, svc)
	reqBody := `{"size_mb": 32}`
	req, _ := http.NewRequest("PATCH", ts.URL+"/api/v1/vms/"+id+"/memory", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 400, got %d: %s", resp.StatusCode, string(body))
	}
}
