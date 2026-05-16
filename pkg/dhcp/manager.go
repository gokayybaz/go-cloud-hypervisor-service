package dhcp

import (
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/gokaybaz/go-cloud-hypervisor-service/pkg/logging"
)

// Manager tracks DHCP servers for all VMs.
type Manager struct {
	mu      sync.Mutex
	servers map[string]*Server // keyed by vm-id
	logger  logging.Logger
}

// NewManager creates a new DHCP manager.
func NewManager(logger logging.Logger) *Manager {
	return &Manager{
		servers: make(map[string]*Server),
		logger:  logger,
	}
}

// StartForVM creates and starts a DHCP server for a VM's TAP interface.
func (m *Manager) StartForVM(vmID, iface, hostIP, vmIP string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.servers[vmID]; exists {
		return nil // already running
	}

	serverIP := net.ParseIP(hostIP).To4()
	clientIP := net.ParseIP(vmIP).To4()
	if serverIP == nil || clientIP == nil {
		return fmt.Errorf("invalid IP: host=%s vm=%s", hostIP, vmIP)
	}

	lease := Lease{
		ClientIP: clientIP,
		ServerIP: serverIP,
		Gateway:  serverIP,
		DNS:      net.ParseIP("8.8.8.8").To4(),
		Mask:     net.CIDRMask(30, 32),
		TTL:      12 * time.Hour,
	}

	srv := NewServer(iface, lease, m.logger)
	if err := srv.Start(); err != nil {
		return fmt.Errorf("start dhcp for vm %s: %w", vmID, err)
	}

	m.servers[vmID] = srv
	m.logger.Info("dhcp started for vm", "vm_id", vmID, "iface", iface, "vm_ip", vmIP)
	return nil
}

// StopForVM stops and removes the DHCP server for a VM.
func (m *Manager) StopForVM(vmID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if srv, exists := m.servers[vmID]; exists {
		srv.Stop()
		delete(m.servers, vmID)
		m.logger.Info("dhcp stopped for vm", "vm_id", vmID)
	}
}

// StopAll stops all running DHCP servers.
func (m *Manager) StopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for vmID, srv := range m.servers {
		srv.Stop()
		delete(m.servers, vmID)
		m.logger.Info("dhcp stopped for vm", "vm_id", vmID)
	}
}
