package storage

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gokaybaz/go-cloud-hypervisor-service/pkg/logging"
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

// createTempDisk creates a temporary raw disk file for testing.
func createTempDisk(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, fmt.Sprintf("disk-%d.raw", time.Now().UnixNano()))
	if err := os.WriteFile(path, []byte("disk content"), 0644); err != nil {
		t.Fatalf("create temp disk: %v", err)
	}
	return path
}

// ---------------------------------------------------------------------------
// Manager construction
// ---------------------------------------------------------------------------

func TestNewManagerRequiresPath(t *testing.T) {
	_, err := NewManager("", noopLogger{})
	if err == nil {
		t.Fatal("expected error for empty log path")
	}
	if !IsStorageError(err) {
		t.Fatalf("expected storage.Error, got %T", err)
	}
}

func TestNewManagerCreatesLog(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "storage-ops.log")
	_, err := NewManager(logPath, noopLogger{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(logPath); err != nil {
		t.Fatalf("log file not created: %v", err)
	}
}

// ---------------------------------------------------------------------------
// AddDisk (hot-plug)
// ---------------------------------------------------------------------------

func TestAddDisk(t *testing.T) {
	dir := t.TempDir()
	mgr, _ := NewManager(filepath.Join(dir, "storage-ops.log"), noopLogger{})

	diskPath := createTempDisk(t, dir)
	disk := &Disk{Path: diskPath}
	result, err := mgr.AddDisk(disk)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ID == "" {
		t.Fatal("expected auto-generated ID")
	}
	if !strings.HasPrefix(result.ID, "disk") {
		t.Fatalf("expected disk prefix, got %s", result.ID)
	}
	if result.Path != diskPath {
		t.Fatalf("expected path %s, got %s", diskPath, result.Path)
	}
	if result.Format != "raw" {
		t.Fatalf("expected raw format, got %s", result.Format)
	}
	if result.NumQueues != 1 {
		t.Fatalf("expected default 1 queue, got %d", result.NumQueues)
	}
	if result.QueueSize != 128 {
		t.Fatalf("expected default queue size 128, got %d", result.QueueSize)
	}

	if mgr.CountDisks() != 1 {
		t.Fatalf("expected 1 disk, got %d", mgr.CountDisks())
	}
}

func TestAddDiskWithExplicitID(t *testing.T) {
	dir := t.TempDir()
	mgr, _ := NewManager(filepath.Join(dir, "storage-ops.log"), noopLogger{})

	diskPath := createTempDisk(t, dir)
	disk := &Disk{ID: "disk-abc", Path: diskPath}
	result, err := mgr.AddDisk(disk)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ID != "disk-abc" {
		t.Fatalf("expected disk-abc, got %s", result.ID)
	}
}

func TestAddDiskDuplicateID(t *testing.T) {
	dir := t.TempDir()
	mgr, _ := NewManager(filepath.Join(dir, "storage-ops.log"), noopLogger{})

	diskPath := createTempDisk(t, dir)
	mgr.AddDisk(&Disk{ID: "disk-dup", Path: diskPath})

	_, err := mgr.AddDisk(&Disk{ID: "disk-dup", Path: diskPath})
	if err == nil {
		t.Fatal("expected error for duplicate ID")
	}
	if !IsStorageError(err) {
		t.Fatalf("expected storage.Error, got %T", err)
	}
}

func TestAddDiskNil(t *testing.T) {
	dir := t.TempDir()
	mgr, _ := NewManager(filepath.Join(dir, "storage-ops.log"), noopLogger{})

	_, err := mgr.AddDisk(nil)
	if err == nil {
		t.Fatal("expected error for nil disk")
	}
}

func TestAddDiskInvalidPath(t *testing.T) {
	dir := t.TempDir()
	mgr, _ := NewManager(filepath.Join(dir, "storage-ops.log"), noopLogger{})

	_, err := mgr.AddDisk(&Disk{Path: filepath.Join(dir, "nonexistent.raw")})
	if err == nil {
		t.Fatal("expected error for invalid path")
	}
}

// ---------------------------------------------------------------------------
// RemoveDisk (hot-unplug)
// ---------------------------------------------------------------------------

func TestRemoveDisk(t *testing.T) {
	dir := t.TempDir()
	mgr, _ := NewManager(filepath.Join(dir, "storage-ops.log"), noopLogger{})

	diskPath := createTempDisk(t, dir)
	mgr.AddDisk(&Disk{ID: "disk-rm", Path: diskPath})

	if err := mgr.RemoveDisk("disk-rm"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mgr.CountDisks() != 0 {
		t.Fatalf("expected 0 disks, got %d", mgr.CountDisks())
	}
}

func TestRemoveDiskNotFound(t *testing.T) {
	dir := t.TempDir()
	mgr, _ := NewManager(filepath.Join(dir, "storage-ops.log"), noopLogger{})

	err := mgr.RemoveDisk("disk-missing")
	if err == nil {
		t.Fatal("expected error for missing disk")
	}
	if !IsStorageError(err) {
		t.Fatalf("expected storage.Error, got %T", err)
	}
}

func TestRemoveDiskEmptyID(t *testing.T) {
	dir := t.TempDir()
	mgr, _ := NewManager(filepath.Join(dir, "storage-ops.log"), noopLogger{})

	err := mgr.RemoveDisk("")
	if err == nil {
		t.Fatal("expected error for empty ID")
	}
}

// ---------------------------------------------------------------------------
// CreateSnapshot
// ---------------------------------------------------------------------------

func TestCreateSnapshot(t *testing.T) {
	dir := t.TempDir()
	mgr, _ := NewManager(filepath.Join(dir, "storage-ops.log"), noopLogger{})

	diskPath := createTempDisk(t, dir)
	mgr.AddDisk(&Disk{ID: "disk-snap", Path: diskPath})

	snapPath := filepath.Join(dir, "snapshot.raw")
	snap, err := mgr.CreateSnapshot("disk-snap", snapPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if snap.ID == "" {
		t.Fatal("expected auto-generated snapshot ID")
	}
	if snap.DiskID != "disk-snap" {
		t.Fatalf("expected disk-snap, got %s", snap.DiskID)
	}
	if snap.Path != snapPath {
		t.Fatalf("expected path %s, got %s", snapPath, snap.Path)
	}
	if snap.Size == 0 {
		t.Fatal("expected non-zero size")
	}
	if snap.CreatedAt.IsZero() {
		t.Fatal("expected non-zero created_at")
	}

	// Verify file was actually copied.
	if _, err := os.Stat(snapPath); err != nil {
		t.Fatalf("snapshot file not created: %v", err)
	}

	if mgr.CountSnapshots() != 1 {
		t.Fatalf("expected 1 snapshot, got %d", mgr.CountSnapshots())
	}
}

func TestCreateSnapshotDiskNotFound(t *testing.T) {
	dir := t.TempDir()
	mgr, _ := NewManager(filepath.Join(dir, "storage-ops.log"), noopLogger{})

	_, err := mgr.CreateSnapshot("disk-missing", filepath.Join(dir, "snap.raw"))
	if err == nil {
		t.Fatal("expected error for missing disk")
	}
	if !IsStorageError(err) {
		t.Fatalf("expected storage.Error, got %T", err)
	}
}

func TestCreateSnapshotEmptyPath(t *testing.T) {
	dir := t.TempDir()
	mgr, _ := NewManager(filepath.Join(dir, "storage-ops.log"), noopLogger{})

	diskPath := createTempDisk(t, dir)
	mgr.AddDisk(&Disk{ID: "disk-snap2", Path: diskPath})

	_, err := mgr.CreateSnapshot("disk-snap2", "")
	if err == nil {
		t.Fatal("expected error for empty snapshot path")
	}
}

// ---------------------------------------------------------------------------
// RestoreSnapshot
// ---------------------------------------------------------------------------

func TestRestoreSnapshot(t *testing.T) {
	dir := t.TempDir()
	mgr, _ := NewManager(filepath.Join(dir, "storage-ops.log"), noopLogger{})

	diskPath := createTempDisk(t, dir)
	mgr.AddDisk(&Disk{ID: "disk-restore", Path: diskPath})

	// Create a snapshot.
	snapPath := filepath.Join(dir, "snapshot.raw")
	if _, err := mgr.CreateSnapshot("disk-restore", snapPath); err != nil {
		t.Fatalf("create snapshot: %v", err)
	}

	// Modify the original disk.
	if err := os.WriteFile(diskPath, []byte("modified content"), 0644); err != nil {
		t.Fatalf("modify disk: %v", err)
	}

	// Restore from snapshot.
	if err := mgr.RestoreSnapshot("disk-restore", snapPath); err != nil {
		t.Fatalf("restore snapshot: %v", err)
	}

	// Verify content is restored.
	content, err := os.ReadFile(diskPath)
	if err != nil {
		t.Fatalf("read restored disk: %v", err)
	}
	if string(content) != "disk content" {
		t.Fatalf("expected restored content 'disk content', got %q", string(content))
	}
}

func TestRestoreSnapshotDiskNotFound(t *testing.T) {
	dir := t.TempDir()
	mgr, _ := NewManager(filepath.Join(dir, "storage-ops.log"), noopLogger{})

	err := mgr.RestoreSnapshot("disk-missing", filepath.Join(dir, "snap.raw"))
	if err == nil {
		t.Fatal("expected error for missing disk")
	}
}

func TestRestoreSnapshotSnapshotNotFound(t *testing.T) {
	dir := t.TempDir()
	mgr, _ := NewManager(filepath.Join(dir, "storage-ops.log"), noopLogger{})

	diskPath := createTempDisk(t, dir)
	mgr.AddDisk(&Disk{ID: "disk-rs", Path: diskPath})

	err := mgr.RestoreSnapshot("disk-rs", filepath.Join(dir, "nonexistent.raw"))
	if err == nil {
		t.Fatal("expected error for missing snapshot")
	}
}

// ---------------------------------------------------------------------------
// Get / List / Count
// ---------------------------------------------------------------------------

func TestGetDisk(t *testing.T) {
	dir := t.TempDir()
	mgr, _ := NewManager(filepath.Join(dir, "storage-ops.log"), noopLogger{})

	diskPath := createTempDisk(t, dir)
	mgr.AddDisk(&Disk{ID: "disk-get", Path: diskPath})

	disk, err := mgr.GetDisk("disk-get")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if disk.ID != "disk-get" {
		t.Fatalf("expected disk-get, got %s", disk.ID)
	}

	_, err = mgr.GetDisk("disk-missing")
	if err == nil {
		t.Fatal("expected error for missing disk")
	}
}

func TestListAndCountDisks(t *testing.T) {
	dir := t.TempDir()
	mgr, _ := NewManager(filepath.Join(dir, "storage-ops.log"), noopLogger{})

	mgr.AddDisk(&Disk{ID: "disk-a", Path: createTempDisk(t, dir)})
	mgr.AddDisk(&Disk{ID: "disk-b", Path: createTempDisk(t, dir)})

	if mgr.CountDisks() != 2 {
		t.Fatalf("expected 2 disks, got %d", mgr.CountDisks())
	}

	list := mgr.ListDisks()
	if len(list) != 2 {
		t.Fatalf("expected 2 in list, got %d", len(list))
	}
}

func TestGetSnapshot(t *testing.T) {
	dir := t.TempDir()
	mgr, _ := NewManager(filepath.Join(dir, "storage-ops.log"), noopLogger{})

	diskPath := createTempDisk(t, dir)
	mgr.AddDisk(&Disk{ID: "disk-gs", Path: diskPath})

	snap, _ := mgr.CreateSnapshot("disk-gs", filepath.Join(dir, "snap.raw"))

	result, err := mgr.GetSnapshot(snap.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ID != snap.ID {
		t.Fatalf("expected %s, got %s", snap.ID, result.ID)
	}

	_, err = mgr.GetSnapshot("snap-missing")
	if err == nil {
		t.Fatal("expected error for missing snapshot")
	}
}

func TestListAndCountSnapshots(t *testing.T) {
	dir := t.TempDir()
	mgr, _ := NewManager(filepath.Join(dir, "storage-ops.log"), noopLogger{})

	diskPath := createTempDisk(t, dir)
	mgr.AddDisk(&Disk{ID: "disk-ls", Path: diskPath})

	mgr.CreateSnapshot("disk-ls", filepath.Join(dir, "snap1.raw"))
	mgr.CreateSnapshot("disk-ls", filepath.Join(dir, "snap2.raw"))

	if mgr.CountSnapshots() != 2 {
		t.Fatalf("expected 2 snapshots, got %d", mgr.CountSnapshots())
	}

	list := mgr.ListSnapshots()
	if len(list) != 2 {
		t.Fatalf("expected 2 in list, got %d", len(list))
	}
}

// ---------------------------------------------------------------------------
// Concurrency
// ---------------------------------------------------------------------------

func TestConcurrency(t *testing.T) {
	dir := t.TempDir()
	mgr, _ := NewManager(filepath.Join(dir, "storage-ops.log"), noopLogger{})

	done := make(chan struct{})
	for i := 0; i < 50; i++ {
		go func(idx int) {
			defer func() { done <- struct{}{} }()
			path := createTempDisk(t, dir)
			_, err := mgr.AddDisk(&Disk{Path: path})
			if err != nil {
				t.Errorf("add %d: %v", idx, err)
			}
		}(i)
	}
	for i := 0; i < 50; i++ {
		<-done
	}

	if mgr.CountDisks() != 50 {
		t.Fatalf("expected 50 disks, got %d", mgr.CountDisks())
	}
}

// ---------------------------------------------------------------------------
// Operation log
// ---------------------------------------------------------------------------

func TestReadOpLog(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "storage-ops.log")
	mgr, _ := NewManager(logPath, noopLogger{})

	entries, err := mgr.ReadOpLog()
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

func TestOpLogContainsDurationAndOutcome(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "storage-ops.log")
	mgr, _ := NewManager(logPath, noopLogger{})

	diskPath := createTempDisk(t, dir)
	mgr.AddDisk(&Disk{ID: "disk-log", Path: diskPath})

	entries, err := mgr.ReadOpLog()
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	var found bool
	for _, e := range entries {
		if e.Operation == "AddDisk" && e.DiskID == "disk-log" {
			found = true
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
		t.Fatal("expected AddDisk entry in op log")
	}
}

func TestOpLogErrorEntry(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "storage-ops.log")
	mgr, _ := NewManager(logPath, noopLogger{})

	mgr.RemoveDisk("disk-missing")

	entries, err := mgr.ReadOpLog()
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	var found bool
	for _, e := range entries {
		if e.Operation == "RemoveDisk" && e.Outcome == "error" {
			found = true
			if e.Message == "" {
				t.Fatal("expected error message")
			}
		}
	}
	if !found {
		t.Fatal("expected error entry in op log")
	}
}

func TestReadOpLogMalformed(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "storage-ops.log")
	_ = os.WriteFile(logPath, []byte("not json\n"), 0644)

	mgr, _ := NewManager(logPath, noopLogger{})
	_, err := mgr.ReadOpLog()
	if err == nil {
		t.Fatal("expected error for malformed log")
	}
}

// ---------------------------------------------------------------------------
// ID generation
// ---------------------------------------------------------------------------

func TestGenerateDiskID(t *testing.T) {
	id := generateDiskID()
	if !strings.HasPrefix(id, "disk") {
		t.Fatalf("expected disk prefix, got %s", id)
	}
	if len(id) != 10 { // "disk" + 6 chars
		t.Fatalf("expected 10 chars, got %d", len(id))
	}
}

func TestGenerateSnapshotID(t *testing.T) {
	id := generateSnapshotID()
	if !strings.HasPrefix(id, "snap") {
		t.Fatalf("expected snap prefix, got %s", id)
	}
	if len(id) != 12 { // "snap" + 8 chars
		t.Fatalf("expected 12 chars, got %d", len(id))
	}
}

// ---------------------------------------------------------------------------
// Error types
// ---------------------------------------------------------------------------

func TestErrorString(t *testing.T) {
	err := &Error{Op: "Test", Message: "something failed"}
	want := "storage.Test: something failed"
	if got := err.Error(); got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestIsStorageError(t *testing.T) {
	if !IsStorageError(&Error{Op: "test"}) {
		t.Fatal("expected true for storage.Error")
	}
	if IsStorageError(fmt.Errorf("plain error")) {
		t.Fatal("expected false for plain error")
	}
}

// ---------------------------------------------------------------------------
// Clone
// ---------------------------------------------------------------------------

func TestDiskClone(t *testing.T) {
	d := &Disk{
		ID:        "disk-1",
		Path:      "/tmp/disk.raw",
		Format:    "raw",
		Readonly:  true,
		Direct:    true,
		NumQueues: 4,
		QueueSize: 256,
	}
	cp := d.Clone()
	if cp.ID != d.ID {
		t.Fatal("clone mismatch")
	}
	// Ensure it's a distinct pointer.
	cp.ID = "disk-2"
	if d.ID != "disk-1" {
		t.Fatal("clone mutated original")
	}
}

func TestSnapshotClone(t *testing.T) {
	s := &Snapshot{
		ID:        "snap-1",
		DiskID:    "disk-1",
		Path:      "/tmp/snap.raw",
		CreatedAt: time.Now(),
		Size:      1024,
	}
	cp := s.Clone()
	if cp.ID != s.ID {
		t.Fatal("clone mismatch")
	}
	cp.ID = "snap-2"
	if s.ID != "snap-1" {
		t.Fatal("clone mutated original")
	}
}
