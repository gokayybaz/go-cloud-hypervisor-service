package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNewLoaderDefaults(t *testing.T) {
	loader := NewLoader()
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("load defaults: %v", err)
	}

	if cfg.Server.Port != "8080" {
		t.Fatalf("default port: got %q, want 8080", cfg.Server.Port)
	}
	if cfg.Server.Host != "" {
		t.Fatalf("default host: got %q, want empty", cfg.Server.Host)
	}
	if cfg.Server.ReadTimeout != 15*time.Second {
		t.Fatalf("default read_timeout: got %v, want 15s", cfg.Server.ReadTimeout)
	}
	if cfg.Server.WriteTimeout != 15*time.Second {
		t.Fatalf("default write_timeout: got %v, want 15s", cfg.Server.WriteTimeout)
	}
	if cfg.Server.IdleTimeout != 60*time.Second {
		t.Fatalf("default idle_timeout: got %v, want 60s", cfg.Server.IdleTimeout)
	}
	if cfg.Server.ShutdownTimeout != 15*time.Second {
		t.Fatalf("default shutdown_timeout: got %v, want 15s", cfg.Server.ShutdownTimeout)
	}
	if cfg.Log.Level != "info" {
		t.Fatalf("default log level: got %q, want info", cfg.Log.Level)
	}
	if cfg.Log.Format != "json" {
		t.Fatalf("default log format: got %q, want json", cfg.Log.Format)
	}
	if !cfg.Preflight.Enabled {
		t.Fatal("default preflight.enabled should be true")
	}
	if cfg.Preflight.StrictMode {
		t.Fatal("default preflight.strict_mode should be false")
	}
	if cfg.CloudHypervisor.BinaryPath != "cloud-hypervisor" {
		t.Fatalf("default binary_path: got %q, want cloud-hypervisor", cfg.CloudHypervisor.BinaryPath)
	}
	if cfg.CloudHypervisor.SocketPath != "/var/run/ch-api/ch.sock" {
		t.Fatalf("default socket_path: got %q", cfg.CloudHypervisor.SocketPath)
	}
}

func TestLoadFromFile(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	content := `
server:
  port: "9090"
  host: "127.0.0.1"
  read_timeout: "30s"
log:
  level: "debug"
  format: "console"
preflight:
  enabled: false
  strict_mode: true
cloud_hypervisor:
  binary_path: "/usr/local/bin/ch"
  socket_path: "/tmp/ch.sock"
`
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	loader := NewLoader()
	loader.SetConfigFile(configPath)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("load from file: %v", err)
	}

	if cfg.Server.Port != "9090" {
		t.Fatalf("port from file: got %q, want 9090", cfg.Server.Port)
	}
	if cfg.Server.Host != "127.0.0.1" {
		t.Fatalf("host from file: got %q, want 127.0.0.1", cfg.Server.Host)
	}
	if cfg.Server.ReadTimeout != 30*time.Second {
		t.Fatalf("read_timeout from file: got %v, want 30s", cfg.Server.ReadTimeout)
	}
	if cfg.Log.Level != "debug" {
		t.Fatalf("log level from file: got %q, want debug", cfg.Log.Level)
	}
	if cfg.Log.Format != "console" {
		t.Fatalf("log format from file: got %q, want console", cfg.Log.Format)
	}
	if cfg.Preflight.Enabled {
		t.Fatal("preflight.enabled from file should be false")
	}
	if !cfg.Preflight.StrictMode {
		t.Fatal("preflight.strict_mode from file should be true")
	}
	if cfg.CloudHypervisor.BinaryPath != "/usr/local/bin/ch" {
		t.Fatalf("binary_path from file: got %q", cfg.CloudHypervisor.BinaryPath)
	}
	if cfg.CloudHypervisor.SocketPath != "/tmp/ch.sock" {
		t.Fatalf("socket_path from file: got %q", cfg.CloudHypervisor.SocketPath)
	}
}

func TestEnvOverrides(t *testing.T) {
	os.Setenv("CH_API_SERVER_PORT", "7777")
	os.Setenv("CH_API_LOG_LEVEL", "warn")
	os.Setenv("CH_API_PREFLIGHT_ENABLED", "false")
	defer func() {
		os.Unsetenv("CH_API_SERVER_PORT")
		os.Unsetenv("CH_API_LOG_LEVEL")
		os.Unsetenv("CH_API_PREFLIGHT_ENABLED")
	}()

	loader := NewLoader()
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("load with env: %v", err)
	}

	if cfg.Server.Port != "7777" {
		t.Fatalf("env override port: got %q, want 7777", cfg.Server.Port)
	}
	if cfg.Log.Level != "warn" {
		t.Fatalf("env override log level: got %q, want warn", cfg.Log.Level)
	}
	if cfg.Preflight.Enabled {
		t.Fatal("env override preflight.enabled should be false")
	}
}

func TestEnvOverridesFile(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	content := `
server:
  port: "9090"
log:
  level: "debug"
`
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	os.Setenv("CH_API_SERVER_PORT", "8888")
	os.Setenv("CH_API_LOG_LEVEL", "error")
	defer func() {
		os.Unsetenv("CH_API_SERVER_PORT")
		os.Unsetenv("CH_API_LOG_LEVEL")
	}()

	loader := NewLoader()
	loader.SetConfigFile(configPath)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("load with env override file: %v", err)
	}

	// Environment variables should override file values.
	if cfg.Server.Port != "8888" {
		t.Fatalf("env should override file port: got %q, want 8888", cfg.Server.Port)
	}
	if cfg.Log.Level != "error" {
		t.Fatalf("env should override file log level: got %q, want error", cfg.Log.Level)
	}
}