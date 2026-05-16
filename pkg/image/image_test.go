package image

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Image / Format tests
// ---------------------------------------------------------------------------

func TestDetectFormat(t *testing.T) {
	cases := []struct {
		path string
		want Format
	}{
		{"/var/lib/vm/disk.qcow2", FormatQCOW2},
		{"/var/lib/vm/disk.vhdx", FormatVHDX},
		{"/var/lib/vm/disk.vmdk", FormatVMDK},
		{"/var/lib/vm/disk.img", FormatRaw},
		{"/var/lib/vm/disk.raw", FormatRaw},
		{"/var/lib/vm/disk", FormatRaw},
		{"/var/lib/vm/disk.QCOW2", FormatQCOW2},
	}
	for _, c := range cases {
		got := DetectFormat(c.path)
		if got != c.want {
			t.Fatalf("DetectFormat(%q) = %q, want %q", c.path, got, c.want)
		}
	}
}

func TestIsSupported(t *testing.T) {
	for _, f := range SupportedFormats {
		if !IsSupported(f) {
			t.Fatalf("format %q should be supported", f)
		}
	}
	if IsSupported(Format("unknown")) {
		t.Fatal("unknown format should not be supported")
	}
}

func TestImageValidate(t *testing.T) {
	dir := t.TempDir()

	// Missing path.
	img := &Image{}
	if err := img.Validate(); !IsValidationError(err) {
		t.Fatalf("expected validation error for empty path, got %v", err)
	}

	// Nonexistent file.
	img = &Image{Path: "/nonexistent/disk.img"}
	if err := img.Validate(); !IsValidationError(err) {
		t.Fatalf("expected validation error for missing file, got %v", err)
	}

	// Directory instead of file.
	img = &Image{Path: dir}
	if err := img.Validate(); !IsValidationError(err) {
		t.Fatalf("expected validation error for directory, got %v", err)
	}

	// Unsupported format.
	path := filepath.Join(dir, "disk.bin")
	os.WriteFile(path, []byte("data"), 0644)
	img = &Image{Path: path, Format: Format("bin")}
	if err := img.Validate(); !IsValidationError(err) {
		t.Fatalf("expected validation error for unsupported format, got %v", err)
	}

	// Valid raw image with checksum.
	path = filepath.Join(dir, "disk.img")
	data := []byte("valid raw image data")
	os.WriteFile(path, data, 0644)
	h := sha256.New()
	h.Write(data)
	expectedSum := hex.EncodeToString(h.Sum(nil))

	img = &Image{Path: path, ExpectedSHA256: expectedSum}
	if err := img.Validate(); err != nil {
		t.Fatalf("valid image should pass: %v", err)
	}
	if img.Format != FormatRaw {
		t.Fatalf("expected format auto-detected as raw, got %q", img.Format)
	}

	// Wrong checksum.
	img = &Image{Path: path, ExpectedSHA256: "0000000000000000000000000000000000000000000000000000000000000000"}
	if err := img.Validate(); !IsValidationError(err) {
		t.Fatalf("expected validation error for wrong checksum, got %v", err)
	}
}

func TestImageSize(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "disk.img")
	os.WriteFile(path, []byte("12345"), 0644)

	img := &Image{Path: path}
	size, err := img.Size()
	if err != nil {
		t.Fatalf("size: %v", err)
	}
	if size != 5 {
		t.Fatalf("expected size 5, got %d", size)
	}
}

// ---------------------------------------------------------------------------
// SHA-256 tests
// ---------------------------------------------------------------------------

func TestComputeSHA256(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.bin")
	data := []byte("hello world")
	os.WriteFile(path, data, 0644)

	got, err := ComputeSHA256(path)
	if err != nil {
		t.Fatalf("compute: %v", err)
	}

	want := sha256.Sum256(data)
	wantHex := hex.EncodeToString(want[:])
	if got != wantHex {
		t.Fatalf("sha256 mismatch: got %s, want %s", got, wantHex)
	}
}

func TestVerifySHA256(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.bin")
	data := []byte("hello world")
	os.WriteFile(path, data, 0644)

	sum, _ := ComputeSHA256(path)

	if err := VerifySHA256(path, sum); err != nil {
		t.Fatalf("verify correct: %v", err)
	}

	if err := VerifySHA256(path, "bad"); !IsValidationError(err) {
		t.Fatalf("expected validation error for bad checksum, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Kernel cmdline tests
// ---------------------------------------------------------------------------

func TestParseCmdline(t *testing.T) {
	cases := []struct {
		input   string
		want    map[string]string
		wantErr bool
	}{
		{"", map[string]string{}, false},
		{"quiet", map[string]string{"quiet": ""}, false},
		{"root=/dev/vda1", map[string]string{"root": "/dev/vda1"}, false},
		{"root=/dev/vda1 console=ttyS0", map[string]string{"root": "/dev/vda1", "console": "ttyS0"}, false},
		{"ip=10.0.0.2::10.0.0.1:255.255.255.0::eth0:off", map[string]string{"ip": "10.0.0.2::10.0.0.1:255.255.255.0::eth0:off"}, false},
		{"key=\"value with spaces\"", map[string]string{"key": "value with spaces"}, false},
		{"key='single quotes'", map[string]string{"key": "single quotes"}, false},
		{"=value", nil, true},
		{"123bad=value", nil, true},
	}

	for _, c := range cases {
		cl, err := ParseCmdline(c.input)
		if c.wantErr {
			if err == nil {
				t.Fatalf("ParseCmdline(%q) expected error", c.input)
			}
			continue
		}
		if err != nil {
			t.Fatalf("ParseCmdline(%q): %v", c.input, err)
		}
		got := cl.Params()
		if len(got) != len(c.want) {
			t.Fatalf("ParseCmdline(%q) params count mismatch: got %v, want %v", c.input, got, c.want)
		}
		for k, v := range c.want {
			if got[k] != v {
				t.Fatalf("ParseCmdline(%q) param %q = %q, want %q", c.input, k, got[k], v)
			}
		}
	}
}

func TestCmdlineStringRoundTrip(t *testing.T) {
	original := "root=/dev/vda1 console=ttyS0,115200 quiet"
	cl, err := ParseCmdline(original)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got := cl.String()
	if got != original {
		t.Fatalf("round-trip failed: got %q, want %q", got, original)
	}
}

func TestCmdlineGetSet(t *testing.T) {
	cl, _ := ParseCmdline("root=/dev/vda1")
	if cl.Get("root") != "/dev/vda1" {
		t.Fatalf("Get root failed")
	}
	if cl.Get("missing") != "" {
		t.Fatalf("Get missing should return empty")
	}
	if !cl.Has("root") {
		t.Fatal("Has root should be true")
	}
	if cl.Has("missing") {
		t.Fatal("Has missing should be false")
	}

	cl.Set("root", "/dev/vdb1")
	if cl.Get("root") != "/dev/vdb1" {
		t.Fatalf("Set root failed")
	}

	cl.Set("new", "value")
	if cl.Get("new") != "value" {
		t.Fatalf("Set new failed")
	}

	cl.AddFlag("nomodeset")
	if !cl.Has("nomodeset") {
		t.Fatal("AddFlag failed")
	}
}

func TestCmdlineValidate(t *testing.T) {
	cases := []struct {
		input   string
		wantErr bool
	}{
		{"root=/dev/vda1", false},
		{"root=UUID=abc123", false},
		{"root=LABEL=cloudimg", false},
		{"root=sda1", true},
		{"ip=10.0.0.1", false},
		{"ip=10.0.0.1/24", false},
		{"ip=invalid", true},
		{"quiet", false},
	}

	for _, c := range cases {
		cl, err := ParseCmdline(c.input)
		if err != nil {
			t.Fatalf("ParseCmdline(%q): %v", c.input, err)
		}
		err = cl.Validate()
		if c.wantErr {
			if err == nil {
				t.Fatalf("Validate(%q) expected error", c.input)
			}
		} else {
			if err != nil {
				t.Fatalf("Validate(%q): %v", c.input, err)
			}
		}
	}
}

func TestCmdlineQuotedValue(t *testing.T) {
	cl, err := ParseCmdline(`cmdline="initrd=/boot/initrd.img console=ttyS0"`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cl.Get("cmdline") != "initrd=/boot/initrd.img console=ttyS0" {
		t.Fatalf("quoted value mismatch: got %q", cl.Get("cmdline"))
	}
}

// ---------------------------------------------------------------------------
// Cloud-init tests
// ---------------------------------------------------------------------------

func TestCloudInitWriteConfigDrive(t *testing.T) {
	dir := t.TempDir()

	ci := &CloudInit{
		InstanceID: "i-12345",
		Hostname:   "test-vm",
		UserData:   "#cloud-config\nusers:\n  - name: admin\n",
		NetworkConfig: "version: 2\n",
	}

	if err := ci.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}

	outDir := filepath.Join(dir, "seed")
	if err := ci.WriteConfigDrive(outDir); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Check files exist.
	for _, name := range []string{"user-data", "meta-data", "network-config"} {
		path := filepath.Join(outDir, name)
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("missing %s: %v", name, err)
		}
	}

	// Verify meta-data content.
	meta, err := os.ReadFile(filepath.Join(outDir, "meta-data"))
	if err != nil {
		t.Fatalf("read meta-data: %v", err)
	}
	if !strings.Contains(string(meta), "instance-id: i-12345") {
		t.Fatalf("meta-data missing instance-id")
	}
	if !strings.Contains(string(meta), "local-hostname: test-vm") {
		t.Fatalf("meta-data missing hostname")
	}

	// Verify user-data content.
	ud, err := os.ReadFile(filepath.Join(outDir, "user-data"))
	if err != nil {
		t.Fatalf("read user-data: %v", err)
	}
	if !strings.Contains(string(ud), "admin") {
		t.Fatalf("user-data missing admin user")
	}
}

func TestCloudInitValidateMissingFields(t *testing.T) {
	cases := []struct {
		ci      CloudInit
		wantErr string
	}{
		{CloudInit{}, "user_data"},
		{CloudInit{UserData: "data"}, "instance_id"},
		{CloudInit{UserData: "data", InstanceID: "i-1"}, "hostname"},
	}

	for _, c := range cases {
		err := c.ci.Validate()
		if err == nil {
			t.Fatalf("expected validation error for %s", c.wantErr)
		}
		ve, ok := err.(*ValidationError)
		if !ok {
			t.Fatalf("expected *ValidationError, got %T", err)
		}
		if ve.Field != c.wantErr {
			t.Fatalf("expected field %s, got %s", c.wantErr, ve.Field)
		}
	}
}

func TestCloudInitCustomMetaData(t *testing.T) {
	dir := t.TempDir()
	ci := &CloudInit{
		InstanceID: "i-1",
		Hostname:   "vm1",
		UserData:   "#cloud-config\n",
		MetaData:   "custom: true\n",
	}

	outDir := filepath.Join(dir, "seed")
	if err := ci.WriteConfigDrive(outDir); err != nil {
		t.Fatalf("write: %v", err)
	}

	meta, _ := os.ReadFile(filepath.Join(outDir, "meta-data"))
	if string(meta) != "custom: true\n" {
		t.Fatalf("custom meta-data not preserved: %q", string(meta))
	}
}

func TestCloudInitToImage(t *testing.T) {
	ci := &CloudInit{InstanceID: "i-1", Hostname: "vm1", UserData: "data"}
	img := ci.ToImage("/seed")
	if img.Path != "/seed" {
		t.Fatalf("path mismatch")
	}
	if img.Format != FormatRaw {
		t.Fatalf("format should be raw")
	}
	if !img.Readonly {
		t.Fatal("should be readonly")
	}
}

// ---------------------------------------------------------------------------
// ValidationError tests
// ---------------------------------------------------------------------------

func TestValidationError(t *testing.T) {
	e := &ValidationError{Field: "path", Message: "not found"}
	if !IsValidationError(e) {
		t.Fatal("IsValidationError should be true")
	}
	if IsValidationError(fmt.Errorf("other")) {
		t.Fatal("IsValidationError should be false for generic error")
	}
	if e.Error() != "image validation error (path): not found" {
		t.Fatalf("unexpected error string: %s", e.Error())
	}
}