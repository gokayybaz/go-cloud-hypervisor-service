package eventlog

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// FileWriter appends formatted entries to a daily-rotating log file.
type FileWriter struct {
	mu       sync.Mutex
	basePath string
	file     *os.File
	current  string // "2006-01-02"
}

// NewFileWriter creates a FileWriter that writes to basePath-YYYY-MM-DD.log.
func NewFileWriter(basePath string) (*FileWriter, error) {
	dir := filepath.Dir(basePath)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("mkdir %s: %w", dir, err)
		}
	}
	w := &FileWriter{basePath: basePath}
	if err := w.rotate(); err != nil {
		return nil, err
	}
	return w, nil
}

// Close closes the current log file.
func (w *FileWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file != nil {
		return w.file.Close()
	}
	return nil
}

// rotate opens the log file for the current day.
func (w *FileWriter) rotate() error {
	today := time.Now().UTC().Format("2006-01-02")
	if w.current == today && w.file != nil {
		return nil
	}
	if w.file != nil {
		_ = w.file.Close()
	}
	path := w.basePath + "-" + today + ".log"
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open event log %s: %w", path, err)
	}
	w.file = f
	w.current = today
	return nil
}

// Write appends a formatted entry to the current day's log file.
func (w *FileWriter) Write(entry Entry) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if time.Now().UTC().Format("2006-01-02") != w.current {
		if err := w.rotate(); err != nil {
			return err
		}
	}

	line := Format(entry) + "\n"
	if _, err := w.file.WriteString(line); err != nil {
		return fmt.Errorf("write event log: %w", err)
	}
	return nil
}
