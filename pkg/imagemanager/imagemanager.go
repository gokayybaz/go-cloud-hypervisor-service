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

// InjectCloudInitSeed writes cloud-init seed files directly into the VM disk
// using the ch-seed-inject helper script.
func (m *Manager) InjectCloudInitSeed(vmID string, files map[string]string) error {
	diskPath := m.VMDiskPath(vmID)

	// Write seed files to a temp directory
	seedDir, err := os.MkdirTemp("", "cloudinit-seed-*")
	if err != nil {
		return fmt.Errorf("create seed dir: %w", err)
	}
	defer os.RemoveAll(seedDir)

	for name, content := range files {
		if err := os.WriteFile(filepath.Join(seedDir, name), []byte(content), 0644); err != nil {
			return fmt.Errorf("write %s: %w", name, err)
		}
	}

	// Call the helper script
	out, err := exec.Command("/usr/local/bin/ch-seed-inject", diskPath, seedDir).CombinedOutput()
	if err != nil {
		return fmt.Errorf("ch-seed-inject: %w: %s", err, out)
	}

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
