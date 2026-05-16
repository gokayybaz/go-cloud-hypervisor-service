package logging

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"os"
	"strings"
	"time"

	"github.com/rs/zerolog"
)

func init() {
	// Adjust caller skip so that the file:line points to the caller of our
	// Logger interface methods, not the zerolog internal frames.
	zerolog.CallerSkipFrameCount = 3
	zerolog.TimeFieldFormat = time.RFC3339Nano
}

// Logger is the application logging interface. It is intentionally narrow so
// that callers do not depend on zerolog directly.
type Logger interface {
	Info(msg string, args ...any)
	Error(msg string, args ...any)
	Debug(msg string, args ...any)
	Warn(msg string, args ...any)

	// WithContext returns a child logger that carries the trace_id found in
	// ctx, if any. Use this inside HTTP handlers or anywhere a context is
	// available to automatically inject trace_id into every log line.
	WithContext(ctx context.Context) Logger

	// With returns a child logger with persistent fields added to every
	// subsequent log line.
	With(fields ...any) Logger
}

// New creates a structured zerolog logger.  The following fields are
// automatically injected into every line:
//
//   - timestamp  — RFC3339Nano
//   - level      — zerolog level
//   - caller     — file:line of the call site
//   - hostname   — os.Hostname()
//
// trace_id is injected automatically when the logger is created via
// WithContext.
func New(level string) Logger {
	lvl := parseLevel(level)

	var w io.Writer = os.Stdout
	if strings.EqualFold(os.Getenv("LOG_FORMAT"), "console") {
		w = zerolog.ConsoleWriter{
			Out:        os.Stdout,
			TimeFormat: time.RFC3339,
		}
	}

	hostname, _ := os.Hostname()

	zl := zerolog.New(w).
		With().
		Timestamp().
		Caller().
		Str("hostname", hostname).
		Logger().
		Level(lvl)

	return &zerologLogger{inner: zl}
}

type zerologLogger struct {
	inner zerolog.Logger
}

// Info logs an informational message.
func (l *zerologLogger) Info(msg string, args ...any) {
	e := l.inner.Info()
	addFields(e, args...)
	e.Msg(msg)
}

// Error logs an error message.
func (l *zerologLogger) Error(msg string, args ...any) {
	e := l.inner.Error()
	addFields(e, args...)
	e.Msg(msg)
}

// Debug logs a debug message.
func (l *zerologLogger) Debug(msg string, args ...any) {
	e := l.inner.Debug()
	addFields(e, args...)
	e.Msg(msg)
}

// Warn logs a warning message.
func (l *zerologLogger) Warn(msg string, args ...any) {
	e := l.inner.Warn()
	addFields(e, args...)
	e.Msg(msg)
}

// WithContext returns a child logger that carries the trace_id from ctx.
func (l *zerologLogger) WithContext(ctx context.Context) Logger {
	traceID := TraceIDFromContext(ctx)
	if traceID == "" {
		return l
	}
	sub := l.inner.With().Str("trace_id", traceID).Logger()
	return &zerologLogger{inner: sub}
}

// With returns a child logger with persistent fields added to every log line.
func (l *zerologLogger) With(fields ...any) Logger {
	m := make(map[string]interface{})
	for i := 0; i < len(fields)-1; i += 2 {
		key, ok := fields[i].(string)
		if !ok {
			continue
		}
		m[key] = fields[i+1]
	}
	sub := l.inner.With().Fields(m).Logger()
	return &zerologLogger{inner: sub}
}

func parseLevel(level string) zerolog.Level {
	switch strings.ToLower(level) {
	case "debug":
		return zerolog.DebugLevel
	case "warn", "warning":
		return zerolog.WarnLevel
	case "error":
		return zerolog.ErrorLevel
	case "fatal":
		return zerolog.FatalLevel
	case "panic":
		return zerolog.PanicLevel
	default:
		return zerolog.InfoLevel
	}
}

func addFields(e *zerolog.Event, args ...any) {
	for i := 0; i < len(args)-1; i += 2 {
		key, ok := args[i].(string)
		if !ok {
			continue
		}
		val := args[i+1]
		switch v := val.(type) {
		case string:
			e.Str(key, v)
		case int:
			e.Int(key, v)
		case int64:
			e.Int64(key, v)
		case bool:
			e.Bool(key, v)
		case error:
			e.AnErr(key, v)
		case float64:
			e.Float64(key, v)
		default:
			e.Interface(key, v)
		}
	}
}

// ---------------------------------------------------------------------------
// Trace-ID helpers
// ---------------------------------------------------------------------------

type traceIDKey struct{}

// WithTraceID returns a derived context that carries traceID.
func WithTraceID(ctx context.Context, traceID string) context.Context {
	return context.WithValue(ctx, traceIDKey{}, traceID)
}

// TraceIDFromContext extracts the trace_id from ctx, or returns "".
func TraceIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(traceIDKey{}).(string)
	return v
}

// GenerateTraceID creates a random 16-byte hex string suitable for use as a
// trace identifier.
func GenerateTraceID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		// Fallback to time-based entropy if crypto/rand fails.
		return hex.EncodeToString([]byte(time.Now().String()))[:16]
	}
	return hex.EncodeToString(b)
}