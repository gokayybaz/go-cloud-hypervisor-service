package network

// TAPConfig holds the configuration for a VM's TAP interface.
type TAPConfig struct {
	VMID      string // full vm id
	TAPName   string // tap-<first 8 non-dash chars of vmid>
	HostIP    string // e.g. 10.100.1.1
	VMIP      string // e.g. 10.100.1.2
	Subnet    string // e.g. 10.100.1.0/30
	Gateway   string // same as HostIP
	DNS       string // 8.8.8.8
	HostIface string // host NIC, e.g. eth0 — read from config
	MAC       string // auto-generated MAC address
}
