package image

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
)

// ComputeSHA256 returns the hex-encoded SHA-256 checksum of the file at path.
func ComputeSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// VerifySHA256 checks that the SHA-256 checksum of the file at path matches
// expected (hex-encoded).  It returns a *ValidationError on mismatch.
func VerifySHA256(path, expected string) error {
	actual, err := ComputeSHA256(path)
	if err != nil {
		return &ValidationError{Field: "checksum", Message: fmt.Sprintf("compute sha256: %v", err)}
	}
	if actual != expected {
		return &ValidationError{
			Field:   "checksum",
			Message: fmt.Sprintf("sha256 mismatch: expected %s, got %s", expected, actual),
		}
	}
	return nil
}