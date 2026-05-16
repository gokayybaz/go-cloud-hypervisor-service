package tls

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// generateTestCert creates a temporary self-signed certificate and key file.
func generateTestCert(t *testing.T) (certFile, keyFile string) {
	t.Helper()

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{"Test Org"},
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}

	dir := t.TempDir()

	certFile = filepath.Join(dir, "cert.pem")
	certOut, err := os.Create(certFile)
	if err != nil {
		t.Fatalf("create cert file: %v", err)
	}
	pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	certOut.Close()

	keyFile = filepath.Join(dir, "key.pem")
	keyOut, err := os.Create(keyFile)
	if err != nil {
		t.Fatalf("create key file: %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	pem.Encode(keyOut, &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	keyOut.Close()

	return certFile, keyFile
}

func TestNewTLSConfigDisabled(t *testing.T) {
	cfg := Config{Enabled: false}
	tlsCfg, err := NewTLSConfig(cfg)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if tlsCfg != nil {
		t.Fatal("expected nil TLS config when disabled")
	}
}

func TestNewTLSConfigManualCert(t *testing.T) {
	certFile, keyFile := generateTestCert(t)
	cfg := Config{Enabled: true, CertFile: certFile, KeyFile: keyFile}

	tlsCfg, err := NewTLSConfig(cfg)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if tlsCfg == nil {
		t.Fatal("expected non-nil TLS config")
	}
	if len(tlsCfg.Certificates) != 1 {
		t.Fatalf("expected 1 certificate, got %d", len(tlsCfg.Certificates))
	}
}

func TestNewTLSConfigACME(t *testing.T) {
	cfg := Config{Enabled: true, ACMEEnabled: true}
	tlsCfg, err := NewTLSConfig(cfg)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if tlsCfg == nil {
		t.Fatal("expected non-nil TLS config")
	}
	if len(tlsCfg.Certificates) != 0 {
		t.Fatalf("expected 0 certificates for ACME mode, got %d", len(tlsCfg.Certificates))
	}
}

func TestNewTLSConfigMissingConfig(t *testing.T) {
	cfg := Config{Enabled: true}
	_, err := NewTLSConfig(cfg)
	if err == nil {
		t.Fatal("expected error when TLS enabled but no cert/ACME configured")
	}
}

func TestNewTLSConfigBadCertFile(t *testing.T) {
	cfg := Config{Enabled: true, CertFile: "/nonexistent/cert.pem", KeyFile: "/nonexistent/key.pem"}
	_, err := NewTLSConfig(cfg)
	if err == nil {
		t.Fatal("expected error for missing certificate files")
	}
}

func TestTLSMinVersion(t *testing.T) {
	certFile, keyFile := generateTestCert(t)
	cfg := Config{Enabled: true, CertFile: certFile, KeyFile: keyFile}

	tlsCfg, err := NewTLSConfig(cfg)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if tlsCfg.MinVersion != tls.VersionTLS12 {
		t.Fatalf("expected TLS 1.2 minimum, got %x", tlsCfg.MinVersion)
	}
}

func TestTLSCipherSuites(t *testing.T) {
	certFile, keyFile := generateTestCert(t)
	cfg := Config{Enabled: true, CertFile: certFile, KeyFile: keyFile}

	tlsCfg, err := NewTLSConfig(cfg)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(tlsCfg.CipherSuites) == 0 {
		t.Fatal("expected cipher suites to be set")
	}
	// Verify no CBC or RSA-KEX suites are present.
	forbidden := []uint16{
		tls.TLS_RSA_WITH_AES_128_CBC_SHA,
		tls.TLS_RSA_WITH_AES_256_CBC_SHA,
		tls.TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA,
		tls.TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA,
	}
	for _, bad := range forbidden {
		for _, cs := range tlsCfg.CipherSuites {
			if cs == bad {
				t.Fatalf("forbidden cipher suite %x found in list", bad)
			}
		}
	}
}

func TestNewAutocertManagerDisabled(t *testing.T) {
	cfg := Config{Enabled: false, ACMEEnabled: false}
	mgr := NewAutocertManager(cfg)
	if mgr != nil {
		t.Fatal("expected nil manager when ACME disabled")
	}
}

func TestNewAutocertManagerEnabled(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{
		Enabled:     true,
		ACMEEnabled: true,
		ACMEEmail:   "admin@example.com",
		ACMEDomains: []string{"example.com"},
		ACMECache:   dir,
	}
	mgr := NewAutocertManager(cfg)
	if mgr == nil {
		t.Fatal("expected non-nil manager when ACME enabled")
	}
}

func TestNewAutocertManagerDefaultCache(t *testing.T) {
	cfg := Config{
		Enabled:     true,
		ACMEEnabled: true,
		ACMEEmail:   "admin@example.com",
		ACMEDomains: []string{"example.com"},
	}
	mgr := NewAutocertManager(cfg)
	if mgr == nil {
		t.Fatal("expected non-nil manager")
	}
	// Default cache dir is "data/certs"; the manager is created successfully
	// even when the directory does not yet exist.
}
