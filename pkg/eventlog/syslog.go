package eventlog

import (
	"fmt"
	"log/syslog"
)

// SyslogWriter sends formatted entries to the local syslog daemon.
type SyslogWriter struct {
	w *syslog.Writer
}

// NewSyslogWriter creates a SyslogWriter using the given syslog tag.
func NewSyslogWriter(tag string) (*SyslogWriter, error) {
	w, err := syslog.New(syslog.LOG_INFO|syslog.LOG_DAEMON, tag)
	if err != nil {
		return nil, fmt.Errorf("open syslog: %w", err)
	}
	return &SyslogWriter{w: w}, nil
}

// Close closes the syslog connection.
func (s *SyslogWriter) Close() error {
	if s.w != nil {
		return s.w.Close()
	}
	return nil
}

// Write sends the entry to syslog at INFO level.
func (s *SyslogWriter) Write(entry Entry) error {
	if s.w == nil {
		return nil
	}
	return s.w.Info(Format(entry))
}
