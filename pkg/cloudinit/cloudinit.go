package cloudinit

import (
	"fmt"
	"os"
	"path/filepath"

	diskfs "github.com/diskfs/go-diskfs"
	"github.com/diskfs/go-diskfs/disk"
	"github.com/diskfs/go-diskfs/filesystem"
	"github.com/diskfs/go-diskfs/filesystem/iso9660"
)

// Config holds the data needed to generate a cloud-init NoCloud ISO.
type Config struct {
	// InstanceID is a unique identifier for the VM instance.
	InstanceID string
	// Hostname is the VM's hostname.
	Hostname string
	// SSHPublicKey is the authorized_keys format public key.
	SSHPublicKey string
	// OutputPath is where the ISO file will be written.
	OutputPath string
}

// Generate creates a cloud-init NoCloud ISO at cfg.OutputPath.
func Generate(cfg Config) error {
	userData := buildUserData(cfg.SSHPublicKey)
	metaData := buildMetaData(cfg.InstanceID, cfg.Hostname)
	networkConfig := buildNetworkConfig()

	// Create a temp directory to hold the files
	tmpDir, err := os.MkdirTemp("", "cloudinit-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	files := map[string]string{
		"user-data":      userData,
		"meta-data":      metaData,
		"network-config": networkConfig,
	}

	for name, content := range files {
		path := filepath.Join(tmpDir, name)
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			return fmt.Errorf("write %s: %w", name, err)
		}
	}

	return createISO(cfg.OutputPath, tmpDir)
}

func buildUserData(sshPublicKey string) string {
	return fmt.Sprintf(`#cloud-config
users:
  - name: ubuntu
    sudo: ALL=(ALL) NOPASSWD:ALL
    shell: /bin/bash
    ssh_authorized_keys:
      - %s

ssh_pwauth: false
disable_root: false
package_update: false
`, sshPublicKey)
}

func buildMetaData(instanceID, hostname string) string {
	return fmt.Sprintf("instance-id: %s\nlocal-hostname: %s\n", instanceID, hostname)
}

func buildNetworkConfig() string {
	return `version: 2
ethernets:
  enp1s0:
    dhcp4: true
  ens3:
    dhcp4: true
  eth0:
    dhcp4: true
`
}

func createISO(outputPath, srcDir string) error {
	// Calculate size needed (~2 MB, must be multiple of 2048 for ISO9660)
	const isoSize = 2048 * 1024

	// Remove existing file if present
	_ = os.Remove(outputPath)

	// Create the disk image
	mydisk, err := diskfs.Create(outputPath, isoSize, diskfs.SectorSizeDefault)
	if err != nil {
		return fmt.Errorf("create disk: %w", err)
	}

	// Create ISO 9660 filesystem with cidata label
	fspec := disk.FilesystemSpec{
		Partition:   0,
		FSType:      filesystem.TypeISO9660,
		VolumeLabel: "cidata",
	}
	fs, err := mydisk.CreateFilesystem(fspec)
	if err != nil {
		return fmt.Errorf("create filesystem: %w", err)
	}

	// Write files to ISO
	fileNames := []string{"user-data", "meta-data", "network-config"}
	for _, name := range fileNames {
		srcPath := filepath.Join(srcDir, name)
		data, err := os.ReadFile(srcPath)
		if err != nil {
			return fmt.Errorf("read %s: %w", name, err)
		}

		rw, err := fs.OpenFile("/"+name, os.O_CREATE|os.O_RDWR)
		if err != nil {
			return fmt.Errorf("open iso file %s: %w", name, err)
		}
		if _, err := rw.Write(data); err != nil {
			return fmt.Errorf("write iso file %s: %w", name, err)
		}
	}

	// Finalize ISO
	iso, ok := fs.(*iso9660.FileSystem)
	if !ok {
		return fmt.Errorf("not an ISO filesystem")
	}
	return iso.Finalize(iso9660.FinalizeOptions{
		VolumeIdentifier: "cidata",
		RockRidge:        true,
	})
}
