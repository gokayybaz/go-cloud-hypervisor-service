package network

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/org/ch-api/pkg/logging"
)

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

// Interface describes a virtio-net virtual NIC.
type Interface struct {
	// ID is a unique identifier for this interface within the VM.
	// When empty a UUID is generated on Add.
	ID string `json:"id"`

	// Mac is the guest MAC address.  When empty a random MAC is generated.
	Mac string `json:"mac,omitempty"`

	// Tap is the host TAP device name (e.g. "tap0").
	Tap string `json:"tap,omitempty"`

	// IP is the guest IP address (optional — used for record-keeping).
	IP string `json:"ip,omitempty"`

	// Mask is the guest network mask (optional).
	Mask string `json:"mask,omitempty"`

	// NumQueues is the number of virtio queues (default 2).
	NumQueues int `json:"num_queues,omitempty"`

	// QueueSize is the size of each virtio queue (default 256).
	QueueSize int `json:"queue_size,omitempty"`

	// QoS holds the current bandwidth limits.
	QoS QoSConfig `json:"qos,omitempty"`
}

// QoSConfig describes bandwidth limits for a virtio-net interface.
type QoSConfig struct {
	// IngressRate is the ingress bandwidth limit in bytes/sec.
	// Zero means unlimited.
	IngressRate int64 `json:"ingress_rate,omitempty"`

	// EgressRate is the egress bandwidth limit in bytes/sec.
	// Zero means unlimited.
	EgressRate int64 `json:"egress_rate,omitempty"`
}

// Validate checks the interface for structural correctness.
func (iface *Interface) Validate() error {
	if iface.ID == "" {
		return &Error{Op: "validate", Message: "interface ID is required"}
	}
	if iface.Mac != "" {
		if _, err := net.ParseMAC(iface.Mac); err != nil {
			return &Error{Op: "validate", Message: fmt.Sprintf("invalid MAC %q: %v", iface.Mac, err)}
		}
	}
	if iface.IP != "" && net.ParseIP(iface.IP) == nil {
		return &Error{Op: "validate", Message: fmt.Sprintf("invalid IP %q", iface.IP)}
	}
	return nil
}

// Clone returns a deep copy of the interface.
func (iface *Interface) Clone() *Interface {
	cp := *iface
	return &cp
}

// ---------------------------------------------------------------------------
// Manager
// ---------------------------------------------------------------------------

// Manager manages virtio-net interfaces for a single VM and persists every
// mutation to an audit log.
type Manager struct {
	mu         sync.RWMutex
	ifaces     map[string]*Interface
	auditPath  string
	auditMu    sync.Mutex
	logger     logging.Logger
}

// NewManager creates a Manager.  auditPath is the file where audit entries
// are appended.  The file is created if it does not exist.
func NewManager(auditPath string, logger logging.Logger) (*Manager, error) {
	if auditPath == "" {
		return nil, &Error{Op: "NewManager", Message: "audit path is required"}
	}

	// Ensure the audit directory exists.
	dir := filepath.Dir(auditPath)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, &Error{Op: "NewManager", Message: fmt.Sprintf("mkdir %s: %v", dir, err)}
		}
	}

	m := &Manager{
		ifaces:    make(map[string]*Interface),
		auditPath: auditPath,
		logger:    logger,
	}

	// Write an initialisation entry so operators know when the log starts.
	_ = m.writeAudit(auditEntry{
		Timestamp: time.Now().UTC(),
		Operation: "init",
		Message:   "network manager initialised",
	})

	return m, nil
}

// AddInterface adds a new virtio-net interface.  It generates an ID and MAC
// if they are empty.  Returns a copy of the fully populated interface.
func (m *Manager) AddInterface(iface *Interface) (*Interface, error) {
	if iface == nil {
		return nil, &Error{Op: "AddInterface", Message: "nil interface"}
	}

	// Generate MAC and defaults before taking the lock.
	if iface.Mac == "" {
		iface.Mac = generateMAC()
	}
	if iface.NumQueues == 0 {
		iface.NumQueues = 2
	}
	if iface.QueueSize == 0 {
		iface.QueueSize = 256
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Generate ID inside the lock with collision retry.
	if iface.ID == "" {
		for attempts := 0; attempts < 10; attempts++ {
			id := generateID()
			if _, exists := m.ifaces[id]; !exists {
				iface.ID = id
				break
			}
		}
		if iface.ID == "" {
			return nil, &Error{Op: "AddInterface", Message: "failed to generate unique interface ID"}
		}
	}

	if err := iface.Validate(); err != nil {
		return nil, err
	}

	if _, exists := m.ifaces[iface.ID]; exists {
		return nil, &Error{Op: "AddInterface", Message: fmt.Sprintf("interface %q already exists", iface.ID)}
	}

	m.ifaces[iface.ID] = iface.Clone()

	_ = m.writeAudit(auditEntry{
		Timestamp:   time.Now().UTC(),
		Operation:   "AddInterface",
		InterfaceID: iface.ID,
		Details: map[string]any{
			"mac":        iface.Mac,
			"tap":        iface.Tap,
			"ip":         iface.IP,
			"num_queues": iface.NumQueues,
			"queue_size": iface.QueueSize,
		},
		Caller: callerInfo(1),
	})

	m.logger.Info("interface added", "id", iface.ID, "mac", iface.Mac, "tap", iface.Tap)

	return m.ifaces[iface.ID].Clone(), nil
}

// RemoveInterface removes an interface by ID.  Returns an error if the
// interface does not exist.
func (m *Manager) RemoveInterface(id string) error {
	if id == "" {
		return &Error{Op: "RemoveInterface", Message: "interface ID is required"}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	iface, exists := m.ifaces[id]
	if !exists {
		return &Error{Op: "RemoveInterface", Message: fmt.Sprintf("interface %q not found", id)}
	}

	delete(m.ifaces, id)

	_ = m.writeAudit(auditEntry{
		Timestamp:   time.Now().UTC(),
		Operation:   "RemoveInterface",
		InterfaceID: id,
		Details: map[string]any{
			"mac": iface.Mac,
			"tap": iface.Tap,
		},
		Caller: callerInfo(1),
	})

	m.logger.Info("interface removed", "id", id, "mac", iface.Mac)

	return nil
}

// SetQoS applies bandwidth limits to an interface.
func (m *Manager) SetQoS(id string, qos QoSConfig) error {
	if id == "" {
		return &Error{Op: "SetQoS", Message: "interface ID is required"}
	}
	if qos.IngressRate < 0 || qos.EgressRate < 0 {
		return &Error{Op: "SetQoS", Message: "bandwidth rates must be non-negative"}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	iface, exists := m.ifaces[id]
	if !exists {
		return &Error{Op: "SetQoS", Message: fmt.Sprintf("interface %q not found", id)}
	}

	iface.QoS = qos

	_ = m.writeAudit(auditEntry{
		Timestamp:   time.Now().UTC(),
		Operation:   "SetQoS",
		InterfaceID: id,
		Details: map[string]any{
			"ingress_rate": qos.IngressRate,
			"egress_rate":  qos.EgressRate,
		},
		Caller: callerInfo(1),
	})

	m.logger.Info("qos updated", "id", id,
		"ingress_rate", qos.IngressRate,
		"egress_rate", qos.EgressRate)

	return nil
}

// Get returns a copy of the interface with the given ID.
func (m *Manager) Get(id string) (*Interface, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	iface, exists := m.ifaces[id]
	if !exists {
		return nil, &Error{Op: "Get", Message: fmt.Sprintf("interface %q not found", id)}
	}

	return iface.Clone(), nil
}

// List returns a snapshot of all interfaces.
func (m *Manager) List() []*Interface {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]*Interface, 0, len(m.ifaces))
	for _, iface := range m.ifaces {
		out = append(out, iface.Clone())
	}
	return out
}

// Count returns the number of managed interfaces.
func (m *Manager) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.ifaces)
}

// ---------------------------------------------------------------------------
// Audit log
// ---------------------------------------------------------------------------

// auditEntry is a single line in the audit log.
type auditEntry struct {
	Timestamp   time.Time      `json:"timestamp"`
	Operation   string         `json:"operation"`
	InterfaceID string         `json:"interface_id,omitempty"`
	Details     map[string]any `json:"details,omitempty"`
	Message     string         `json:"message,omitempty"`
	Caller      callerFrame    `json:"caller"`
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

// writeAudit appends a JSON-encoded audit entry to the audit log file.
func (m *Manager) writeAudit(entry auditEntry) error {
	m.auditMu.Lock()
	defer m.auditMu.Unlock()

	f, err := os.OpenFile(m.auditPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open audit log: %w", err)
	}
	defer f.Close()

	b, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal audit entry: %w", err)
	}

	if _, err := f.Write(b); err != nil {
		return fmt.Errorf("write audit entry: %w", err)
	}
	if _, err := f.WriteString("\n"); err != nil {
		return fmt.Errorf("write audit newline: %w", err)
	}

	return nil
}

// ReadAudit returns all audit entries from the log file.
func (m *Manager) ReadAudit() ([]auditEntry, error) {
	m.auditMu.Lock()
	defer m.auditMu.Unlock()

	data, err := os.ReadFile(m.auditPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read audit log: %w", err)
	}

	var entries []auditEntry
	lines := splitLines(string(data))
	for _, line := range lines {
		line = trimSpace(line)
		if line == "" {
			continue
		}
		var entry auditEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			return nil, fmt.Errorf("unmarshal audit entry: %w", err)
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
	return fmt.Sprintf("network.%s: %s", e.Op, e.Message)
}

// IsNetworkError reports whether err is a *Error.
func IsNetworkError(err error) bool {
	_, ok := err.(*Error)
	return ok
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// generateID creates a random interface ID.
func generateID() string {
	const letters = "abcdefghijklmnopqrstuvwxyz"
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		// Fallback to time-based generation on entropy exhaustion.
		for i := range b {
			b[i] = byte('a' + (time.Now().UnixNano()+int64(i))%26)
		}
	} else {
		for i := range b {
			b[i] = letters[int(b[i])%len(letters)]
		}
	}
	return "eth" + string(b)
}

// generateMAC creates a locally-administered unicast MAC address.
func generateMAC() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		// Fallback to time + pid for determinism in tests.
		seed := time.Now().UnixNano() ^ int64(os.Getpid())
		for i := range b {
			b[i] = byte(seed >> (uint(i) * 8))
		}
	}
	b[0] = (b[0] | 0x02) & 0xfe // locally administered, unicast
	return net.HardwareAddr(b).String()
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