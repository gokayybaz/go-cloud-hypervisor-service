package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// OperationLogEntry
// ---------------------------------------------------------------------------

// OperationLogEntry records a single VM lifecycle operation.
type OperationLogEntry struct {
	Timestamp time.Time `json:"timestamp"`
	VMID      string    `json:"vm_id"`
	Operation string    `json:"operation"`
	User      string    `json:"user"`
	Outcome   string    `json:"outcome"` // "success" or "error"
	Message   string    `json:"message,omitempty"`
}

// ---------------------------------------------------------------------------
// OperationLog
// ---------------------------------------------------------------------------

// OperationLog is an append-only NDJSON log of VM lifecycle operations.
type OperationLog struct {
	mu      sync.Mutex
	path    string
}

// NewOperationLog creates an OperationLog.  The file is created if it does
// not exist.
func NewOperationLog(path string) (*OperationLog, error) {
	if path == "" {
		return nil, fmt.Errorf("operation log path is required")
	}
	dir := filepath.Dir(path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("mkdir %s: %w", dir, err)
		}
	}
	return &OperationLog{path: path}, nil
}

// Write appends an entry to the log.
func (l *OperationLog) Write(entry OperationLogEntry) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	f, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open op log: %w", err)
	}
	defer f.Close()

	b, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal op log entry: %w", err)
	}

	if _, err := f.Write(b); err != nil {
		return fmt.Errorf("write op log entry: %w", err)
	}
	if _, err := f.WriteString("\n"); err != nil {
		return fmt.Errorf("write op log newline: %w", err)
	}
	return nil
}

// Read returns all entries from the log.
func (l *OperationLog) Read() ([]OperationLogEntry, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	data, err := os.ReadFile(l.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read op log: %w", err)
	}

	var entries []OperationLogEntry
	lines := splitLines(string(data))
	for _, line := range lines {
		line = trimSpace(line)
		if line == "" {
			continue
		}
		var entry OperationLogEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			return nil, fmt.Errorf("unmarshal op log entry: %w", err)
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// splitLines splits s on newlines.  Works on both LF and CRLF.
func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

// trimSpace removes trailing \r from a line.
func trimSpace(s string) string {
	if len(s) > 0 && s[len(s)-1] == '\r' {
		return s[:len(s)-1]
	}
	return s
}
