package api

import (
	"fmt"
	"net/http"
	"time"
)

// ServerConfig holds tunable parameters for the HTTP server.
type ServerConfig struct {
	Host         string
	Port         string
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	IdleTimeout  time.Duration
}

// DefaultServerConfig returns sensible defaults.
func DefaultServerConfig() ServerConfig {
	return ServerConfig{
		Host:         "",
		Port:         "8080",
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}
}

// NewServer builds an http.Server with the given configuration and handler.
// If host is empty the server listens on all interfaces.
func NewServer(cfg ServerConfig, handler http.Handler) *http.Server {
	addr := fmt.Sprintf("%s:%s", cfg.Host, cfg.Port)
	if cfg.Host == "" {
		addr = ":" + cfg.Port
	}
	return &http.Server{
		Addr:         addr,
		Handler:      handler,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
		IdleTimeout:  cfg.IdleTimeout,
	}
}
