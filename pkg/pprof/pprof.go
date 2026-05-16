// Package pprof exposes Go runtime profiling endpoints on a dedicated
// localhost-only HTTP server.  It is intended for development and debugging
// and should be disabled in production.
package pprof

import (
	"context"
	"fmt"
	"net/http"
	"net/http/pprof"

	"github.com/gokaybaz/go-cloud-hypervisor-service/pkg/logging"
)

// DefaultAddr is the default bind address for the pprof server.
const DefaultAddr = "localhost:6060"

// Server is a localhost-only HTTP server that serves Go pprof endpoints.
// It implements the lifecycle.Component interface for graceful startup
// and shutdown.
type Server struct {
	addr   string
	server *http.Server
	logger logging.Logger
}

// New creates a pprof server bound to localhost:6060 by default.
// The addr parameter can be used to override the bind address in tests.
func New(logger logging.Logger, addr string) *Server {
	if addr == "" {
		addr = DefaultAddr
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)

	return &Server{
		addr:   addr,
		logger: logger,
		server: &http.Server{
			Addr:    addr,
			Handler: mux,
		},
	}
}

// Name implements lifecycle.Component.
func (s *Server) Name() string { return "pprof-server" }

// Start implements lifecycle.Component.  It starts the pprof server in a
// background goroutine.
func (s *Server) Start(_ context.Context) error {
	go func() {
		s.logger.Info("starting pprof server", "addr", s.addr)
		if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			s.logger.Error("pprof server error", "err", err)
		}
	}()
	return nil
}

// Stop implements lifecycle.Component.  It shuts down the pprof server
// gracefully within the provided context deadline.
func (s *Server) Stop(ctx context.Context) error {
	s.logger.Info("shutting down pprof server")
	if err := s.server.Shutdown(ctx); err != nil {
		return fmt.Errorf("pprof server shutdown: %w", err)
	}
	return nil
}
