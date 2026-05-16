package network

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gokaybaz/go-cloud-hypervisor-service/pkg/logging"
)

// ---------------------------------------------------------------------------
// noopLogger for tests
// ---------------------------------------------------------------------------

type noopLogger struct{}

func (noopLogger) Info(string, ...any)  {}
func (noopLogger) Error(string, ...any) {}
func (noopLogger) Debug(string, ...any) {}
func (noopLogger) Warn(string, ...any)  {}
func (l noopLogger) WithContext(_ context.Context) logging.Logger { return l }
func (l noopLogger) With(...any) logging.Logger                   { return l }

// ---------------------------------------------------------------------------
// Manager construction
// ---------------------------------------------------------------------------

func TestNewManagerRequiresPath(t *testing.T) {
	_, err := NewManager("", noopLogger{})
	if err == nil {
		t.Fatal("expected error for empty audit path")
	}
	if !IsNetworkError(err) {
		t.Fatalf("expected *Error, got %T", err)
	}
}

func TestNewManagerCreatesAuditLog(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")

	mgr, err := NewManager(path, noopLogger{})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	entries, err := mgr.ReadAudit()
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 init entry, got %d", len(entries))
	}
	if entries[0].Operation != "init" {
		t.Fatalf("expected init operation, got %s", entries[0].Operation)
	}
}

// ---------------------------------------------------------------------------
// AddInterface
// ---------------------------------------------------------------------------

func TestAddInterface(t *testing.T) {
	dir := t.TempDir()
	mgr, _ := NewManager(filepath.Join(dir, "audit.log"), noopLogger{})

	iface := &Interface{
		Tap: "tap0",
		IP:  "10.0.0.2",
	}

	added, err := mgr.AddInterface(iface)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if added.ID == "" {
		t.Fatal("expected auto-generated ID")
	}
	if added.Mac == "" {
		t.Fatal("expected auto-generated MAC")
	}
	if added.NumQueues != 2 {
		t.Fatalf("expected default 2 queues, got %d", added.NumQueues)
	}
	if added.QueueSize != 256 {
		t.Fatalf("expected default queue size 256, got %d", added.QueueSize)
	}
	if added.Tap != "tap0" {
		t.Fatalf("tap mismatch")
	}

	// Verify it exists.
	got, err := mgr.Get(added.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ID != added.ID {
		t.Fatal("id mismatch")
	}

	// Verify audit log.
	entries, _ := mgr.ReadAudit()
	var found bool
	for _, e := range entries {
		if e.Operation == "AddInterface" && e.InterfaceID == added.ID {
			found = true
			if e.Caller.File == "" {
				t.Fatal("expected caller file")
			}
			if e.Details["tap"] != "tap0" {
				t.Fatalf("audit detail mismatch")
			}
		}
	}
	if !found {
		t.Fatal("AddInterface audit entry not found")
	}
}

func TestAddInterfaceDuplicateID(t *testing.T) {
	dir := t.TempDir()
	mgr, _ := NewManager(filepath.Join(dir, "audit.log"), noopLogger{})

	iface := &Interface{ID: "eth0", Tap: "tap0"}
	_, err := mgr.AddInterface(iface)
	if err != nil {
		t.Fatalf("first add: %v", err)
	}

	_, err = mgr.AddInterface(iface)
	if err == nil {
		t.Fatal("expected error for duplicate ID")
	}
	if !IsNetworkError(err) {
		t.Fatalf("expected *Error, got %T", err)
	}
}

func TestAddInterfaceInvalidMAC(t *testing.T) {
	dir := t.TempDir()
	mgr, _ := NewManager(filepath.Join(dir, "audit.log"), noopLogger{})

	iface := &Interface{Mac: "bad-mac"}
	_, err := mgr.AddInterface(iface)
	if err == nil {
		t.Fatal("expected error for invalid MAC")
	}
}

func TestAddInterfaceInvalidIP(t *testing.T) {
	dir := t.TempDir()
	mgr, _ := NewManager(filepath.Join(dir, "audit.log"), noopLogger{})

	iface := &Interface{IP: "not-an-ip"}
	_, err := mgr.AddInterface(iface)
	if err == nil {
		t.Fatal("expected error for invalid IP")
	}
}

// ---------------------------------------------------------------------------
// RemoveInterface
// ---------------------------------------------------------------------------

func TestRemoveInterface(t *testing.T) {
	dir := t.TempDir()
	mgr, _ := NewManager(filepath.Join(dir, "audit.log"), noopLogger{})

	added, _ := mgr.AddInterface(&Interface{Tap: "tap0"})
	id := added.ID

	if err := mgr.RemoveInterface(id); err != nil {
		t.Fatalf("remove: %v", err)
	}

	_, err := mgr.Get(id)
	if err == nil {
		t.Fatal("expected error after removal")
	}

	entries, _ := mgr.ReadAudit()
	var found bool
	for _, e := range entries {
		if e.Operation == "RemoveInterface" && e.InterfaceID == id {
			found = true
		}
	}
	if !found {
		t.Fatal("RemoveInterface audit entry not found")
	}
}

func TestRemoveInterfaceNotFound(t *testing.T) {
	dir := t.TempDir()
	mgr, _ := NewManager(filepath.Join(dir, "audit.log"), noopLogger{})

	err := mgr.RemoveInterface("nonexistent")
	if err == nil {
		t.Fatal("expected error")
	}
	if !IsNetworkError(err) {
		t.Fatalf("expected *Error, got %T", err)
	}
}

// ---------------------------------------------------------------------------
// SetQoS
// ---------------------------------------------------------------------------

func TestSetQoS(t *testing.T) {
	dir := t.TempDir()
	mgr, _ := NewManager(filepath.Join(dir, "audit.log"), noopLogger{})

	added, _ := mgr.AddInterface(&Interface{Tap: "tap0"})
	id := added.ID

	qos := QoSConfig{IngressRate: 1024 * 1024, EgressRate: 512 * 1024}
	if err := mgr.SetQoS(id, qos); err != nil {
		t.Fatalf("set qos: %v", err)
	}

	iface, _ := mgr.Get(id)
	if iface.QoS.IngressRate != 1024*1024 {
		t.Fatalf("ingress rate mismatch")
	}
	if iface.QoS.EgressRate != 512*1024 {
		t.Fatalf("egress rate mismatch")
	}

	entries, _ := mgr.ReadAudit()
	var found bool
	for _, e := range entries {
		if e.Operation == "SetQoS" && e.InterfaceID == id {
			found = true
			if e.Details["ingress_rate"] != float64(1024*1024) {
				t.Fatalf("audit ingress rate mismatch: %v", e.Details["ingress_rate"])
			}
		}
	}
	if !found {
		t.Fatal("SetQoS audit entry not found")
	}
}

func TestSetQoSNotFound(t *testing.T) {
	dir := t.TempDir()
	mgr, _ := NewManager(filepath.Join(dir, "audit.log"), noopLogger{})

	err := mgr.SetQoS("nonexistent", QoSConfig{IngressRate: 1})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestSetQoSNegativeRate(t *testing.T) {
	dir := t.TempDir()
	mgr, _ := NewManager(filepath.Join(dir, "audit.log"), noopLogger{})

	added, _ := mgr.AddInterface(&Interface{Tap: "tap0"})
	err := mgr.SetQoS(added.ID, QoSConfig{IngressRate: -1})
	if err == nil {
		t.Fatal("expected error for negative rate")
	}
}

// ---------------------------------------------------------------------------
// List / Count
// ---------------------------------------------------------------------------

func TestListAndCount(t *testing.T) {
	dir := t.TempDir()
	mgr, _ := NewManager(filepath.Join(dir, "audit.log"), noopLogger{})

	if mgr.Count() != 0 {
		t.Fatalf("expected 0 interfaces, got %d", mgr.Count())
	}

	mgr.AddInterface(&Interface{Tap: "tap0"})
	mgr.AddInterface(&Interface{Tap: "tap1"})

	if mgr.Count() != 2 {
		t.Fatalf("expected 2 interfaces, got %d", mgr.Count())
	}

	list := mgr.List()
	if len(list) != 2 {
		t.Fatalf("expected 2 in list, got %d", len(list))
	}
}

// ---------------------------------------------------------------------------
// Concurrency
// ---------------------------------------------------------------------------

func TestConcurrency(t *testing.T) {
	dir := t.TempDir()
	mgr, _ := NewManager(filepath.Join(dir, "audit.log"), noopLogger{})

	// Add 100 interfaces concurrently.
	done := make(chan struct{})
	for i := 0; i < 100; i++ {
		go func(idx int) {
			defer func() { done <- struct{}{} }()
			iface := &Interface{Tap: "tap"}
			_, err := mgr.AddInterface(iface)
			if err != nil {
				t.Errorf("add %d: %v", idx, err)
			}
		}(i)
	}
	for i := 0; i < 100; i++ {
		<-done
	}

	if mgr.Count() != 100 {
		t.Fatalf("expected 100 interfaces, got %d", mgr.Count())
	}
}

// ---------------------------------------------------------------------------
// MAC generation
// ---------------------------------------------------------------------------

func TestGenerateMAC(t *testing.T) {
	mac := generateMAC()
	if mac == "" {
		t.Fatal("expected non-empty MAC")
	}
	// Must be a valid MAC.
	parts := strings.Split(mac, ":")
	if len(parts) != 6 {
		t.Fatalf("expected 6 octets, got %d", len(parts))
	}
	// First octet must be locally-administered and unicast.
	first := parts[0]
	if first == "" {
		t.Fatal("empty first octet")
	}
	// Check bit 1 is set (locally administered) and bit 0 is clear (unicast).
	b := []byte(first)
	// The string is hex, so first byte is actually two hex chars.
	// For simplicity just verify it starts with 02, 06, 0a, 0e, etc.
	// Local admin unicast means second hex digit is 2,6,a,e.
	validSecond := "26ae"
	if !strings.Contains(validSecond, strings.ToLower(string(b[1]))) {
		t.Fatalf("expected locally-administered unicast MAC, got %s", mac)
	}
}

func TestGenerateID(t *testing.T) {
	id := generateID()
	if !strings.HasPrefix(id, "eth") {
		t.Fatalf("expected eth prefix, got %s", id)
	}
	if len(id) != 7 { // "eth" + 4 chars
		t.Fatalf("expected 7 chars, got %d", len(id))
	}
}

// ---------------------------------------------------------------------------
// Audit log helpers
// ---------------------------------------------------------------------------

func TestReadAuditEmptyFile(t *testing.T) {
	dir := t.TempDir()
	mgr, _ := NewManager(filepath.Join(dir, "audit.log"), noopLogger{})

	entries, err := mgr.ReadAudit()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(entries) < 1 {
		t.Fatal("expected at least init entry")
	}
}

func TestReadAuditMalformed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")
	os.WriteFile(path, []byte("not json\n"), 0644)

	mgr, _ := NewManager(path, noopLogger{})
	_, err := mgr.ReadAudit()
	if err == nil {
		t.Fatal("expected error for malformed audit log")
	}
}

func TestAuditLogAppend(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")

	// Create first manager, add one interface.
	mgr1, _ := NewManager(path, noopLogger{})
	iface1, _ := mgr1.AddInterface(&Interface{Tap: "tap0"})

	// Create second manager pointing at same file, add another interface.
	mgr2, _ := NewManager(path, noopLogger{})
	iface2, _ := mgr2.AddInterface(&Interface{Tap: "tap1"})

	// Read via first manager — should see both entries plus two init entries.
	entries, _ := mgr1.ReadAudit()
	var addCount int
	for _, e := range entries {
		if e.Operation == "AddInterface" {
			addCount++
		}
	}
	if addCount != 2 {
		t.Fatalf("expected 2 AddInterface entries, got %d", addCount)
	}

	// Verify IDs match.
	var ids []string
	for _, e := range entries {
		if e.Operation == "AddInterface" {
			ids = append(ids, e.InterfaceID)
		}
	}
	if !contains(ids, iface1.ID) || !contains(ids, iface2.ID) {
		t.Fatal("audit log missing interface IDs")
	}
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Error type
// ---------------------------------------------------------------------------

func TestErrorString(t *testing.T) {
	e := &Error{Op: "AddInterface", Message: "boom"}
	if e.Error() != "network.AddInterface: boom" {
		t.Fatalf("unexpected error string: %s", e.Error())
	}
}

func TestIsNetworkError(t *testing.T) {
	if !IsNetworkError(&Error{Op: "x", Message: "y"}) {
		t.Fatal("IsNetworkError should be true")
	}
	if IsNetworkError(os.ErrNotExist) {
		t.Fatal("IsNetworkError should be false for generic error")
	}
}

// ---------------------------------------------------------------------------
// Interface Clone
// ---------------------------------------------------------------------------

func TestInterfaceClone(t *testing.T) {
	orig := &Interface{
		ID:        "eth0",
		Mac:       "52:54:00:12:34:56",
		Tap:       "tap0",
		NumQueues: 4,
		QoS:       QoSConfig{IngressRate: 100},
	}
	clone := orig.Clone()
	if clone.ID != orig.ID {
		t.Fatal("clone id mismatch")
	}
	if clone.QoS.IngressRate != orig.QoS.IngressRate {
		t.Fatal("clone qos mismatch")
	}
	// Mutate clone, ensure original is untouched.
	clone.ID = "eth1"
	if orig.ID == "eth1" {
		t.Fatal("clone mutation leaked to original")
	}
}