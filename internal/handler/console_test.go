package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/gokaybaz/go-cloud-hypervisor-service/internal/service"
	"github.com/gokaybaz/go-cloud-hypervisor-service/internal/store"
	"github.com/gokaybaz/go-cloud-hypervisor-service/pkg/api"
	"github.com/gokaybaz/go-cloud-hypervisor-service/pkg/vmm"
)

func newConsoleRouter(t *testing.T) (*api.Router, *service.Service) {
	t.Helper()
	st := store.New(testLogger{}, nil)
	svc := service.New(st, testLogger{}, nil)
	router := api.NewRouter(testLogger{}, nil, nil, nil, nil)
	Register(router, svc, testLogger{}, nil)
	return router, svc
}

func TestConsoleStream(t *testing.T) {
	router, svc := newConsoleRouter(t)
	ts := httptest.NewServer(router.Handler())
	defer ts.Close()

	vm, _ := svc.CreateVM(nil, service.CreateVMRequest{
		Name: "console-vm",
		Config: vmm.VmConfig{
			CPUs:   &vmm.CPUConfig{BootVCPUs: 1, MaxVCPUs: 1},
			Memory: &vmm.MemoryConfig{Size: 256},
			Kernel: &vmm.KernelConfig{Path: "/boot"},
			Disks:  []vmm.DiskConfig{{Path: "/disk.raw"}},
		},
	})

	wsURL := strings.Replace(ts.URL, "http", "ws", 1) + "/api/v1/vms/" + vm.ID + "/console"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("websocket dial failed: %v", err)
	}
	defer conn.Close()

	// Read a few messages.
	for i := 0; i < 3; i++ {
		conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, msg, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read message: %v", err)
		}
		if !strings.Contains(string(msg), "console line") {
			t.Fatalf("expected console line in message, got %q", string(msg))
		}
	}

	// Graceful close.
	conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
}

func TestConsoleVMNotFound(t *testing.T) {
	router, _ := newConsoleRouter(t)
	ts := httptest.NewServer(router.Handler())
	defer ts.Close()

	wsURL := strings.Replace(ts.URL, "http", "ws", 1) + "/api/v1/vms/nonexistent/console"
	_, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err == nil {
		t.Fatal("expected dial error for nonexistent vm")
	}
	if resp != nil && resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}
