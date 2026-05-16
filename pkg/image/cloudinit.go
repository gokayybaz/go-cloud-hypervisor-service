package image

import (
	"fmt"
	"os"
	"path/filepath"
)

// ---------------------------------------------------------------------------
// Cloud-init NoCloud datasource
// ---------------------------------------------------------------------------

// CloudInit holds data for a cloud-init NoCloud config drive.
type CloudInit struct {
	// InstanceID is a unique identifier for this VM instance.
	// Defaults to a generated UUID if empty.
	InstanceID string `json:"instance_id,omitempty"`

	// Hostname sets the VM hostname.
	Hostname string `json:"hostname,omitempty"`

	// UserData is raw cloud-init user-data (YAML or #cloud-config).
	UserData string `json:"user_data,omitempty"`

	// MetaData is raw cloud-init meta-data (YAML).
	// When empty it is generated from InstanceID and Hostname.
	MetaData string `json:"meta_data,omitempty"`

	// NetworkConfig is raw cloud-init network-config (YAML v1 or v2).
	NetworkConfig string `json:"network_config,omitempty"`
}

// Validate checks that the cloud-init data is non-empty where required.
func (ci *CloudInit) Validate() error {
	if ci.UserData == "" {
		return &ValidationError{Field: "user_data", Message: "user_data is required"}
	}
	if ci.InstanceID == "" {
		return &ValidationError{Field: "instance_id", Message: "instance_id is required"}
	}
	if ci.Hostname == "" {
		return &ValidationError{Field: "hostname", Message: "hostname is required"}
	}
	return nil
}

// WriteConfigDrive writes the cloud-init files into dir using the NoCloud
// datasource layout:
//
//	<dir>/user-data
//	<dir>/meta-data
//	<dir>/network-config   (optional)
func (ci *CloudInit) WriteConfigDrive(dir string) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}

	meta := ci.MetaData
	if meta == "" {
		meta = fmt.Sprintf("instance-id: %s\nlocal-hostname: %s\n", ci.InstanceID, ci.Hostname)
	}

	if err := os.WriteFile(filepath.Join(dir, "user-data"), []byte(ci.UserData), 0644); err != nil {
		return fmt.Errorf("write user-data: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "meta-data"), []byte(meta), 0644); err != nil {
		return fmt.Errorf("write meta-data: %w", err)
	}
	if ci.NetworkConfig != "" {
		if err := os.WriteFile(filepath.Join(dir, "network-config"), []byte(ci.NetworkConfig), 0644); err != nil {
			return fmt.Errorf("write network-config: %w", err)
		}
	}

	return nil
}

// ToImage creates an Image descriptor pointing at dir, suitable for passing
// to the Cloud Hypervisor DiskConfig with Readonly=true.
func (ci *CloudInit) ToImage(dir string) *Image {
	return &Image{
		Path:     dir,
		Format:   FormatRaw,
		Readonly: true,
	}
}