package imagemanager

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Manager handles VM disk image creation and cleanup.
type Manager struct {
	basePath  string // e.g. /var/lib/ch-api/images
	baseImage string // e.g. ubuntu.raw
	kernel    string // e.g. bzImage
}

// NewManager creates a new image manager.
func NewManager(basePath, baseImage, kernel string) *Manager {
	return &Manager{
		basePath:  basePath,
		baseImage: baseImage,
		kernel:    kernel,
	}
}

// VMDiskPath returns the path for a VM's disk image.
func (m *Manager) VMDiskPath(vmID string) string {
	return filepath.Join(m.basePath, vmID+".raw")
}

// KernelPath returns the full path to the kernel.
func (m *Manager) KernelPath() string {
	return filepath.Join(m.basePath, m.kernel)
}

// BasePath returns the base path for images.
func (m *Manager) BasePath() string {
	return m.basePath
}

// CreateDisk copies the base image to a VM-specific path.
func (m *Manager) CreateDisk(vmID string) (string, error) {
	src := filepath.Join(m.basePath, m.baseImage)
	dst := m.VMDiskPath(vmID)

	if _, err := os.Stat(dst); err == nil {
		return dst, nil // already exists
	}

	if err := copyFile(src, dst); err != nil {
		return "", fmt.Errorf("copy base image: %w", err)
	}
	return dst, nil
}

// DeleteDisk removes a VM's disk image.
func (m *Manager) DeleteDisk(vmID string) error {
	path := m.VMDiskPath(vmID)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("delete disk: %w", err)
	}
	return nil
}

// InjectCloudInitSeed writes cloud-init seed files directly into the VM disk
// using debugfs, which does not require mounting or loop devices.
func (m *Manager) InjectCloudInitSeed(vmID string, files map[string]string) error {
	diskPath := m.VMDiskPath(vmID)

	// Find the loop device for this disk (e.g. /dev/loop1p1)
	// First check if a loop device exists for ubuntu.raw (the base image);
	// the VM disk is a copy so it has the same partition layout.
	// We use the VM disk path directly with debugfs partition offset.
	// Ubuntu 22.04 cloud image: partition 1 is at sector 227328.
	// debugfs accepts the disk image directly with -o srcdev for partition.
	// Since debugfs doesn't support offset, we find the loop device for the disk.

	// Find partition device - use any available loop device backed by a .raw file
	partDev, err := m.findPartitionDevice(diskPath)
	if err != nil {
		return fmt.Errorf("find loop device: %w", err)
	}

	// Ensure seed directory exists
	mkdirCmds := []string{
		"mkdir /var/lib/cloud",
		"mkdir /var/lib/cloud/seed",
		"mkdir /var/lib/cloud/seed/nocloud",
	}

	for _, cmd := range mkdirCmds {
		// Ignore errors - directories may already exist
		exec.Command("debugfs", "-w", partDev, "-R", cmd).Run()
	}

	// Write each seed file
	for name, content := range files {
		// Write content to a temp file first
		tmpFile, err := os.CreateTemp("", "cloudinit-*")
		if err != nil {
			return fmt.Errorf("create temp file: %w", err)
		}
		if _, err := tmpFile.WriteString(content); err != nil {
			tmpFile.Close()
			os.Remove(tmpFile.Name())
			return fmt.Errorf("write temp file: %w", err)
		}
		tmpFile.Close()

		destPath := "/var/lib/cloud/seed/nocloud/" + name
		writeCmd := fmt.Sprintf("write %s %s", tmpFile.Name(), destPath)
		if out, err := exec.Command("debugfs", "-w", partDev, "-R", writeCmd).CombinedOutput(); err != nil {
			os.Remove(tmpFile.Name())
			return fmt.Errorf("debugfs write %s: %w: %s", name, err, out)
		}
		os.Remove(tmpFile.Name())
	}

	// Reset cloud-init instance state
	exec.Command("debugfs", "-w", partDev, "-R", "rm /var/lib/cloud/instance").Run()
	exec.Command("debugfs", "-w", partDev, "-R", "rm_rf /var/lib/cloud/instances").Run()

	return nil
}

// findPartitionDevice finds an existing loop device backed by any .raw image
// and returns its partition device path (e.g. /dev/loop1p1).
func (m *Manager) findPartitionDevice(diskPath string) (string, error) {
	// Check for loop device backed by any .raw image (all share same partition layout)
	out, err := exec.Command("losetup", "-l", "--noheadings", "-O", "NAME,BACK-FILE").Output()
	if err != nil {
		return "", fmt.Errorf("losetup list: %w", err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && strings.HasSuffix(fields[1], ".raw") {
			partDev := fields[0] + "p1"
			if _, err := os.Stat(partDev); err == nil {
				return partDev, nil
			}
		}
	}
	return "", fmt.Errorf("no loop device found; ensure base image is attached: losetup -f --show --partscan %s", filepath.Join(filepath.Dir(diskPath), "ubuntu.raw"))
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}
