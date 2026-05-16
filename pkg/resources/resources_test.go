package resources

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/org/ch-api/pkg/logging"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

type noopLogger struct{}

func (noopLogger) Info(string, ...any)  {}
func (noopLogger) Error(string, ...any) {}
func (noopLogger) Debug(string, ...any) {}
func (noopLogger) Warn(string, ...any)  {}
func (n noopLogger) WithContext(context.Context) logging.Logger { return n }
func (n noopLogger) With(...any) logging.Logger                 { return n }

// ---------------------------------------------------------------------------
// Manager construction
// ---------------------------------------------------------------------------

func TestNewManagerRequiresPaths(t *testing.T) {
	_, err := NewManager("", "/tmp/log", noopLogger{})
	if err == nil {
		t.Fatal("expected error for empty state path")
	}
	if !IsResourcesError(err) {
		t.Fatalf("expected resources.Error, got %T", err)
	}

	_, err = NewManager("/tmp/state", "", noopLogger{})
	if err == nil {
		t.Fatal("expected error for empty log path")
	}
}

func TestNewManagerCreatesPaths(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "state", "resources.json")
	logPath := filepath.Join(dir, "logs", "resources-ops.log")

	_, err := NewManager(statePath, logPath, noopLogger{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := os.Stat(statePath); err != nil {
		t.Fatalf("state file not created: %v", err)
	}
	if _, err := os.Stat(logPath); err != nil {
		t.Fatalf("log file not created: %v", err)
	}
}

func TestNewManagerDefaults(t *testing.T) {
	dir := t.TempDir()
	mgr, _ := NewManager(
		filepath.Join(dir, "resources.json"),
		filepath.Join(dir, "resources-ops.log"),
		noopLogger{},
	)

	cfg := mgr.Config()
	if cfg.CPUs != 1 {
		t.Fatalf("expected default 1 CPU, got %d", cfg.CPUs)
	}
	if cfg.MemoryMB != 256 {
		t.Fatalf("expected default 256 MB, got %d", cfg.MemoryMB)
	}

	limits := mgr.Limits()
	if limits.MinCPUs != 1 {
		t.Fatalf("expected default min CPUs 1, got %d", limits.MinCPUs)
	}
	if limits.MaxCPUs != 1024 {
		t.Fatalf("expected default max CPUs 1024, got %d", limits.MaxCPUs)
	}
}

func TestNewManagerLoadsState(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "resources.json")

	// Pre-seed a state file.
	data := []byte(`{"cpus":4,"memory_mb":1024}`)
	if err := os.WriteFile(statePath, data, 0644); err != nil {
		t.Fatalf("write state: %v", err)
	}

	mgr, _ := NewManager(
		statePath,
		filepath.Join(dir, "resources-ops.log"),
		noopLogger{},
	)

	cfg := mgr.Config()
	if cfg.CPUs != 4 {
		t.Fatalf("expected 4 CPUs from state, got %d", cfg.CPUs)
	}
	if cfg.MemoryMB != 1024 {
		t.Fatalf("expected 1024 MB from state, got %d", cfg.MemoryMB)
	}
}

func TestNewManagerMalformedState(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "resources.json")
	_ = os.WriteFile(statePath, []byte("not json"), 0644)

	_, err := NewManager(
		statePath,
		filepath.Join(dir, "resources-ops.log"),
		noopLogger{},
	)
	if err == nil {
		t.Fatal("expected error for malformed state")
	}
	if !IsResourcesError(err) {
		t.Fatalf("expected resources.Error, got %T", err)
	}
}

// ---------------------------------------------------------------------------
// ResizeCPU
// ---------------------------------------------------------------------------

func TestResizeCPU(t *testing.T) {
	dir := t.TempDir()
	mgr, _ := NewManager(
		filepath.Join(dir, "resources.json"),
		filepath.Join(dir, "resources-ops.log"),
		noopLogger{},
	)

	if err := mgr.ResizeCPU(4, "api"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cfg := mgr.Config()
	if cfg.CPUs != 4 {
		t.Fatalf("expected 4 CPUs, got %d", cfg.CPUs)
	}
}

func TestResizeCPUBelowMin(t *testing.T) {
	dir := t.TempDir()
	mgr, _ := NewManager(
		filepath.Join(dir, "resources.json"),
		filepath.Join(dir, "resources-ops.log"),
		noopLogger{},
	)

	err := mgr.ResizeCPU(0, "api")
	if err == nil {
		t.Fatal("expected error for CPU below minimum")
	}
	if !IsResourcesError(err) {
		t.Fatalf("expected resources.Error, got %T", err)
	}
	if !strings.Contains(err.Error(), "below minimum") {
		t.Fatalf("expected 'below minimum' in error, got %v", err)
	}
}

func TestResizeCPUAboveMax(t *testing.T) {
	dir := t.TempDir()
	mgr, _ := NewManager(
		filepath.Join(dir, "resources.json"),
		filepath.Join(dir, "resources-ops.log"),
		noopLogger{},
	)

	err := mgr.ResizeCPU(2048, "api")
	if err == nil {
		t.Fatal("expected error for CPU above maximum")
	}
	if !strings.Contains(err.Error(), "above maximum") {
		t.Fatalf("expected 'above maximum' in error, got %v", err)
	}
}

func TestResizeCPUWithCustomLimits(t *testing.T) {
	dir := t.TempDir()
	mgr, _ := NewManager(
		filepath.Join(dir, "resources.json"),
		filepath.Join(dir, "resources-ops.log"),
		noopLogger{},
	)

	mgr.SetLimits(Limits{MinCPUs: 2, MaxCPUs: 8, MinMemoryMB: 64, MaxMemoryMB: 1024, MemoryAlignMB: 4})

	if err := mgr.ResizeCPU(1, "api"); err == nil {
		t.Fatal("expected error for CPU below custom minimum")
	}
	if err := mgr.ResizeCPU(16, "api"); err == nil {
		t.Fatal("expected error for CPU above custom maximum")
	}
	if err := mgr.ResizeCPU(4, "api"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResizeCPUUnknownSource(t *testing.T) {
	dir := t.TempDir()
	mgr, _ := NewManager(
		filepath.Join(dir, "resources.json"),
		filepath.Join(dir, "resources-ops.log"),
		noopLogger{},
	)

	// Empty source should default to "unknown".
	if err := mgr.ResizeCPU(2, ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	entries, _ := mgr.ReadLog()
	var found bool
	for _, e := range entries {
		if e.Operation == "ResizeCPU" {
			found = true
			if e.Source != "unknown" {
				t.Fatalf("expected source 'unknown', got %q", e.Source)
			}
		}
	}
	if !found {
		t.Fatal("expected ResizeCPU entry in log")
	}
}

// ---------------------------------------------------------------------------
// ResizeMemory
// ---------------------------------------------------------------------------

func TestResizeMemory(t *testing.T) {
	dir := t.TempDir()
	mgr, _ := NewManager(
		filepath.Join(dir, "resources.json"),
		filepath.Join(dir, "resources-ops.log"),
		noopLogger{},
	)

	if err := mgr.ResizeMemory(512, "cli"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cfg := mgr.Config()
	if cfg.MemoryMB != 512 {
		t.Fatalf("expected 512 MB, got %d", cfg.MemoryMB)
	}
}

func TestResizeMemoryBelowMin(t *testing.T) {
	dir := t.TempDir()
	mgr, _ := NewManager(
		filepath.Join(dir, "resources.json"),
		filepath.Join(dir, "resources-ops.log"),
		noopLogger{},
	)

	err := mgr.ResizeMemory(32, "api")
	if err == nil {
		t.Fatal("expected error for memory below minimum")
	}
	if !strings.Contains(err.Error(), "below minimum") {
		t.Fatalf("expected 'below minimum' in error, got %v", err)
	}
}

func TestResizeMemoryAboveMax(t *testing.T) {
	dir := t.TempDir()
	mgr, _ := NewManager(
		filepath.Join(dir, "resources.json"),
		filepath.Join(dir, "resources-ops.log"),
		noopLogger{},
	)

	err := mgr.ResizeMemory(2000000, "api")
	if err == nil {
		t.Fatal("expected error for memory above maximum")
	}
	if !strings.Contains(err.Error(), "above maximum") {
		t.Fatalf("expected 'above maximum' in error, got %v", err)
	}
}

func TestResizeMemoryUnaligned(t *testing.T) {
	dir := t.TempDir()
	mgr, _ := NewManager(
		filepath.Join(dir, "resources.json"),
		filepath.Join(dir, "resources-ops.log"),
		noopLogger{},
	)

	// Default alignment is 4 MB.
	err := mgr.ResizeMemory(65, "api")
	if err == nil {
		t.Fatal("expected error for unaligned memory")
	}
	if !strings.Contains(err.Error(), "not aligned") {
		t.Fatalf("expected 'not aligned' in error, got %v", err)
	}
}

func TestResizeMemoryCustomAlignment(t *testing.T) {
	dir := t.TempDir()
	mgr, _ := NewManager(
		filepath.Join(dir, "resources.json"),
		filepath.Join(dir, "resources-ops.log"),
		noopLogger{},
	)

	mgr.SetLimits(Limits{MinCPUs: 1, MaxCPUs: 1024, MinMemoryMB: 128, MaxMemoryMB: 1024, MemoryAlignMB: 128})

	if err := mgr.ResizeMemory(128, "api"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := mgr.ResizeMemory(256, "api"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := mgr.ResizeMemory(200, "api"); err == nil {
		t.Fatal("expected error for memory not aligned to 128 MB")
	}
}

// ---------------------------------------------------------------------------
// Persistence
// ---------------------------------------------------------------------------

func TestStatePersistence(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "resources.json")

	// First manager: resize and let it persist.
	mgr1, _ := NewManager(
		statePath,
		filepath.Join(dir, "resources-ops.log"),
		noopLogger{},
	)
	if err := mgr1.ResizeCPU(8, "api"); err != nil {
		t.Fatalf("resize: %v", err)
	}
	if err := mgr1.ResizeMemory(2048, "api"); err != nil {
		t.Fatalf("resize: %v", err)
	}

	// Second manager: load the same state file.
	mgr2, _ := NewManager(
		statePath,
		filepath.Join(dir, "resources-ops-2.log"),
		noopLogger{},
	)

	cfg := mgr2.Config()
	if cfg.CPUs != 8 {
		t.Fatalf("expected 8 CPUs from persisted state, got %d", cfg.CPUs)
	}
	if cfg.MemoryMB != 2048 {
		t.Fatalf("expected 2048 MB from persisted state, got %d", cfg.MemoryMB)
	}
}

// ---------------------------------------------------------------------------
// Operation log
// ---------------------------------------------------------------------------

func TestReadLog(t *testing.T) {
	dir := t.TempDir()
	mgr, _ := NewManager(
		filepath.Join(dir, "resources.json"),
		filepath.Join(dir, "resources-ops.log"),
		noopLogger{},
	)

	entries, err := mgr.ReadLog()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(entries) < 1 {
		t.Fatal("expected at least init entry")
	}
	if entries[0].Operation != "init" {
		t.Fatalf("expected init operation, got %s", entries[0].Operation)
	}
}

func TestLogContainsBeforeAfterAndSource(t *testing.T) {
	dir := t.TempDir()
	mgr, _ := NewManager(
		filepath.Join(dir, "resources.json"),
		filepath.Join(dir, "resources-ops.log"),
		noopLogger{},
	)

	if err := mgr.ResizeCPU(4, "auto-scaler"); err != nil {
		t.Fatalf("resize: %v", err)
	}

	entries, _ := mgr.ReadLog()
	var found bool
	for _, e := range entries {
		if e.Operation == "ResizeCPU" {
			found = true
			if e.Before != 1 {
				t.Fatalf("expected before=1, got %d", e.Before)
			}
			if e.After != 4 {
				t.Fatalf("expected after=4, got %d", e.After)
			}
			if e.Source != "auto-scaler" {
				t.Fatalf("expected source 'auto-scaler', got %q", e.Source)
			}
			if e.Duration <= 0 {
				t.Fatalf("expected positive duration, got %v", e.Duration)
			}
			if e.Outcome != "success" {
				t.Fatalf("expected success outcome, got %s", e.Outcome)
			}
			if e.Caller.File == "" {
				t.Fatal("expected caller file")
			}
		}
	}
	if !found {
		t.Fatal("expected ResizeCPU entry in log")
	}
}

func TestLogErrorEntry(t *testing.T) {
	dir := t.TempDir()
	mgr, _ := NewManager(
		filepath.Join(dir, "resources.json"),
		filepath.Join(dir, "resources-ops.log"),
		noopLogger{},
	)

	mgr.ResizeCPU(0, "api")

	entries, _ := mgr.ReadLog()
	var found bool
	for _, e := range entries {
		if e.Operation == "ResizeCPU" && e.Outcome == "error" {
			found = true
			if e.Message == "" {
				t.Fatal("expected error message")
			}
			if e.Before != 1 {
				t.Fatalf("expected before=1 on error, got %d", e.Before)
			}
			if e.After != 0 {
				t.Fatalf("expected after=0 on error, got %d", e.After)
			}
		}
	}
	if !found {
		t.Fatal("expected error entry in log")
	}
}

func TestLogMemoryResize(t *testing.T) {
	dir := t.TempDir()
	mgr, _ := NewManager(
		filepath.Join(dir, "resources.json"),
		filepath.Join(dir, "resources-ops.log"),
		noopLogger{},
	)

	if err := mgr.ResizeMemory(512, "cli"); err != nil {
		t.Fatalf("resize: %v", err)
	}

	entries, _ := mgr.ReadLog()
	var found bool
	for _, e := range entries {
		if e.Operation == "ResizeMemory" {
			found = true
			if e.Before != 256 {
				t.Fatalf("expected before=256, got %d", e.Before)
			}
			if e.After != 512 {
				t.Fatalf("expected after=512, got %d", e.After)
			}
			if e.Source != "cli" {
				t.Fatalf("expected source 'cli', got %q", e.Source)
			}
		}
	}
	if !found {
		t.Fatal("expected ResizeMemory entry in log")
	}
}

func TestReadLogMalformed(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "resources-ops.log")
	_ = os.WriteFile(logPath, []byte("not json\n"), 0644)

	mgr, _ := NewManager(
		filepath.Join(dir, "resources.json"),
		logPath,
		noopLogger{},
	)
	_, err := mgr.ReadLog()
	if err == nil {
		t.Fatal("expected error for malformed log")
	}
}

// ---------------------------------------------------------------------------
// Config clone
// ---------------------------------------------------------------------------

func TestConfigClone(t *testing.T) {
	c := &Config{CPUs: 4, MemoryMB: 1024}
	cp := c.Clone()
	if cp.CPUs != 4 || cp.MemoryMB != 1024 {
		t.Fatal("clone mismatch")
	}
	cp.CPUs = 8
	if c.CPUs != 4 {
		t.Fatal("clone mutated original")
	}
}

// ---------------------------------------------------------------------------
// Limits validation
// ---------------------------------------------------------------------------

func TestLimitsValidateCPU(t *testing.T) {
	l := Limits{MinCPUs: 1, MaxCPUs: 8}
	if err := l.ValidateCPU(4); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := l.ValidateCPU(0); err == nil {
		t.Fatal("expected error for CPU below min")
	}
	if err := l.ValidateCPU(16); err == nil {
		t.Fatal("expected error for CPU above max")
	}
}

func TestLimitsValidateMemory(t *testing.T) {
	l := Limits{MinMemoryMB: 64, MaxMemoryMB: 1024, MemoryAlignMB: 4}
	if err := l.ValidateMemory(128); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := l.ValidateMemory(32); err == nil {
		t.Fatal("expected error for memory below min")
	}
	if err := l.ValidateMemory(2048); err == nil {
		t.Fatal("expected error for memory above max")
	}
	if err := l.ValidateMemory(65); err == nil {
		t.Fatal("expected error for unaligned memory")
	}
}

// ---------------------------------------------------------------------------
// Concurrency
// ---------------------------------------------------------------------------

func TestConcurrency(t *testing.T) {
	dir := t.TempDir()
	mgr, _ := NewManager(
		filepath.Join(dir, "resources.json"),
		filepath.Join(dir, "resources-ops.log"),
		noopLogger{},
	)

	// Narrow limits to create contention.
	mgr.SetLimits(Limits{MinCPUs: 1, MaxCPUs: 16, MinMemoryMB: 64, MaxMemoryMB: 1024, MemoryAlignMB: 4})

	done := make(chan struct{})
	for i := 0; i < 50; i++ {
		go func(idx int) {
			defer func() { done <- struct{}{} }()
			_ = mgr.ResizeCPU((idx%16)+1, fmt.Sprintf("worker-%d", idx))
			_ = mgr.ResizeMemory(64+((idx%16)*4), fmt.Sprintf("worker-%d", idx))
		}(i)
	}
	for i := 0; i < 50; i++ {
		<-done
	}

	// Config should be valid after all concurrent updates.
	cfg := mgr.Config()
	if err := mgr.Limits().ValidateCPU(cfg.CPUs); err != nil {
		t.Fatalf("final CPU invalid: %v", err)
	}
	if err := mgr.Limits().ValidateMemory(cfg.MemoryMB); err != nil {
		t.Fatalf("final memory invalid: %v", err)
	}

	// State file should reflect final config.
	data, _ := os.ReadFile(filepath.Join(dir, "resources.json"))
	if !strings.Contains(string(data), fmt.Sprintf("\"cpus\":%d", cfg.CPUs)) {
		t.Fatalf("state file missing final CPU value")
	}
}

// ---------------------------------------------------------------------------
// Error types
// ---------------------------------------------------------------------------

func TestErrorString(t *testing.T) {
	err := &Error{Op: "Test", Message: "something failed"}
	want := "resources.Test: something failed"
	if got := err.Error(); got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestIsResourcesError(t *testing.T) {
	if !IsResourcesError(&Error{Op: "test"}) {
		t.Fatal("expected true for resources.Error")
	}
	if IsResourcesError(fmt.Errorf("plain error")) {
		t.Fatal("expected false for plain error")
	}
}

// ---------------------------------------------------------------------------
// DefaultLimits
// ---------------------------------------------------------------------------

func TestDefaultLimits(t *testing.T) {
	l := DefaultLimits()
	if l.MinCPUs != 1 {
		t.Fatalf("expected MinCPUs=1, got %d", l.MinCPUs)
	}
	if l.MaxCPUs != 1024 {
		t.Fatalf("expected MaxCPUs=1024, got %d", l.MaxCPUs)
	}
	if l.MinMemoryMB != 64 {
		t.Fatalf("expected MinMemoryMB=64, got %d", l.MinMemoryMB)
	}
	if l.MaxMemoryMB != 1048576 {
		t.Fatalf("expected MaxMemoryMB=1048576, got %d", l.MaxMemoryMB)
	}
	if l.MemoryAlignMB != 4 {
		t.Fatalf("expected MemoryAlignMB=4, got %d", l.MemoryAlignMB)
	}
}
