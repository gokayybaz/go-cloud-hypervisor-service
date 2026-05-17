package imagemanager

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
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

	// Ubuntu 22.04 cloud image: partition 1 starts at sector 227328
	// offset = 227328 * 512 = 116391936 bytes
	const partitionOffset = 227328 * 512

	// Mount the partition directly using offset
	mountCmd := exec.Command("mount",
		"-o", fmt.Sprintf("loop,offset=%d", partitionOffset),
		diskPath, mountPoint)
	if out, err := mountCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("mount: %w: %s", err, out)
	}
	defer exec.Command("umount", mountPoint).Run()

	// Create seed directory
	seedDir := filepath.Join(mountPoint, "var", "lib", "cloud", "seed", "nocloud")
	if err := os.MkdirAll(seedDir, 0755); err != nil {
		return fmt.Errorf("mkdir seed: %w", err)
	}

	// Write seed files
	for name, content := range files {
		path := filepath.Join(seedDir, name)
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			return fmt.Errorf("write %s: %w", name, err)
		}
	}

	// Reset cloud-init instance state so it runs fresh
	instanceDir := filepath.Join(mountPoint, "var", "lib", "cloud", "instances")
	_ = os.RemoveAll(instanceDir)
	instanceLink := filepath.Join(mountPoint, "var", "lib", "cloud", "instance")
	_ = os.Remove(instanceLink)

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
