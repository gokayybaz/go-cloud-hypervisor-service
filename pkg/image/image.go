package image

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Format identifies a VM disk image format supported by Cloud Hypervisor.
type Format string

// FormatVMDK is the vMDK disk image format.
const (
	FormatRaw   Format = "raw"
	FormatQCOW2 Format = "qcow2"
	FormatVHDX  Format = "vhdx"
	FormatVMDK  Format = "vmdk"
)

// SupportedFormats is the complete list of formats CH can boot.
var SupportedFormats = []Format{FormatRaw, FormatQCOW2, FormatVHDX, FormatVMDK}

// IsSupported reports whether f is a known format.
func IsSupported(f Format) bool {
	switch f {
	case FormatRaw, FormatQCOW2, FormatVHDX, FormatVMDK:
		return true
	}
	return false
}

// Image describes a VM disk image.
type Image struct {
	// Path is the absolute or relative path to the image file.
	Path string `json:"path"`

	// Format is the on-disk format.  When empty DetectFormat is used.
	Format Format `json:"format,omitempty"`

	// Readonly marks the image as read-only (e.g. a cloud-init seed ISO).
	Readonly bool `json:"readonly,omitempty"`

	// Direct enables O_DIRECT for the backing file.
	Direct bool `json:"direct,omitempty"`

	// ExpectedSHA256 is the hex-encoded SHA-256 checksum of the image.
	// When non-empty the image is verified before use.
	ExpectedSHA256 string `json:"expected_sha256,omitempty"`
}

// Validate checks the image path, format, and optionally verifies the
// SHA-256 checksum.
func (img *Image) Validate() error {
	if img.Path == "" {
		return &ValidationError{Field: "path", Message: "image path is required"}
	}

	if !filepath.IsAbs(img.Path) {
		abs, err := filepath.Abs(img.Path)
		if err != nil {
			return &ValidationError{Field: "path", Message: fmt.Sprintf("resolve path: %v", err)}
		}
		img.Path = abs
	}

	info, err := os.Stat(img.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return &ValidationError{Field: "path", Message: fmt.Sprintf("image not found: %s", img.Path)}
		}
		return &ValidationError{Field: "path", Message: fmt.Sprintf("stat image: %v", err)}
	}
	if info.IsDir() {
		return &ValidationError{Field: "path", Message: fmt.Sprintf("path is a directory: %s", img.Path)}
	}

	if img.Format == "" {
		img.Format = DetectFormat(img.Path)
	}
	if !IsSupported(img.Format) {
		return &ValidationError{Field: "format", Message: fmt.Sprintf("unsupported format %q", img.Format)}
	}

	if img.ExpectedSHA256 != "" {
		if err := VerifySHA256(img.Path, img.ExpectedSHA256); err != nil {
			return err
		}
	}

	return nil
}

// DetectFormat guesses the image format from the file extension.
func DetectFormat(path string) Format {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".qcow2":
		return FormatQCOW2
	case ".vhdx":
		return FormatVHDX
	case ".vmdk":
		return FormatVMDK
	case ".img", ".raw":
		return FormatRaw
	default:
		return FormatRaw
	}
}

// Size returns the file size in bytes.
func (img *Image) Size() (int64, error) {
	info, err := os.Stat(img.Path)
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

// ---------------------------------------------------------------------------
// ValidationError
// ---------------------------------------------------------------------------

// ValidationError is returned when an image fails structural validation.
type ValidationError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// Error implements the error interface.
func (e *ValidationError) Error() string {
	return fmt.Sprintf("image validation error (%s): %s", e.Field, e.Message)
}

// IsValidationError reports whether err (or any error in its chain) is a
// *ValidationError.
func IsValidationError(err error) bool {
	_, ok := err.(*ValidationError)
	return ok
}