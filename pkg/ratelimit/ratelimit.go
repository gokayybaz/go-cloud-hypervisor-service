// Package ratelimit provides per-IP and per-token sliding-window rate limiting.
package ratelimit

import (
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/gokaybaz/go-cloud-hypervisor-service/pkg/api/problem"
	"github.com/gokaybaz/go-cloud-hypervisor-service/pkg/auth"
	"github.com/gokaybaz/go-cloud-hypervisor-service/pkg/logging"
)

// ---------------------------------------------------------------------------
// Configuration
// ---------------------------------------------------------------------------

// Config holds tunable rate limiting parameters.
type Config struct {
	Enabled       bool          // master switch
	Window        time.Duration // sliding window size
	IPLimit       int           // requests per window per IP address
	ViewerLimit   int           // requests per window per viewer token
	OperatorLimit int           // requests per window per operator token
	AdminLimit    int           // requests per window per admin token
}

// DefaultConfig returns sensible production defaults.
func DefaultConfig() Config {
	return Config{
		Enabled:       true,
		Window:        time.Minute,
		IPLimit:       100,
		ViewerLimit:   100,
		OperatorLimit: 200,
		AdminLimit:    500,
	}
}

// ---------------------------------------------------------------------------
// Sliding-window counter
// ---------------------------------------------------------------------------

// window tracks request counts for a single key.
type window struct {
	prevCount  int   // requests in the previous window
	currCount  int   // requests in the current window
	currWindow int64 // index of the current window
}

// ---------------------------------------------------------------------------
// Limiter
// ---------------------------------------------------------------------------

// Limiter implements per-IP and per-token sliding-window rate limiting.
type Limiter struct {
	mu       sync.RWMutex
	windows  map[string]*window
	config   Config
	logger   logging.Logger
	stopChan chan struct{}
}

// NewLimiter creates a rate limiter with the given configuration.
func NewLimiter(cfg Config, logger logging.Logger) *Limiter {
	l := &Limiter{
		windows:  make(map[string]*window),
		config:   cfg,
		logger:   logger,
		stopChan: make(chan struct{}),
	}
	go l.cleanup()
	return l
}

// Stop halts the background cleanup goroutine.
func (l *Limiter) Stop() {
	close(l.stopChan)
}

// cleanup removes stale window entries periodically.
func (l *Limiter) cleanup() {
	ticker := time.NewTicker(l.config.Window)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			l.purge()
		case <-l.stopChan:
			return
		}
	}
}

func (l *Limiter) purge() {
	l.mu.Lock()
	defer l.mu.Unlock()

	nowWindow := time.Now().UnixNano() / int64(l.config.Window)
	for k, w := range l.windows {
		if nowWindow > w.currWindow {
			delete(l.windows, k)
		}
	}
}

// ---------------------------------------------------------------------------
// Middleware
// ---------------------------------------------------------------------------

// Middleware returns HTTP middleware that enforces rate limits.
//
// Rules applied in order:
//   1. Per-IP limit is always checked.
//   2. If the request is authenticated (subject in context), the per-token
//      limit for the caller's highest role is also checked.
//   3. A request must pass both limits to proceed.
//
// On rejection the middleware writes 429 Too Many Requests with an
// RFC 7807 Problem Details body and a Retry-After header (seconds).
func (l *Limiter) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !l.config.Enabled {
				next.ServeHTTP(w, r)
				return
			}

			ip := clientIP(r)

			// 1. Per-IP limit.
			if ok, retry := l.check("ip:"+ip, l.config.IPLimit); !ok {
				l.deny(w, r, "ip", ip, retry)
				return
			}

			// 2. Per-token limit (if authenticated).
			if sub, ok := auth.SubjectFromContext(r.Context()); ok {
				limit := l.tokenLimit(r)
				if limit > 0 {
					if ok, retry := l.check("tok:"+sub, limit); !ok {
						l.deny(w, r, "token", sub, retry)
						return
					}
				}
			}

			next.ServeHTTP(w, r)
		})
	}
}

// check evaluates the sliding-window counter for key against limit.
// It returns (allowed, retryAfter).  A limit <= 0 means unlimited.
func (l *Limiter) check(key string, limit int) (bool, time.Duration) {
	if limit <= 0 {
		return true, 0
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	w, ok := l.windows[key]
	if !ok {
		w = &window{}
		l.windows[key] = w
	}

	now := time.Now()
	windowSize := l.config.Window
	currentWindow := now.UnixNano() / int64(windowSize)

	// Roll over to a new window.
	if currentWindow > w.currWindow {
		w.prevCount = w.currCount
		w.currCount = 0
		w.currWindow = currentWindow
	}

	windowStart := time.Unix(0, w.currWindow*int64(windowSize))
	elapsed := now.Sub(windowStart)
	if elapsed > windowSize {
		elapsed = windowSize
	}

	weight := float64(elapsed) / float64(windowSize)
	estimated := float64(w.prevCount)*(1.0-weight) + float64(w.currCount)

	if estimated+1.0 > float64(limit) {
		retryAfter := windowSize - elapsed
		if retryAfter < 0 {
			retryAfter = 0
		}
		return false, retryAfter
	}

	w.currCount++
	return true, 0
}

// deny writes a 429 response with Retry-After and logs the event.
func (l *Limiter) deny(w http.ResponseWriter, r *http.Request, typ, key string, retryAfter time.Duration) {
	secs := int(retryAfter.Seconds())
	if secs < 1 {
		secs = 1
	}
	w.Header().Set("Retry-After", fmt.Sprintf("%d", secs))

	log := l.logger.WithContext(r.Context())
	log.Warn("rate limit exceeded",
		"type", typ,
		"key", key,
		"retry_after_sec", secs,
		"method", r.Method,
		"path", r.URL.Path,
	)

	problem.RateLimitExceeded(r.URL.Path,
		fmt.Sprintf("rate limit exceeded for %s; retry after %d seconds", typ, secs),
		secs,
	).Write(w)
}

// tokenLimit returns the per-token limit based on the caller's highest role.
// Falls back to the IP limit when no roles are present.
func (l *Limiter) tokenLimit(r *http.Request) int {
	roles, ok := auth.RolesFromContext(r.Context())
	if !ok || len(roles) == 0 {
		return l.config.IPLimit
	}

	highest := auth.RoleViewer
	for _, rs := range roles {
		role, parsed := auth.ParseRole(rs)
		if parsed && role > highest {
			highest = role
		}
	}

	switch highest {
	case auth.RoleAdmin:
		return l.config.AdminLimit
	case auth.RoleOperator:
		return l.config.OperatorLimit
	default:
		return l.config.ViewerLimit
	}
}

// clientIP extracts the client IP from the request.
func clientIP(r *http.Request) string {
	// Trust X-Forwarded-For and X-Real-Ip when present.
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		// Use the first (outermost) address.
		if i := len(fwd); i > 0 {
			if idx := indexOf(fwd, ','); idx >= 0 {
				fwd = fwd[:idx]
			}
			fwd = trimSpace(fwd)
			if fwd != "" {
				return fwd
			}
		}
	}
	if real := r.Header.Get("X-Real-Ip"); real != "" {
		return real
	}
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return ip
}

func indexOf(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

func trimSpace(s string) string {
	start := 0
	end := len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t') {
		end--
	}
	return s[start:end]
}
