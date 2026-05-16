// Package e2e provides end-to-end tests that exercise the full VM lifecycle
// against a real Cloud Hypervisor binary.
//
// Tests automatically skip when the cloud-hypervisor binary is absent.
// Set CH_BINARY to override the binary path and CH_KERNEL to point to a
// compiled kernel image (vmlinux or bzImage) required for boot tests.
package e2e

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/gokaybaz/go-cloud-hypervisor-service/pkg/vmm"
)

const (
	chBinaryEnv  = "CH_BINARY"
	kernelEnv    = "CH_KERNEL"
	defaultBin   = "cloud-hypervisor"
	socketName   = "ch-e2e.sock"
	waitTimeout  = 10 * time.Second
	pollInterval = 100 * time.Millisecond
)

// ---------------------------------------------------------------------------
// Binary discovery
// ---------------------------------------------------------------------------

// findCHBinary returns the path to the cloud-hypervisor binary, or an empty
// string when it cannot be found.
func findCHBinary() string {
	if p := os.Getenv(chBinaryEnv); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if p, err := exec.LookPath(defaultBin); err == nil {
		return p
	}
	return ""
}

// findKernel returns the path to a kernel image supplied via CH_KERNEL, or
// an empty string when absent.
func findKernel() string {
	if p := os.Getenv(kernelEnv); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// CH process management
// ---------------------------------------------------------------------------

// chProcess wraps a running cloud-hypervisor child process and its socket.
type chProcess struct {
	cmd    *exec.Cmd
	socket string
}

// startCH starts cloud-hypervisor with an API socket in a temporary directory.
// The caller is responsible for calling stop() to kill the process and remove
// the socket.
func startCH(t testing.TB, binPath string) *chProcess {
	t.Helper()

	dir := t.TempDir()
	socket := filepath.Join(dir, socketName)

	// CH is started with --api-socket only.  VM configuration (kernel, etc.)
	// is supplied later via the REST API.
	cmd := exec.Command(binPath, "--api-socket", socket)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Dir = dir

	if err := cmd.Start(); err != nil {
		t.Fatalf("start cloud-hypervisor: %v", err)
	}

	proc := &chProcess{cmd: cmd, socket: socket}

	// Wait for the Unix socket to appear.
	deadline := time.Now().Add(waitTimeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(socket); err == nil {
			break
		}
		time.Sleep(pollInterval)
	}

	if _, err := os.Stat(socket); err != nil {
		proc.stop()
		t.Fatalf("cloud-hypervisor did not create socket %q within %v", socket, waitTimeout)
	}

	return proc
}

// stop kills the child process and removes the socket.
func (p *chProcess) stop() {
	if p.cmd != nil && p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
		_ = p.cmd.Wait()
	}
	if p.socket != "" {
		_ = os.Remove(p.socket)
	}
}

// ---------------------------------------------------------------------------
// Client helper
// ---------------------------------------------------------------------------

func newClient(socket string) *vmm.Client {
	return vmm.New(vmm.Config{
		Transport:      vmm.TransportUnixSock,
		Address:        socket,
		RequestTimeout: 30 * time.Second,
	})
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestVMMClient_Ping(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("Cloud Hypervisor requires Linux/KVM")
	}

	bin := findCHBinary()
	if bin == "" {
		t.Skipf("cloud-hypervisor binary not found (set %s or add to $PATH)", chBinaryEnv)
	}

	proc := startCH(t, bin)
	defer proc.stop()

	client := newClient(proc.socket)
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := client.Ping(ctx); err != nil {
		t.Fatalf("ping failed: %v", err)
	}
}

func TestVMMClient_Version(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("Cloud Hypervisor requires Linux/KVM")
	}

	bin := findCHBinary()
	if bin == "" {
		t.Skipf("cloud-hypervisor binary not found (set %s or add to $PATH)", chBinaryEnv)
	}

	proc := startCH(t, bin)
	defer proc.stop()

	client := newClient(proc.socket)
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ver, err := client.Version(ctx)
	if err != nil {
		t.Fatalf("version failed: %v", err)
	}
	if ver == "" {
		t.Fatal("expected non-empty version string")
	}
	t.Logf("cloud-hypervisor version: %s", ver)
}

func TestVMMClient_CreateAndDelete(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("Cloud Hypervisor requires Linux/KVM")
	}

	bin := findCHBinary()
	if bin == "" {
		t.Skipf("cloud-hypervisor binary not found (set %s or add to $PATH)", chBinaryEnv)
	}

	proc := startCH(t, bin)
	defer proc.stop()

	client := newClient(proc.socket)
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cfg := &vmm.VmConfig{
		CPUs:   &vmm.CPUConfig{BootVCPUs: 1, MaxVCPUs: 1},
		Memory: &vmm.MemoryConfig{Size: 256},
		Kernel: &vmm.KernelConfig{Path: "/nonexistent/vmlinux"},
	}

	if err := client.Create(ctx, cfg); err != nil {
		t.Fatalf("create failed: %v", err)
	}

	if err := client.Delete(ctx); err != nil {
		t.Fatalf("delete failed: %v", err)
	}
}

func TestVMMClient_FullLifecycle(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("Cloud Hypervisor requires Linux/KVM")
	}

	bin := findCHBinary()
	if bin == "" {
		t.Skipf("cloud-hypervisor binary not found (set %s or add to $PATH)", chBinaryEnv)
	}

	kernel := findKernel()
	if kernel == "" {
		t.Skipf("kernel image not found (set %s to a compiled vmlinux or bzImage)", kernelEnv)
	}

	proc := startCH(t, bin)
	defer proc.stop()

	client := newClient(proc.socket)
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// 1. Health check
	if err := client.Ping(ctx); err != nil {
		t.Fatalf("ping failed: %v", err)
	}

	// 2. Version
	ver, err := client.Version(ctx)
	if err != nil {
		t.Fatalf("version failed: %v", err)
	}
	t.Logf("cloud-hypervisor version: %s", ver)

	// 3. Create VM
	cfg := &vmm.VmConfig{
		CPUs:   &vmm.CPUConfig{BootVCPUs: 1, MaxVCPUs: 1},
		Memory: &vmm.MemoryConfig{Size: 256},
		Kernel: &vmm.KernelConfig{Path: kernel},
		Payload: &vmm.PayloadConfig{
			Kernel:  kernel,
			Cmdline: "console=hvc0 reboot=k panic=1",
		},
	}

	if err := client.Create(ctx, cfg); err != nil {
		t.Fatalf("create failed: %v", err)
	}

	// 4. Boot VM
	if err := client.Boot(ctx); err != nil {
		t.Fatalf("boot failed: %v", err)
	}

	// Allow CH a moment to transition to Running.
	time.Sleep(500 * time.Millisecond)

	// 5. Info
	info, err := client.Info(ctx)
	if err != nil {
		t.Fatalf("info failed: %v", err)
	}
	if info.State != "Running" {
		t.Fatalf("expected state Running, got %q", info.State)
	}
	t.Logf("vm info: state=%s cpus=%d memory=%d", info.State, info.Cpus, info.Memory)

	// 6. Pause
	if err := client.Pause(ctx); err != nil {
		t.Fatalf("pause failed: %v", err)
	}

	time.Sleep(200 * time.Millisecond)

	info, err = client.Info(ctx)
	if err != nil {
		t.Fatalf("info after pause failed: %v", err)
	}
	if info.State != "Paused" {
		t.Fatalf("expected state Paused, got %q", info.State)
	}

	// 7. Resume
	if err := client.Resume(ctx); err != nil {
		t.Fatalf("resume failed: %v", err)
	}

	time.Sleep(200 * time.Millisecond)

	info, err = client.Info(ctx)
	if err != nil {
		t.Fatalf("info after resume failed: %v", err)
	}
	if info.State != "Running" {
		t.Fatalf("expected state Running after resume, got %q", info.State)
	}

	// 8. Shutdown
	if err := client.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown failed: %v", err)
	}

	// Wait for shutdown to complete.
	shutdownDeadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(shutdownDeadline) {
		info, err = client.Info(ctx)
		if err != nil || info.State == "Shutdown" {
			break
		}
		time.Sleep(pollInterval)
	}

	// 9. Delete VM
	if err := client.Delete(ctx); err != nil {
		t.Fatalf("delete failed: %v", err)
	}

	// Verify deletion by expecting Info to fail.
	_, err = client.Info(ctx)
	if err == nil {
		t.Fatal("expected info to fail after delete")
	}
}

func TestVMMClient_Reboot(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("Cloud Hypervisor requires Linux/KVM")
	}

	bin := findCHBinary()
	if bin == "" {
		t.Skipf("cloud-hypervisor binary not found (set %s or add to $PATH)", chBinaryEnv)
	}

	kernel := findKernel()
	if kernel == "" {
		t.Skipf("kernel image not found (set %s to a compiled vmlinux or bzImage)", kernelEnv)
	}

	proc := startCH(t, bin)
	defer proc.stop()

	client := newClient(proc.socket)
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cfg := &vmm.VmConfig{
		CPUs:   &vmm.CPUConfig{BootVCPUs: 1, MaxVCPUs: 1},
		Memory: &vmm.MemoryConfig{Size: 256},
		Payload: &vmm.PayloadConfig{
			Kernel:  kernel,
			Cmdline: "console=hvc0 reboot=k panic=1",
		},
	}

	if err := client.Create(ctx, cfg); err != nil {
		t.Fatalf("create failed: %v", err)
	}

	if err := client.Boot(ctx); err != nil {
		t.Fatalf("boot failed: %v", err)
	}

	time.Sleep(500 * time.Millisecond)

	info, err := client.Info(ctx)
	if err != nil {
		t.Fatalf("info failed: %v", err)
	}
	if info.State != "Running" {
		t.Fatalf("expected state Running, got %q", info.State)
	}

	if err := client.Reboot(ctx); err != nil {
		t.Fatalf("reboot failed: %v", err)
	}

	// Give CH time to reboot.
	time.Sleep(500 * time.Millisecond)

	info, err = client.Info(ctx)
	if err != nil {
		t.Fatalf("info after reboot failed: %v", err)
	}
	if info.State != "Running" {
		t.Fatalf("expected state Running after reboot, got %q", info.State)
	}

	if err := client.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown failed: %v", err)
	}

	if err := client.Delete(ctx); err != nil {
		t.Fatalf("delete failed: %v", err)
	}
}
