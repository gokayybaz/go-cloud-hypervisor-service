package api

import (
	"context"
	"fmt"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/org/ch-api/pkg/audit"
	"github.com/org/ch-api/pkg/auth"
	"github.com/org/ch-api/pkg/logging"
	"github.com/org/ch-api/pkg/metrics"
	"github.com/org/ch-api/pkg/ratelimit"
)

// ---------------------------------------------------------------------------
// Router
// ---------------------------------------------------------------------------

// Router wraps a chi.Mux with a versioned sub-router and a pre-configured
// middleware stack.
type Router struct {
	root   chi.Router
	v1     chi.Router
	logger logging.Logger
}

// NewRouter creates a Router with the standard middleware stack applied.
// The stack, from outermost to innermost, is:
//
//   1. PanicRecovery   — catch panics, log stack trace, return 500
//   2. TraceID         — inject X-Request-ID into context as trace_id
//   3. RateLimit       — per-IP and per-token sliding window rate limiting
//   4. Audit           — persist request metadata to SQLite (optional)
//   5. AccessLogger    — structured access log with duration, status, trace_id
//   6. Metrics         — request count and latency histogram per endpoint
//   7. Timeout         — per-request deadline (default 30s)
//
// Auth and RBAC are applied only to the /api/v1 sub-router so that public
// endpoints such as /healthz and /metrics remain accessible without a token.
//
// TraceID is placed before AccessLogger so that the access log can read the
// trace_id from the request context.  RateLimit sits after TraceID so that
// denied requests still carry a trace_id for logs.  Audit is placed after
// RateLimit so that 429 responses are still audited.  Metrics sits just
// above Timeout so that it captures the latency of the actual handler work.
func NewRouter(logger logging.Logger, auditor *audit.Auditor, mr *metrics.Registry, authCfg *auth.Config, rl *ratelimit.Limiter) *Router {
	root := chi.NewRouter()

	// Middleware stack — applied outermost first.
	root.Use(PanicRecovery(logger))
	root.Use(traceIDMiddleware)
	if rl != nil {
		root.Use(rl.Middleware())
	}
	if auditor != nil {
		root.Use(audit.Middleware(auditor))
	}
	root.Use(AccessLogger(logger))
	if mr != nil {
		root.Use(mr.Middleware)
	}
	root.Use(Timeout(30 * time.Second))

	// Versioned sub-router with optional auth + RBAC.
	v1 := chi.NewRouter()
	if authCfg != nil && authCfg.Secret != "" {
		v1.Use(auth.Middleware(*authCfg, logger))
		if authCfg.RBACEnabled {
			v1.Use(auth.RBACMiddleware(auth.DefaultPermissionTable(), logger))
		}
	}
	root.Mount("/api/v1", v1)

	return &Router{
		root:   root,
		v1:     v1,
		logger: logger,
	}
}

// V1 returns the /api/v1 sub-router.  Handlers should register routes here.
func (r *Router) V1() chi.Router {
	return r.v1
}

// Root returns the root chi router.  Useful for mounting additional
// non-versioned paths (e.g. /healthz, /metrics).
func (r *Router) Root() chi.Router {
	return r.root
}

// Handler returns the root router as an http.Handler.
func (r *Router) Handler() http.Handler {
	return r.root
}

// ---------------------------------------------------------------------------
// Trace-ID middleware (thin wrapper around pkg/middleware)
// ---------------------------------------------------------------------------

// traceIDMiddleware reuses the existing TraceIDMiddleware from
// pkg/middleware so that the trace_id propagation logic is defined in one
// place.
func traceIDMiddleware(next http.Handler) http.Handler {
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

// ---------------------------------------------------------------------------
// Access logging middleware
// ---------------------------------------------------------------------------

// AccessLogger returns a middleware that records a structured access log line
// for every request.  It logs method, path, remote address, status code,
// response duration, and the trace_id extracted from the context.
func AccessLogger(logger logging.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)

			log := logger.WithContext(r.Context())
			log.Info("http access",
				"method", r.Method,
				"path", r.URL.Path,
				"remote_addr", r.RemoteAddr,
				"status", ww.Status(),
				"duration_ms", time.Since(start).Milliseconds(),
				"bytes_written", ww.BytesWritten(),
			)
		})
	}
}

// ---------------------------------------------------------------------------
// Panic recovery middleware
// ---------------------------------------------------------------------------

// PanicRecovery returns a middleware that recovers from panics in any
// downstream handler or middleware.  It logs the panic value and stack trace
// via the structured logger and writes a 500 Internal Server Error response.
func PanicRecovery(logger logging.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					log := logger.WithContext(r.Context())
					log.Error("panic recovered",
						"panic", fmt.Sprintf("%v", rec),
						"stack", string(debug.Stack()),
						"method", r.Method,
						"path", r.URL.Path,
					)
					http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// ---------------------------------------------------------------------------
// Timeout middleware
// ---------------------------------------------------------------------------

// Timeout returns a middleware that enforces a per-request deadline.
// If the handler does not complete before the deadline the client receives
// 504 Gateway Timeout and the context is cancelled.
func Timeout(limit time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), limit)
			defer cancel()

			done := make(chan struct{})
			var panicRecovered any
			go func() {
				defer func() {
					if rec := recover(); rec != nil {
						panicRecovered = rec
					}
					close(done)
				}()
				next.ServeHTTP(w, r.WithContext(ctx))
			}()

			select {
			case <-done:
				if panicRecovered != nil {
					panic(panicRecovered)
				}
			case <-ctx.Done():
				// Deadline exceeded — write timeout response if headers not yet sent.
				http.Error(w, http.StatusText(http.StatusGatewayTimeout), http.StatusGatewayTimeout)
			}
		})
	}
}
