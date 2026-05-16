// Package eventlog emits human-readable changelog-style entries for every VM
// operation.  Each entry records the event type, VM ID, actor, and the
// before/after state of the VM.
package eventlog

import (
	"fmt"
	"time"
)

// Entry is a single changelog record.
type Entry struct {
	Timestamp time.Time
	Event     string // e.g. "create", "delete", "boot", "pause"
	VMID      string
	Actor     string
	Before    string // human-readable state before the change
	After     string // human-readable state after the change
}

// Writer is a sink for changelog entries.
type Writer interface {
	// Write persists a single entry.
	Write(entry Entry) error
	// Close releases any resources held by the writer.
	Close() error
}

// ---------------------------------------------------------------------------
// MultiWriter
// ---------------------------------------------------------------------------

// MultiWriter writes to every configured backend.
type MultiWriter struct {
	writers []Writer
}

// NewMulti creates a MultiWriter that broadcasts to all supplied writers.
func NewMulti(writers ...Writer) *MultiWriter {
	return &MultiWriter{writers: writers}
}

// Write sends the entry to every backend.  Individual errors are silently
// ignored so that a failing syslog server does not block VM operations.
func (m *MultiWriter) Write(entry Entry) error {
	for _, w := range m.writers {
		_ = w.Write(entry)
	}
	return nil
}

// Close closes all backends.
func (m *MultiWriter) Close() error {
	var lastErr error
	for _, w := range m.writers {
		if err := w.Close(); err != nil {
			lastErr = err
		}
	}
	return lastErr
}

// ---------------------------------------------------------------------------
// Formatter
// ---------------------------------------------------------------------------

// Format returns a single human-readable line for the entry.
// Example:
//
//	2026-05-13T21:30:00Z [boot] vm-abc by alice: created → running
func Format(entry Entry) string {
	ts := entry.Timestamp.UTC().Format(time.RFC3339)
	actor := entry.Actor
	if actor == "" {
		actor = "system"
	}
	return fmt.Sprintf("%s [%s] %s by %s: %s → %s",
		ts, entry.Event, entry.VMID, actor, stateOrDash(entry.Before), stateOrDash(entry.After))
}

func stateOrDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
