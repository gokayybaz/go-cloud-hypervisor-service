package sshkey

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/crypto/ssh"
)

// Manager generates and stores ED25519 SSH keypairs for VMs.
type Manager struct {
	basePath string // e.g. /var/lib/ch-api/keys
}

// NewManager creates a new SSH key manager.
func NewManager(basePath string) *Manager {
	return &Manager{basePath: basePath}
}

// KeyPair holds the generated SSH key material.
type KeyPair struct {
	PrivateKeyPEM []byte // PEM encoded private key
	PublicKey     string // authorized_keys format
}

// Generate creates an ED25519 keypair for a VM and saves to disk.
func (m *Manager) Generate(vmID string) (*KeyPair, error) {
	if err := os.MkdirAll(m.basePath, 0700); err != nil {
		return nil, fmt.Errorf("create key dir: %w", err)
	}

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate key: %w", err)
	}

	// Marshal private key to PEM
	privPEM, err := marshalPrivateKey(priv)
	if err != nil {
		return nil, fmt.Errorf("marshal private key: %w", err)
	}

	// Marshal public key to authorized_keys format
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return nil, fmt.Errorf("marshal public key: %w", err)
	}
	pubStr := string(ssh.MarshalAuthorizedKey(sshPub))

	// Save to disk
	privPath := filepath.Join(m.basePath, vmID+".pem")
	pubPath := filepath.Join(m.basePath, vmID+".pub")

	if err := os.WriteFile(privPath, privPEM, 0600); err != nil {
		return nil, fmt.Errorf("write private key: %w", err)
	}
	if err := os.WriteFile(pubPath, []byte(pubStr), 0644); err != nil {
		return nil, fmt.Errorf("write public key: %w", err)
	}

	return &KeyPair{
		PrivateKeyPEM: privPEM,
		PublicKey:     pubStr,
	}, nil
}

// GetPrivateKey reads the private key for a VM.
func (m *Manager) GetPrivateKey(vmID string) ([]byte, error) {
	path := filepath.Join(m.basePath, vmID+".pem")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read private key: %w", err)
	}
	return data, nil
}

// GetPublicKey reads the public key for a VM.
func (m *Manager) GetPublicKey(vmID string) (string, error) {
	path := filepath.Join(m.basePath, vmID+".pub")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read public key: %w", err)
	}
	return string(data), nil
}

// Delete removes keypair for a VM.
func (m *Manager) Delete(vmID string) error {
	for _, ext := range []string{".pem", ".pub"} {
		path := filepath.Join(m.basePath, vmID+ext)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("delete key: %w", err)
		}
	}
	return nil
}

func marshalPrivateKey(key ed25519.PrivateKey) ([]byte, error) {
	block, err := ssh.MarshalPrivateKey(key, "")
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(block), nil
}
