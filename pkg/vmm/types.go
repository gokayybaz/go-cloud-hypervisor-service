package vmm

// VmConfig mirrors a subset of the Cloud Hypervisor REST API VM configuration.
// See https://github.com/cloud-hypervisor/cloud-hypervisor/blob/main/docs/api.md
// for the full schema.
type VmConfig struct {
	CPUs        *CPUConfig        `json:"cpus,omitempty"`
	Memory      *MemoryConfig     `json:"memory,omitempty"`
	Kernel      *KernelConfig     `json:"kernel,omitempty"`
	Disks       []DiskConfig      `json:"disks,omitempty"`
	Net         []NetConfig       `json:"net,omitempty"`
	Console     *ConsoleConfig    `json:"console,omitempty"`
	Serial      *ConsoleConfig    `json:"serial,omitempty"`
	Payload     *PayloadConfig    `json:"payload,omitempty"`
	Balloon     *BalloonConfig    `json:"balloon,omitempty"`
	Rng         *RngConfig        `json:"rng,omitempty"`
}

// CPUConfig holds vCPU configuration.
type CPUConfig struct {
	BootVCPUs    int    `json:"boot_vcpus,omitempty"`
	MaxVCPUs     int    `json:"max_vcpus,omitempty"`
	Topology     *CPUTopology `json:"topology,omitempty"`
	KvmHyperV    bool   `json:"kvm_hyperv,omitempty"`
}

// CPUTopology describes the desired CPU topology.
type CPUTopology struct {
	ThreadsPerCore int `json:"threads_per_core,omitempty"`
	CoresPerDie    int `json:"cores_per_die,omitempty"`
	DiesPerPackage int `json:"dies_per_package,omitempty"`
	Packages       int `json:"packages,omitempty"`
}

// MemoryConfig holds memory configuration.
type MemoryConfig struct {
	Size                int64  `json:"size,omitempty"`
	Mergeable           bool   `json:"mergeable,omitempty"`
	HotplugMethod       string `json:"hotplug_method,omitempty"`
	HotplugSize         int64  `json:"hotplug_size,omitempty"`
	Shared              bool   `json:"shared,omitempty"`
	Hugepages           bool   `json:"hugepages,omitempty"`
	HugepageSize        int64  `json:"hugepage_size,omitempty"`
	Zones               []MemoryZoneConfig `json:"zones,omitempty"`
}

// MemoryZoneConfig describes a guest memory zone.
type MemoryZoneConfig struct {
	ID              string `json:"id,omitempty"`
	Size            int64  `json:"size,omitempty"`
	HostNumaNode    int    `json:"host_numa_node,omitempty"`
	HotplugMethod   string `json:"hotplug_method,omitempty"`
	HotplugSize     int64  `json:"hotplug_size,omitempty"`
	Mergeable       bool   `json:"mergeable,omitempty"`
	Shared          bool   `json:"shared,omitempty"`
	Hugepages       bool   `json:"hugepages,omitempty"`
	HugepageSize    int64  `json:"hugepage_size,omitempty"`
}

// KernelConfig holds the kernel image configuration.
type KernelConfig struct {
	Path string `json:"path"`
}

// DiskConfig describes a disk device.
type DiskConfig struct {
	Path         string `json:"path,omitempty"`
	Readonly     bool   `json:"readonly,omitempty"`
	Direct       bool   `json:"direct,omitempty"`
	Iommu        bool   `json:"iommu,omitempty"`
	NumQueues    int    `json:"num_queues,omitempty"`
	QueueSize    int    `json:"queue_size,omitempty"`
	VhostUser    bool   `json:"vhost_user,omitempty"`
	VhostSocket  string `json:"vhost_socket,omitempty"`
	PciSegment   int    `json:"pci_segment,omitempty"`
	RateLimiterGroup string `json:"rate_limiter_group,omitempty"`
}

// NetConfig describes a network device.
type NetConfig struct {
	Tap            string `json:"tap,omitempty"`
	IP             string `json:"ip,omitempty"`
	Mask           string `json:"mask,omitempty"`
	Mac            string `json:"mac,omitempty"`
	HostMac        string `json:"host_mac,omitempty"`
	Iommu          bool   `json:"iommu,omitempty"`
	NumQueues      int    `json:"num_queues,omitempty"`
	QueueSize      int    `json:"queue_size,omitempty"`
	VhostUser      bool   `json:"vhost_user,omitempty"`
	VhostSocket    string `json:"vhost_socket,omitempty"`
	VhostMode      string `json:"vhost_mode,omitempty"`
	Id             string `json:"id,omitempty"`
	Fd             []int  `json:"fd,omitempty"`
	RateLimiterGroup string `json:"rate_limiter_group,omitempty"`
}

// ConsoleConfig describes a serial or virtio-console device.
type ConsoleConfig struct {
	Mode   string `json:"mode,omitempty"` // "Off", "Tty", "File", "Socket"
	Path   string `json:"path,omitempty"`
	Socket string `json:"socket,omitempty"`
	Iommu  bool   `json:"iommu,omitempty"`
}

// PayloadConfig describes the firmware payload.
type PayloadConfig struct {
	Firmware string `json:"firmware,omitempty"`
	Kernel   string `json:"kernel,omitempty"`
	Cmdline  string `json:"cmdline,omitempty"`
	Initramfs string `json:"initramfs,omitempty"`
}

// BalloonConfig describes a memory balloon device.
type BalloonConfig struct {
	Size         int64 `json:"size,omitempty"`
	DeflateOnOom bool  `json:"deflate_on_oom,omitempty"`
	FreePageReporting bool `json:"free_page_reporting,omitempty"`
}

// RngConfig describes a random number generator device.
type RngConfig struct {
	Src string `json:"src,omitempty"`
}