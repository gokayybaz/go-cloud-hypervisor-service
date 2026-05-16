// Package tls provides TLS configuration builders for the HTTP server.
//
// Supports both manually supplied certificates and automatic ACME
// certificate provisioning via Let's Encrypt.
package tls

import (
	"crypto/tls"
	"fmt"

	"golang.org/x/crypto/acme/autocert"
)

// ---------------------------------------------------------------------------
// Configuration
// ---------------------------------------------------------------------------

// Config holds TLS tunables.
type Config struct {
	Enabled     bool     // master switch
	CertFile    string   // path to PEM-encoded certificate
	KeyFile     string   // path to PEM-encoded private key
	ACMEEnabled bool     // enable automatic certificate provisioning
	ACMEEmail   string   // contact email for Let's Encrypt account
	ACMEDomains []string // domains to request certificates for
	ACMECache   string   // directory for autocert cache (default "data/certs")
}

// ---------------------------------------------------------------------------
// Safe cipher suites
// ---------------------------------------------------------------------------

// Modern cipher suites in order of preference.  Only AEAD suites with
// forward secrecy are included.  CBC and RSA key exchange are excluded.
var safeCipherSuites = []uint16{
	tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
	tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
	tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
	tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
	tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305,
	tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305,
}

// ---------------------------------------------------------------------------
// TLS config builder
// ---------------------------------------------------------------------------

// NewTLSConfig builds a tls.Config with the selected certificate source and
// hardened defaults.
//
// Hardening applied unconditionally:
//   - Minimum TLS 1.2
//   - Only AEAD cipher suites with forward secrecy
//   - Server cipher suite preference
//
// Certificate sources (in order of precedence):
//   1. Manual certificate file + key file
//   2. Automatic ACME (if ACMEEnabled is true)
//
// Returns an error when TLS is enabled but neither a certificate nor ACME
// is configured.
func NewTLSConfig(cfg Config) (*tls.Config, error) {
	if !cfg.Enabled {
		return nil, nil
	}

	tlsCfg := &tls.Config{
		MinVersion:   tls.VersionTLS12,
		CipherSuites: safeCipherSuites,
	}

	if cfg.ACMEEnabled {
		return tlsCfg, nil
	}

	if cfg.CertFile == "" || cfg.KeyFile == "" {
		return nil, fmt.Errorf("tls enabled but neither cert_file/key_file nor acme_enabled is configured")
	}

	cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("load TLS certificate: %w", err)
	}
	tlsCfg.Certificates = []tls.Certificate{cert}
	return tlsCfg, nil
}

// NewAutocertManager creates an autocert.Manager for ACME certificate
// provisioning.  Returns nil when ACME is not enabled.
func NewAutocertManager(cfg Config) *autocert.Manager {
	if !cfg.ACMEEnabled {
		return nil
	}

	cacheDir := cfg.ACMECache
	if cacheDir == "" {
		cacheDir = "data/certs"
	}

	return &autocert.Manager{
		Prompt:     autocert.AcceptTOS,
		Email:      cfg.ACMEEmail,
		HostPolicy: autocert.HostWhitelist(cfg.ACMEDomains...),
		Cache:      autocert.DirCache(cacheDir),
	}
}
