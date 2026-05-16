package storage

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/gokaybaz/go-cloud-hypervisor-service/pkg/image"
	"github.com/gokaybaz/go-cloud-hypervisor-service/pkg/logging"
)

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

// Disk describes a virtio-blk virtual disk.
type Disk struct {
	// ID is a unique identifier for this disk within the VM.
	// When empty a random ID is generated on Add.
	ID string `json:"id"`

	// Path is the absolute path to the disk image file on the host.
	Path string `json:"path"`

	// Format is the on-disk image format.  When empty it is auto-detected
	// from the file extension.
	Format image.Format `json:"format,omitempty"`

	// Readonly marks the disk as read-only.
	Readonly bool `json:"readonly,omitempty"`

	// Direct enables O_DIRECT for the backing file.
	Direct bool `json:"direct,omitempty"`

	// NumQueues is the number of virtio queues (default 1).
	NumQueues int `json:"num_queues,omitempty"`

	// QueueSize is the size of each virtio queue (default 128).
	QueueSize int `json:"queue_size,omitempty"`
}

// Validate checks the disk for structural correctness.
func (d *Disk) Validate() error {
	if d.ID == "" {
		return &Error{Op: "validate", Message: "disk ID is required"}
	}
	if d.Path == "" {
		return &Error{Op: "validate", Message: "disk path is required"}
	}

	// Reuse image package validation for path, format, and existence.
	img := &image.Image{
		Path:   d.Path,
		Format: d.Format,
	}
	if err := img.Validate(); err != nil {
		return &Error{Op: "validate", Message: err.Error()}
	}
	// Sync back the resolved absolute path and detected format.
	d.Path = img.Path
	d.Format = img.Format

	return nil
}

// Clone returns a deep copy of the disk.
func (d *Disk) Clone() *Disk {
	cp := *d
	return &cp
}

// Snapshot describes a point-in-time copy of a disk.
type Snapshot struct {
	// ID is a unique identifier for this snapshot.
	ID string `json:"id"`

	// DiskID is the ID of the source disk.
	DiskID string `json:"disk_id"`

	// Path is the absolute path to the snapshot file on the host.
	Path string `json:"path"`

	// CreatedAt is the snapshot creation timestamp.
	CreatedAt time.Time `json:"created_at"`

	// Size is the snapshot file size in bytes.
	Size int64 `json:"size"`
}

// Clone returns a deep copy of the snapshot.
func (s *Snapshot) Clone() *Snapshot {
	cp := *s
	return &cp
}

// ---------------------------------------------------------------------------
// Manager
// ---------------------------------------------------------------------------

// Manager manages virtio-blk disks and snapshots for a single VM and logs
// every operation to a dedicated operation log.
type Manager struct {
	mu        sync.RWMutex
	disks     map[string]*Disk
	snapshots map[string]*Snapshot
	logPath   string
	logMu     sync.Mutex
	logger    logging.Logger
}

// NewManager creates a Manager.  logPath is the file where operation entries
// are appended.  The file is created if it does not exist.
func NewManager(logPath string, logger logging.Logger) (*Manager, error) {
	if logPath == "" {
		return nil, &Error{Op: "NewManager", Message: "log path is required"}
	}

	dir := filepath.Dir(logPath)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, &Error{Op: "NewManager", Message: fmt.Sprintf("mkdir %s: %v", dir, err)}
		}
	}

	m := &Manager{
		disks:     make(map[string]*Disk),
		snapshots: make(map[string]*Snapshot),
		logPath:   logPath,
		logger:    logger,
	}

	_ = m.writeOpLog(opLogEntry{
		Timestamp: time.Now().UTC(),
		Operation: "init",
		Outcome:   "success",
		Message:   "storage manager initialised",
	})

	return m, nil
}

// AddDisk hot-plugs a new virtio-blk disk.  It generates an ID if empty.
// Returns a copy of the fully populated disk.
func (m *Manager) AddDisk(disk *Disk) (*Disk, error) {
	if disk == nil {
		return nil, &Error{Op: "AddDisk", Message: "nil disk"}
	}

	if disk.NumQueues == 0 {
		disk.NumQueues = 1
	}
	if disk.QueueSize == 0 {
		disk.QueueSize = 128
	}

	start := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()

	if disk.ID == "" {
		for attempts := 0; attempts < 10; attempts++ {
			id := generateDiskID()
			if _, exists := m.disks[id]; !exists {
				disk.ID = id
				break
			}
		}
		if disk.ID == "" {
			m.logOp(start, "AddDisk", disk.ID, "error", "failed to generate unique disk ID")
			return nil, &Error{Op: "AddDisk", Message: "failed to generate unique disk ID"}
		}
	}

	if err := disk.Validate(); err != nil {
		m.logOp(start, "AddDisk", disk.ID, "error", err.Error())
		return nil, err
	}

	if _, exists := m.disks[disk.ID]; exists {
		m.logOp(start, "AddDisk", disk.ID, "error", fmt.Sprintf("disk %q already exists", disk.ID))
		return nil, &Error{Op: "AddDisk", Message: fmt.Sprintf("disk %q already exists", disk.ID)}
	}

	m.disks[disk.ID] = disk.Clone()

	m.logOp(start, "AddDisk", disk.ID, "success", "")
	m.logger.Info("disk added", "id", disk.ID, "path", disk.Path, "format", disk.Format)

	return m.disks[disk.ID].Clone(), nil
}

// RemoveDisk hot-unplugs a disk by ID.  Returns an error if the disk does
// not exist.
func (m *Manager) RemoveDisk(id string) error {
	if id == "" {
		return &Error{Op: "RemoveDisk", Message: "disk ID is required"}
	}

	start := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()

	disk, exists := m.disks[id]
	if !exists {
		m.logOp(start, "RemoveDisk", id, "error", fmt.Sprintf("disk %q not found", id))
		return &Error{Op: "RemoveDisk", Message: fmt.Sprintf("disk %q not found", id)}
	}

	delete(m.disks, id)

	m.logOp(start, "RemoveDisk", id, "success", "")
	m.logger.Info("disk removed", "id", id, "path", disk.Path)

	return nil
}

// CreateSnapshot creates a point-in-time copy of a disk.  The snapshot is
// written to snapshotPath.  Returns the snapshot metadata.
func (m *Manager) CreateSnapshot(diskID, snapshotPath string) (*Snapshot, error) {
	if diskID == "" {
		return nil, &Error{Op: "CreateSnapshot", Message: "disk ID is required"}
	}
	if snapshotPath == "" {
		return nil, &Error{Op: "CreateSnapshot", Message: "snapshot path is required"}
	}

	start := time.Now()
	m.mu.RLock()
	disk, exists := m.disks[diskID]
	m.mu.RUnlock()

	if !exists {
		m.logOp(start, "CreateSnapshot", diskID, "error", fmt.Sprintf("disk %q not found", diskID))
		return nil, &Error{Op: "CreateSnapshot", Message: fmt.Sprintf("disk %q not found", diskID)}
	}

	if err := CopyFile(disk.Path, snapshotPath); err != nil {
		m.logOp(start, "CreateSnapshot", diskID, "error", fmt.Sprintf("copy failed: %v", err))
		return nil, &Error{Op: "CreateSnapshot", Message: fmt.Sprintf("copy failed: %v", err)}
	}

	info, err := os.Stat(snapshotPath)
	if err != nil {
		m.logOp(start, "CreateSnapshot", diskID, "error", fmt.Sprintf("stat snapshot: %v", err))
		return nil, &Error{Op: "CreateSnapshot", Message: fmt.Sprintf("stat snapshot: %v", err)}
	}

	snap := &Snapshot{
		ID:        generateSnapshotID(),
		DiskID:    diskID,
		Path:      snapshotPath,
		CreatedAt: time.Now().UTC(),
		Size:      info.Size(),
	}

	m.mu.Lock()
	m.snapshots[snap.ID] = snap.Clone()
	m.mu.Unlock()

	m.logOp(start, "CreateSnapshot", diskID, "success", fmt.Sprintf("snapshot %s", snap.ID))
	m.logger.Info("snapshot created", "snapshot_id", snap.ID, "disk_id", diskID, "path", snapshotPath, "size", snap.Size)

	return snap.Clone(), nil
}

// RestoreSnapshot restores a disk from a snapshot file.  The current disk
// file is overwritten by the snapshot.
func (m *Manager) RestoreSnapshot(diskID, snapshotPath string) error {
	if diskID == "" {
		return &Error{Op: "RestoreSnapshot", Message: "disk ID is required"}
	}
	if snapshotPath == "" {
		return &Error{Op: "RestoreSnapshot", Message: "snapshot path is required"}
	}

	start := time.Now()
	m.mu.RLock()
	disk, exists := m.disks[diskID]
	m.mu.RUnlock()

	if !exists {
		m.logOp(start, "RestoreSnapshot", diskID, "error", fmt.Sprintf("disk %q not found", diskID))
		return &Error{Op: "RestoreSnapshot", Message: fmt.Sprintf("disk %q not found", diskID)}
	}

	if _, err := os.Stat(snapshotPath); err != nil {
		m.logOp(start, "RestoreSnapshot", diskID, "error", fmt.Sprintf("snapshot not found: %v", err))
		return &Error{Op: "RestoreSnapshot", Message: fmt.Sprintf("snapshot not found: %v", err)}
	}

	if err := CopyFile(snapshotPath, disk.Path); err != nil {
		m.logOp(start, "RestoreSnapshot", diskID, "error", fmt.Sprintf("copy failed: %v", err))
		return &Error{Op: "RestoreSnapshot", Message: fmt.Sprintf("copy failed: %v", err)}
	}

	m.logOp(start, "RestoreSnapshot", diskID, "success", fmt.Sprintf("from %s", snapshotPath))
	m.logger.Info("snapshot restored", "disk_id", diskID, "snapshot_path", snapshotPath)

	return nil
}

// GetDisk returns a copy of the disk with the given ID.
func (m *Manager) GetDisk(id string) (*Disk, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	disk, exists := m.disks[id]
	if !exists {
		return nil, &Error{Op: "GetDisk", Message: fmt.Sprintf("disk %q not found", id)}
	}

	return disk.Clone(), nil
}

// ListDisks returns a snapshot of all managed disks.
func (m *Manager) ListDisks() []*Disk {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]*Disk, 0, len(m.disks))
	for _, disk := range m.disks {
		out = append(out, disk.Clone())
	}
	return out
}

// CountDisks returns the number of managed disks.
func (m *Manager) CountDisks() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.disks)
}

// GetSnapshot returns a copy of the snapshot with the given ID.
func (m *Manager) GetSnapshot(id string) (*Snapshot, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	snap, exists := m.snapshots[id]
	if !exists {
		return nil, &Error{Op: "GetSnapshot", Message: fmt.Sprintf("snapshot %q not found", id)}
	}

	return snap.Clone(), nil
}

// ListSnapshots returns a snapshot of all recorded snapshots.
func (m *Manager) ListSnapshots() []*Snapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]*Snapshot, 0, len(m.snapshots))
	for _, snap := range m.snapshots {
		out = append(out, snap.Clone())
	}
	return out
}

// CountSnapshots returns the number of recorded snapshots.
func (m *Manager) CountSnapshots() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.snapshots)
}

// ---------------------------------------------------------------------------
// Operation log
// ---------------------------------------------------------------------------

// opLogEntry is a single line in the storage operation log.
type opLogEntry struct {
	Timestamp time.Time      `json:"timestamp"`
	Operation string         `json:"operation"`
	DiskID    string         `json:"disk_id,omitempty"`
	Duration  time.Duration  `json:"duration_ms"`
	Outcome   string         `json:"outcome"` // "success" or "error"
	Message   string         `json:"message,omitempty"`
	Caller    callerFrame    `json:"caller"`
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

// logOp records an operation with its duration and outcome.
func (m *Manager) logOp(start time.Time, op, diskID, outcome, msg string) {
	_ = m.writeOpLog(opLogEntry{
		Timestamp: time.Now().UTC(),
		Operation: op,
		DiskID:    diskID,
		Duration:  time.Since(start).Round(time.Microsecond),
		Outcome:   outcome,
		Message:   msg,
		Caller:    callerInfo(2),
	})
}

// writeOpLog appends a JSON-encoded operation entry to the log file.
func (m *Manager) writeOpLog(entry opLogEntry) error {
	m.logMu.Lock()
	defer m.logMu.Unlock()

	f, err := os.OpenFile(m.logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
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

// ReadOpLog returns all operation entries from the log file.
func (m *Manager) ReadOpLog() ([]opLogEntry, error) {
	m.logMu.Lock()
	defer m.logMu.Unlock()

	data, err := os.ReadFile(m.logPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read op log: %w", err)
	}

	var entries []opLogEntry
	lines := splitLines(string(data))
	for _, line := range lines {
		line = trimSpace(line)
		if line == "" {
			continue
		}
		var entry opLogEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			return nil, fmt.Errorf("unmarshal op log entry: %w", err)
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
	return fmt.Sprintf("storage.%s: %s", e.Op, e.Message)
}

// IsStorageError reports whether err is a *Error.
func IsStorageError(err error) bool {
	_, ok := err.(*Error)
	return ok
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// generateDiskID creates a random disk ID.
func generateDiskID() string {
	const letters = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		for i := range b {
			b[i] = letters[(time.Now().UnixNano()+int64(i))%int64(len(letters))]
		}
	} else {
		for i := range b {
			b[i] = letters[int(b[i])%len(letters)]
		}
	}
	return "disk" + string(b)
}

// generateSnapshotID creates a random snapshot ID.
func generateSnapshotID() string {
	const letters = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		for i := range b {
			b[i] = letters[(time.Now().UnixNano()+int64(i))%int64(len(letters))]
		}
	} else {
		for i := range b {
			b[i] = letters[int(b[i])%len(letters)]
		}
	}
	return "snap" + string(b)
}

// CopyFile copies src to dst using a buffered copy.
func CopyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	// Ensure destination directory exists.
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}

	return out.Close()
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
