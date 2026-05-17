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

// InjectCloudInitSeed mounts the VM disk, writes cloud-init seed files
// into /var/lib/cloud/seed/nocloud/, then unmounts.
// Files map: filename -> content (e.g. "user-data" -> "#cloud-config\n...")
func (m *Manager) InjectCloudInitSeed(vmID string, files map[string]string) error {
	diskPath := m.VMDiskPath(vmID)

	mountPoint, err := os.MkdirTemp("", "vmmount-*")
	if err != nil {
		return fmt.Errorf("create mount point: %w", err)
	}
	defer os.RemoveAll(mountPoint)

	const partitionOffset = 227328 * 512

	// Check if a loop device already exists for this disk
	out, err := exec.Command("losetup", "-j", diskPath).Output()

	var mountDevice string
	if err == nil && len(strings.TrimSpace(string(out))) > 0 {
		// Use existing loop device: extract /dev/loopN from output
		// Output format: "/dev/loop1: [1792]:13 (/path/to/disk)"
		parts := strings.SplitN(strings.TrimSpace(string(out)), ":", 2)
		mountDevice = strings.TrimSpace(parts[0])
		// Mount using existing loop device with offset
		if out, err := exec.Command("mount", "-o",
			fmt.Sprintf("offset=%d", partitionOffset),
			mountDevice, mountPoint).CombinedOutput(); err != nil {
			return fmt.Errorf("mount existing loop: %w: %s", err, out)
		}
	} else {
		// No existing loop device, mount directly with loop option
		if out, err := exec.Command("mount", "-o",
			fmt.Sprintf("loop,offset=%d", partitionOffset),
			diskPath, mountPoint).CombinedOutput(); err != nil {
			return fmt.Errorf("mount: %w: %s", err, out)
		}
	}
	defer exec.Command("umount", mountPoint).Run()

	// Create seed directory
	seedDir := filepath.Join(mountPoint, "var", "lib", "cloud", "seed", "nocloud")
	if err := os.MkdirAll(seedDir, 0755); err != nil {
		return fmt.Errorf("mkdir seed: %w", err)
	}

	// Write seed files
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(seedDir, name), []byte(content), 0644); err != nil {
			return fmt.Errorf("write %s: %w", name, err)
		}
	}

	// Reset cloud-init instance state
	_ = os.RemoveAll(filepath.Join(mountPoint, "var", "lib", "cloud", "instances"))
	_ = os.Remove(filepath.Join(mountPoint, "var", "lib", "cloud", "instance"))

	return nil
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
