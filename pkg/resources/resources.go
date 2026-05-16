package resources

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/gokaybaz/go-cloud-hypervisor-service/pkg/logging"
)

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

// Config holds the current VM resource configuration.
type Config struct {
	// CPUs is the number of virtual CPUs assigned to the VM.
	CPUs int `json:"cpus"`

	// MemoryMB is the RAM allocated to the VM in megabytes.
	MemoryMB int `json:"memory_mb"`
}

// Clone returns a deep copy of the configuration.
func (c *Config) Clone() *Config {
	cp := *c
	return &cp
}

// Limits defines the validation boundaries for CPU and memory.
type Limits struct {
	// MinCPUs is the minimum number of vCPUs (default 1).
	MinCPUs int `json:"min_cpus"`

	// MaxCPUs is the maximum number of vCPUs (default 1024).
	MaxCPUs int `json:"max_cpus"`

	// MinMemoryMB is the minimum memory in MB (default 64).
	MinMemoryMB int `json:"min_memory_mb"`

	// MaxMemoryMB is the maximum memory in MB (default 1,048,576 = 1 TiB).
	MaxMemoryMB int `json:"max_memory_mb"`

	// MemoryAlignMB is the memory alignment step in MB (default 4).
	// Memory values must be an integer multiple of this field.
	MemoryAlignMB int `json:"memory_align_mb"`
}

// DefaultLimits returns the built-in default limits.
func DefaultLimits() Limits {
	return Limits{
		MinCPUs:       1,
		MaxCPUs:       1024,
		MinMemoryMB:   64,
		MaxMemoryMB:   1048576,
		MemoryAlignMB: 4,
	}
}

// ValidateCPU checks whether count satisfies the CPU limits.
func (l Limits) ValidateCPU(count int) error {
	if count < l.MinCPUs {
		return &Error{Op: "ValidateCPU", Message: fmt.Sprintf("cpu count %d below minimum %d", count, l.MinCPUs)}
	}
	if count > l.MaxCPUs {
		return &Error{Op: "ValidateCPU", Message: fmt.Sprintf("cpu count %d above maximum %d", count, l.MaxCPUs)}
	}
	return nil
}

// ValidateMemory checks whether sizeMB satisfies the memory limits and alignment.
func (l Limits) ValidateMemory(sizeMB int) error {
	if sizeMB < l.MinMemoryMB {
		return &Error{Op: "ValidateMemory", Message: fmt.Sprintf("memory %d MB below minimum %d MB", sizeMB, l.MinMemoryMB)}
	}
	if sizeMB > l.MaxMemoryMB {
		return &Error{Op: "ValidateMemory", Message: fmt.Sprintf("memory %d MB above maximum %d MB", sizeMB, l.MaxMemoryMB)}
	}
	if l.MemoryAlignMB > 0 && sizeMB%l.MemoryAlignMB != 0 {
		return &Error{Op: "ValidateMemory", Message: fmt.Sprintf("memory %d MB is not aligned to %d MB", sizeMB, l.MemoryAlignMB)}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Manager
// ---------------------------------------------------------------------------

// Manager manages vCPU and memory configuration for a single VM.  It persists
// the current configuration to disk and logs every resize event.
type Manager struct {
	mu        sync.RWMutex
	config    Config
	limits    Limits
	statePath string
	logPath   string
	logMu     sync.Mutex
	logger    logging.Logger
}

// NewManager creates a Manager.  statePath is the JSON file where the current
// Config is persisted.  logPath is the NDJSON file where resize events are
// appended.  If statePath exists it is loaded; otherwise default Config
// (1 CPU, 256 MB) is used.
func NewManager(statePath, logPath string, logger logging.Logger) (*Manager, error) {
	if statePath == "" {
		return nil, &Error{Op: "NewManager", Message: "state path is required"}
	}
	if logPath == "" {
		return nil, &Error{Op: "NewManager", Message: "log path is required"}
	}

	dir := filepath.Dir(statePath)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, &Error{Op: "NewManager", Message: fmt.Sprintf("mkdir %s: %v", dir, err)}
		}
	}
	dir = filepath.Dir(logPath)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, &Error{Op: "NewManager", Message: fmt.Sprintf("mkdir %s: %v", dir, err)}
		}
	}

	m := &Manager{
		config:    Config{CPUs: 1, MemoryMB: 256},
		limits:    DefaultLimits(),
		statePath: statePath,
		logPath:   logPath,
		logger:    logger,
	}

	// Load existing state if present.
	if data, err := os.ReadFile(statePath); err == nil {
		var loaded Config
		if err := json.Unmarshal(data, &loaded); err != nil {
			return nil, &Error{Op: "NewManager", Message: fmt.Sprintf("load state: %v", err)}
		}
		m.config = loaded
	}

	// Persist initial state so the file always exists.
	if err := m.persistStateLocked(); err != nil {
		return nil, &Error{Op: "NewManager", Message: fmt.Sprintf("persist initial state: %v", err)}
	}

	_ = m.writeLog(resizeLogEntry{
		Timestamp: time.Now().UTC(),
		Operation: "init",
		Outcome:   "success",
		Message:   "resources manager initialised",
	})

	return m, nil
}

// SetLimits updates the validation boundaries.  Must be called before any
// resize operation if the defaults are unsuitable.
func (m *Manager) SetLimits(limits Limits) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.limits = limits
}

// Limits returns a copy of the current limits.
func (m *Manager) Limits() Limits {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.limits
}

// ResizeCPU changes the vCPU count.  The new value is validated against the
// current limits, written to memory, persisted to statePath, and logged.
// source identifies the request origin (e.g. "api", "cli", "auto-scaler").
func (m *Manager) ResizeCPU(count int, source string) error {
	if source == "" {
		source = "unknown"
	}

	start := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.limits.ValidateCPU(count); err != nil {
		m.logResize(start, "ResizeCPU", source, m.config.CPUs, count, "error", err.Error())
		return err
	}

	old := m.config.CPUs
	m.config.CPUs = count

	if err := m.persistStateLocked(); err != nil {
		m.config.CPUs = old // rollback
		m.logResize(start, "ResizeCPU", source, old, count, "error", fmt.Sprintf("persist failed: %v", err))
		return &Error{Op: "ResizeCPU", Message: fmt.Sprintf("persist failed: %v", err)}
	}

	m.logResize(start, "ResizeCPU", source, old, count, "success", "")
	m.logger.Info("cpu resized", "source", source, "before", old, "after", count)

	return nil
}

// ResizeMemory changes the memory size in megabytes.  The new value is
// validated against the current limits and alignment, written to memory,
// persisted to statePath, and logged.
func (m *Manager) ResizeMemory(sizeMB int, source string) error {
	if source == "" {
		source = "unknown"
	}

	start := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.limits.ValidateMemory(sizeMB); err != nil {
		m.logResize(start, "ResizeMemory", source, m.config.MemoryMB, sizeMB, "error", err.Error())
		return err
	}

	old := m.config.MemoryMB
	m.config.MemoryMB = sizeMB

	if err := m.persistStateLocked(); err != nil {
		m.config.MemoryMB = old // rollback
		m.logResize(start, "ResizeMemory", source, old, sizeMB, "error", fmt.Sprintf("persist failed: %v", err))
		return &Error{Op: "ResizeMemory", Message: fmt.Sprintf("persist failed: %v", err)}
	}

	m.logResize(start, "ResizeMemory", source, old, sizeMB, "success", "")
	m.logger.Info("memory resized", "source", source, "before", old, "after", sizeMB)

	return nil
}

// Config returns a deep copy of the current configuration.
func (m *Manager) Config() *Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.config.Clone()
}

// ---------------------------------------------------------------------------
// State persistence
// ---------------------------------------------------------------------------

// persistStateLocked writes the current config to statePath atomically.
// Caller must hold m.mu.
func (m *Manager) persistStateLocked() error {
	data, err := json.Marshal(m.config)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	tmp := m.statePath + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return fmt.Errorf("write temp state: %w", err)
	}
	if err := os.Rename(tmp, m.statePath); err != nil {
		return fmt.Errorf("rename state: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Operation log
// ---------------------------------------------------------------------------

// resizeLogEntry is a single line in the resources operation log.
type resizeLogEntry struct {
	Timestamp time.Time     `json:"timestamp"`
	Operation string        `json:"operation"`
	Source    string        `json:"source"`
	Before    int           `json:"before"`
	After     int           `json:"after"`
	Duration  time.Duration `json:"duration_ms"`
	Outcome   string        `json:"outcome"` // "success" or "error"
	Message   string        `json:"message,omitempty"`
	Caller    callerFrame   `json:"caller"`
}

// callerFrame captures the call site.
type callerFrame struct {
	File string `json:"file"`
	Line int    `json:"line"`
	Func string `json:"func"`
}

// callerInfo captures the caller at skip frames above the caller of
// callerInfo.
func callerInfo(skip int) callerFrame {
	pc, file, line, ok := runtime.Caller(skip + 1)
	if !ok {
		return callerFrame{}
	}
	fn := runtime.FuncForPC(pc)
	name := ""
	if fn != nil {
		name = fn.Name()
	}
	return callerFrame{
		File: filepath.Base(file),
		Line: line,
		Func: name,
	}
}

// logResize records a resize event with before/after values, source, duration,
// and outcome.
func (m *Manager) logResize(start time.Time, op, source string, before, after int, outcome, msg string) {
	_ = m.writeLog(resizeLogEntry{
		Timestamp: time.Now().UTC(),
		Operation: op,
		Source:    source,
		Before:    before,
		After:     after,
		Duration:  time.Since(start).Round(time.Microsecond),
		Outcome:   outcome,
		Message:   msg,
		Caller:    callerInfo(2),
	})
}

// writeLog appends a JSON-encoded operation entry to the log file.
func (m *Manager) writeLog(entry resizeLogEntry) error {
	m.logMu.Lock()
	defer m.logMu.Unlock()

	f, err := os.OpenFile(m.logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open log: %w", err)
	}
	defer f.Close()

	b, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal log entry: %w", err)
	}

	if _, err := f.Write(b); err != nil {
		return fmt.Errorf("write log entry: %w", err)
	}
	if _, err := f.WriteString("\n"); err != nil {
		return fmt.Errorf("write log newline: %w", err)
	}

	return nil
}

// ReadLog returns all resize entries from the log file.
func (m *Manager) ReadLog() ([]resizeLogEntry, error) {
	m.logMu.Lock()
	defer m.logMu.Unlock()

	data, err := os.ReadFile(m.logPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read log: %w", err)
	}

	var entries []resizeLogEntry
	lines := splitLines(string(data))
	for _, line := range lines {
		line = trimSpace(line)
		if line == "" {
			continue
		}
		var entry resizeLogEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			return nil, fmt.Errorf("unmarshal log entry: %w", err)
		}
		entries = append(entries, entry)
	}

	return entries, nil
}

// ---------------------------------------------------------------------------
// Error type
// ---------------------------------------------------------------------------

// Error is returned by Manager methods on failure.
type Error struct {
	Op      string `json:"op"`
	Message string `json:"message"`
}

// Error implements the error interface.
func (e *Error) Error() string {
	return fmt.Sprintf("resources.%s: %s", e.Op, e.Message)
}

// IsResourcesError reports whether err is a *Error.
func IsResourcesError(err error) bool {
	_, ok := err.(*Error)
	return ok
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

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
