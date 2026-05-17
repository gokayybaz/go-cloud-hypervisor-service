package network

import (
	"crypto/rand"
	"fmt"
	"os/exec"
	"strings"
	"sync"
)

// Manager handles per-VM TAP interface allocation and lifecycle.
type Manager struct {
	mu          sync.Mutex
	allocations map[string]*TAPConfig
	nextIndex   int
	hostIface   string
}

// NewManager creates a new network manager for the given host interface.
func NewManager(hostIface string) *Manager {
	return &Manager{
		allocations: make(map[string]*TAPConfig),
		nextIndex:   1,
		hostIface:   hostIface,
	}
}

// Allocate assigns a unique /30 subnet and TAP name to the VM.
func (m *Manager) Allocate(vmID string) (*TAPConfig, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	idx := m.nextIndex
	m.nextIndex++

	tapName := fmt.Sprintf("tap-%d", idx)

	hostIP := fmt.Sprintf("10.100.%d.1", idx)
	vmIP := fmt.Sprintf("10.100.%d.2", idx)
	subnet := fmt.Sprintf("10.100.%d.0/30", idx)

	cfg := &TAPConfig{
		VMID:      vmID,
		TAPName:   tapName,
		HostIP:    hostIP,
		VMIP:      vmIP,
		Subnet:    subnet,
		Gateway:   hostIP,
		DNS:       "8.8.8.8",
		HostIface: m.hostIface,
		MAC:       generateMAC(),
	}

	m.allocations[vmID] = cfg
	return cfg, nil
}

// Release removes the allocation for the given VM.
func (m *Manager) Release(vmID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.allocations, vmID)
	return nil
}

// SetupTAP creates and brings up the TAP interface.
func (m *Manager) SetupTAP(cfg *TAPConfig) error {
	cmds := [][]string{
		{"ip", "tuntap", "add", "dev", cfg.TAPName, "mode", "tap"},
		{"ip", "addr", "add", cfg.HostIP + "/30", "dev", cfg.TAPName},
		{"ip", "link", "set", cfg.TAPName, "up"},
	}
	for _, args := range cmds {
		cmd := exec.Command(args[0], args[1:]...)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("%s: %w: %s", strings.Join(args, " "), err, string(out))
		}
	}
	return nil
}

// TeardownTAP brings down and deletes the TAP interface.
func (m *Manager) TeardownTAP(cfg *TAPConfig) error {
	cmds := [][]string{
		{"ip", "link", "set", cfg.TAPName, "down"},
		{"ip", "tuntap", "del", "dev", cfg.TAPName, "mode", "tap"},
	}
	for _, args := range cmds {
		cmd := exec.Command(args[0], args[1:]...)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("%s: %w: %s", strings.Join(args, " "), err, string(out))
		}
	}
	return nil
}

// SetupNAT configures IP forwarding and iptables masquerade rules.
func (m *Manager) SetupNAT(hostIface string) error {
	cmds := [][]string{
		{"sysctl", "-w", "net.ipv4.ip_forward=1"},
		{"iptables", "-t", "nat", "-A", "POSTROUTING", "-o", hostIface, "-j", "MASQUERADE"},
		{"iptables", "-A", "FORWARD", "-i", hostIface, "-o", "tap-+", "-m", "state", "--state", "RELATED,ESTABLISHED", "-j", "ACCEPT"},
		{"iptables", "-A", "FORWARD", "-i", "tap-+", "-o", hostIface, "-j", "ACCEPT"},
		{"iptables", "-A", "FORWARD", "-i", "tap-+", "-o", "tap-+", "-j", "DROP"},
	}
	for _, args := range cmds {
		cmd := exec.Command(args[0], args[1:]...)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("%s: %w: %s", strings.Join(args, " "), err, string(out))
		}
	}
	return nil
}

// Get returns the TAP configuration for a VM if it exists.
func (m *Manager) Get(vmID string) (*TAPConfig, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cfg, ok := m.allocations[vmID]
	return cfg, ok
}

// generateMAC creates a random locally-administered unicast MAC address.
func generateMAC() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	// Set locally administered bit (bit 1) and clear multicast bit (bit 0).
	b[0] = (b[0] | 0x02) & 0xfe
	return fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x", b[0], b[1], b[2], b[3], b[4], b[5])
}

// AllowDHCP opens firewall ports for DHCP on a TAP interface.
func (m *Manager) AllowDHCP(tapName string) error {
	cmds := [][]string{
		{"ufw", "allow", "in", "on", tapName, "to", "any", "port", "67", "proto", "udp"},
		{"ufw", "allow", "in", "on", tapName, "to", "any", "port", "68", "proto", "udp"},
	}
	for _, args := range cmds {
		if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
			return fmt.Errorf("ufw allow dhcp: %w: %s", err, out)
		}
	}
	return nil
}
