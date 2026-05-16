package audit

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/org/ch-api/pkg/auth"
	"github.com/org/ch-api/pkg/logging"
)

// MaxBodyBytes is the maximum request body size that will be read for hashing.
const MaxBodyBytes = 1 << 20 // 1 MiB

// Middleware returns an HTTP middleware that records every request via the auditor.
func Middleware(auditor *Auditor) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			// Capture body for hashing.
			var bodyHash string
			if r.Body != nil && r.Body != http.NoBody {
				body, err := io.ReadAll(io.LimitReader(r.Body, MaxBodyBytes))
				if err == nil {
					sum := sha256.Sum256(body)
					bodyHash = hex.EncodeToString(sum[:])
					// Restore body so downstream handlers can read it.
					r.Body = io.NopCloser(bytes.NewReader(body))
				}
			}

			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)

			traceID := logging.TraceIDFromContext(r.Context())
			user := r.Header.Get("X-User-ID")
			if sub, ok := auth.SubjectFromContext(r.Context()); ok && sub != "" {
				user = sub
			}
			ip, _, _ := net.SplitHostPort(r.RemoteAddr)
			if ip == "" {
				ip = r.RemoteAddr
			}

			entry := Entry{
				TraceID:    traceID,
				User:       user,
				Method:     r.Method,
				Path:       r.URL.Path,
				BodyHash:   bodyHash,
				DurationMs: time.Since(start).Milliseconds(),
				StatusCode: ww.Status(),
				IP:         ip,
			}

			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_ = auditor.Record(ctx, entry)
		})
	}
}
