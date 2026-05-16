package middleware

import (
	"net/http"

	"github.com/org/ch-api/pkg/logging"
)

// TraceIDMiddleware injects a trace_id into the request context.  It first
// looks for an existing X-Request-ID header; if absent it generates a new
// random trace ID.  The trace ID is then propagated via the request context
// so that loggers created with logger.WithContext(r.Context()) automatically
// emit a trace_id field.
func TraceIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		traceID := r.Header.Get("X-Request-ID")
		if traceID == "" {
			traceID = logging.GenerateTraceID()
		}
		ctx := logging.WithTraceID(r.Context(), traceID)
		w.Header().Set("X-Request-ID", traceID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// Register applies all application middleware to the root handler.
func Register(next http.Handler) http.Handler {
	return TraceIDMiddleware(next)
}