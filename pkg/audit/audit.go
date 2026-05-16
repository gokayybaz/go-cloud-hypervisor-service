package audit

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS operation_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    trace_id TEXT NOT NULL,
    user TEXT,
    method TEXT NOT NULL,
    path TEXT NOT NULL,
    body_hash TEXT,
    duration_ms INTEGER NOT NULL,
    status_code INTEGER NOT NULL,
    ip TEXT NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_trace_id ON operation_log(trace_id);
CREATE INDEX IF NOT EXISTS idx_created_at ON operation_log(created_at);
`

// Entry holds a single audit record.
type Entry struct {
	TraceID    string
	User       string
	Method     string
	Path       string
	BodyHash   string
	DurationMs int64
	StatusCode int
	IP         string
}

// Auditor persists HTTP request metadata to a SQLite database with daily rotation.
type Auditor struct {
	mu       sync.Mutex
	basePath string // e.g., "data/audit" -> files "data/audit-2006-01-02.db"
	db       *sql.DB
	current  string // current date suffix: "2006-01-02"
}

// New creates an Auditor that writes to path-YYYY-MM-DD.db.
func New(basePath string) (*Auditor, error) {
	dir := filepath.Dir(basePath)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("mkdir %s: %w", dir, err)
		}
	}
	a := &Auditor{basePath: basePath}
	if err := a.rotate(); err != nil {
		return nil, err
	}
	return a, nil
}

// Close closes the current database connection.
func (a *Auditor) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.db != nil {
		return a.db.Close()
	}
	return nil
}

// rotate opens the database for the current day.
func (a *Auditor) rotate() error {
	today := time.Now().UTC().Format("2006-01-02")
	if a.current == today && a.db != nil {
		return nil
	}
	if a.db != nil {
		_ = a.db.Close()
	}
	path := a.basePath + "-" + today + ".db"
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return fmt.Errorf("open sqlite %s: %w", path, err)
	}
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return fmt.Errorf("create schema: %w", err)
	}
	// Best-effort performance tuning.
	_, _ = db.Exec("PRAGMA journal_mode=WAL;")
	_, _ = db.Exec("PRAGMA synchronous=NORMAL;")
	a.db = db
	a.current = today
	return nil
}

// Record writes an entry to the operation_log table.
func (a *Auditor) Record(ctx context.Context, entry Entry) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if time.Now().UTC().Format("2006-01-02") != a.current {
		if err := a.rotate(); err != nil {
			return err
		}
	}

	_, err := a.db.ExecContext(ctx, `
        INSERT INTO operation_log (trace_id, user, method, path, body_hash, duration_ms, status_code, ip)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?)
    `, entry.TraceID, entry.User, entry.Method, entry.Path, entry.BodyHash, entry.DurationMs, entry.StatusCode, entry.IP)
	if err != nil {
		return fmt.Errorf("insert operation_log: %w", err)
	}
	return nil
}
