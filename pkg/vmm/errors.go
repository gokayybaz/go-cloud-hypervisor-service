package vmm

import (
	"context"
	"fmt"

	"github.com/org/ch-api/pkg/logging"
)

// ErrorCode classifies the kind of error returned by the VMM client.
type ErrorCode string

// Error codes returned by the VMM client.
const (
	ErrCodeConnection     ErrorCode = "connection"
	ErrCodeTimeout        ErrorCode = "timeout"
	ErrCodeAPI            ErrorCode = "api"
	ErrCodeRetryExhausted ErrorCode = "retry_exhausted"
	ErrCodeNotFound       ErrorCode = "not_found"
	ErrCodeInvalidRequest ErrorCode = "invalid_request"
	ErrCodeUnknown        ErrorCode = "unknown"
)

// Error is a structured error returned by the VMM client.  It exposes the
// operation that failed, the error code, HTTP status (when applicable), and
// the raw response body for debugging.
type Error struct {
	// Op records the high-level operation that failed, e.g. "Create", "Boot".
	Op string `json:"op,omitempty"`

	// Code classifies the failure mode.
	Code ErrorCode `json:"code"`

	// Status is the HTTP status code when Code is ErrCodeAPI or ErrCodeNotFound.
	Status int `json:"status,omitempty"`

	// Message is a human-readable description.
	Message string `json:"message"`

	// Body is the raw response body for API errors.
	Body string `json:"body,omitempty"`

	// Cause is the underlying error, if any.
	Cause error `json:"-"`
}

// Error implements the error interface.
func (e *Error) Error() string {
	if e.Op != "" {
		if e.Status != 0 {
			return fmt.Sprintf("vmm.%s[%s] (HTTP %d): %s", e.Code, e.Op, e.Status, e.Message)
		}
		return fmt.Sprintf("vmm.%s[%s]: %s", e.Code, e.Op, e.Message)
	}
	if e.Status != 0 {
		return fmt.Sprintf("vmm.%s (HTTP %d): %s", e.Code, e.Status, e.Message)
	}
	return fmt.Sprintf("vmm.%s: %s", e.Code, e.Message)
}

// Unwrap returns the underlying cause of the error.
func (e *Error) Unwrap() error {
	return e.Cause
}

// IsCode returns true if the error (or any error in its chain) is a *Error
// with the given code.
func IsCode(err error, code ErrorCode) bool {
	if err == nil {
		return false
	}
	e, ok := err.(*Error)
	if ok && e.Code == code {
		return true
	}
	return false
}

// IsOp returns true if the error (or any error in its chain) is a *Error
// whose Op field matches the given operation name.
func IsOp(err error, op string) bool {
	if err == nil {
		return false
	}
	e, ok := err.(*Error)
	if ok && e.Op == op {
		return true
	}
	return false
}

// IsNotFound is a convenience helper that reports whether err is a *Error
// with code ErrCodeNotFound.
func IsNotFound(err error) bool {
	return IsCode(err, ErrCodeNotFound)
}

// ---------------------------------------------------------------------------
// Operation-specific helpers
// ---------------------------------------------------------------------------

// IsCreateFailed reports whether err is a *Error for the Create operation.
func IsCreateFailed(err error) bool { return IsOp(err, "Create") }

// IsBootFailed reports whether err is a *Error for the Boot operation.
func IsBootFailed(err error) bool { return IsOp(err, "Boot") }

// IsPauseFailed reports whether err is a *Error for the Pause operation.
func IsPauseFailed(err error) bool { return IsOp(err, "Pause") }

// IsResumeFailed reports whether err is a *Error for the Resume operation.
func IsResumeFailed(err error) bool { return IsOp(err, "Resume") }

// IsShutdownFailed reports whether err is a *Error for the Shutdown operation.
func IsShutdownFailed(err error) bool { return IsOp(err, "Shutdown") }

// IsRebootFailed reports whether err is a *Error for the Reboot operation.
func IsRebootFailed(err error) bool { return IsOp(err, "Reboot") }

// IsDeleteFailed reports whether err is a *Error for the Delete operation.
func IsDeleteFailed(err error) bool { return IsOp(err, "Delete") }

// ---------------------------------------------------------------------------
// Constructor
// ---------------------------------------------------------------------------

// NewError creates a new structured error with an operation context.
func NewError(op string, code ErrorCode, status int, msg string, body string, cause error) *Error {
	return &Error{
		Op:      op,
		Code:    code,
		Status:  status,
		Message: msg,
		Body:    body,
		Cause:   cause,
	}
}

// ---------------------------------------------------------------------------
// No-op logger (used when no logger is provided)
// ---------------------------------------------------------------------------

type noopLogger struct{}

// Info is a no-op.
func (noopLogger) Info(string, ...any) {}
// Error is a no-op.
func (noopLogger) Error(string, ...any) {}
// Debug is a no-op.
func (noopLogger) Debug(string, ...any) {}
// Warn is a no-op.
func (noopLogger) Warn(string, ...any) {}
// WithContext returns the receiver unchanged.
func (l noopLogger) WithContext(_ context.Context) logging.Logger { return l }
// With returns the receiver unchanged.
func (l noopLogger) With(...any) logging.Logger { return l }