package eventlog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFormat(t *testing.T) {
	entry := Entry{
		Timestamp: time.Date(2026, 5, 13, 21, 30, 0, 0, time.UTC),
		Event:     "boot",
		VMID:      "vm-abc",
		Actor:     "alice",
		Before:    "status=created",
		After:     "status=running",
	}
	got := Format(entry)
	want := "2026-05-13T21:30:00Z [boot] vm-abc by alice: status=created → status=running"
	if got != want {
		t.Fatalf("format mismatch:\ngot:  %s\nwant: %s", got, want)
	}
}

func TestFormatEmptyActor(t *testing.T) {
	entry := Entry{
		Timestamp: time.Date(2026, 5, 13, 21, 30, 0, 0, time.UTC),
		Event:     "create",
		VMID:      "vm-xyz",
		Actor:     "",
		Before:    "",
		After:     "status=created name=test-vm",
	}
	got := Format(entry)
	want := "2026-05-13T21:30:00Z [create] vm-xyz by system: — → status=created name=test-vm"
	if got != want {
		t.Fatalf("format mismatch:\ngot:  %s\nwant: %s", got, want)
	}
}

func TestFileWriter(t *testing.T) {
	dir := t.TempDir()
	w, err := NewFileWriter(filepath.Join(dir, "events"))
	if err != nil {
		t.Fatalf("new file writer: %v", err)
	}
	defer w.Close()

	entry := Entry{
		Timestamp: time.Now().UTC(),
		Event:     "delete",
		VMID:      "vm-1",
		Actor:     "bob",
		Before:    "status=stopped",
		After:     "",
	}
	if err := w.Write(entry); err != nil {
		t.Fatalf("write: %v", err)
	}

	matches, _ := os.ReadDir(dir)
	if len(matches) != 1 {
		t.Fatalf("expected 1 log file, got %d", len(matches))
	}

	data, err := os.ReadFile(filepath.Join(dir, matches[0].Name()))
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	line := strings.TrimSpace(string(data))
	if !strings.Contains(line, "[delete]") {
		t.Fatalf("expected [delete] in line, got %q", line)
	}
	if !strings.Contains(line, "vm-1") {
		t.Fatalf("expected vm-1 in line, got %q", line)
	}
	if !strings.Contains(line, "by bob") {
		t.Fatalf("expected 'by bob' in line, got %q", line)
	}
	if !strings.Contains(line, "status=stopped → —") {
		t.Fatalf("expected state transition in line, got %q", line)
	}
}

func TestFileWriterDailyRotation(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "events")
	w, err := NewFileWriter(base)
	if err != nil {
		t.Fatalf("new file writer: %v", err)
	}
	defer w.Close()

	if err := w.Write(Entry{Event: "create", VMID: "vm-a", Actor: "x", After: "status=created"}); err != nil {
		t.Fatalf("write: %v", err)
	}
	w.Close()

	// Simulate next day by renaming the current file.
	today := time.Now().UTC().Format("2006-01-02")
	oldPath := base + "-" + today + ".log"
	yesterday := time.Now().UTC().Add(-24 * time.Hour).Format("2006-01-02")
	newPath := base + "-" + yesterday + ".log"
	if err := os.Rename(oldPath, newPath); err != nil {
		t.Fatalf("rename: %v", err)
	}

	w2, err := NewFileWriter(base)
	if err != nil {
		t.Fatalf("new file writer 2: %v", err)
	}
	defer w2.Close()

	if err := w2.Write(Entry{Event: "delete", VMID: "vm-b", Actor: "y", Before: "status=stopped"}); err != nil {
		t.Fatalf("write 2: %v", err)
	}

	entries, _ := os.ReadDir(dir)
	logCount := 0
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".log") {
			logCount++
		}
	}
	if logCount != 2 {
		t.Fatalf("expected 2 log files, got %d", logCount)
	}
}

func TestMultiWriter(t *testing.T) {
	dir := t.TempDir()
	fw, _ := NewFileWriter(filepath.Join(dir, "events"))
	defer fw.Close()

	var captured []string
	capture := &testWriter{
		fn: func(e Entry) error {
			captured = append(captured, Format(e))
			return nil
		},
	}

	mw := NewMulti(fw, capture)
	defer mw.Close()

	entry := Entry{Event: "pause", VMID: "vm-2", Actor: "z", Before: "status=running", After: "status=paused"}
	if err := mw.Write(entry); err != nil {
		t.Fatalf("write: %v", err)
	}

	if len(captured) != 1 {
		t.Fatalf("expected 1 captured entry, got %d", len(captured))
	}
	if !strings.Contains(captured[0], "[pause]") {
		t.Fatalf("expected [pause] in captured, got %q", captured[0])
	}

	// Verify file also received it.
	matches, _ := os.ReadDir(dir)
	data, _ := os.ReadFile(filepath.Join(dir, matches[0].Name()))
	if !strings.Contains(string(data), "[pause]") {
		t.Fatalf("expected [pause] in file, got %q", string(data))
	}
}

// testWriter is a test double that delegates to a function.
type testWriter struct {
	fn func(Entry) error
}

func (t *testWriter) Write(e Entry) error { return t.fn(e) }
func (t *testWriter) Close() error        { return nil }
