# VM Image & Kernel Guide

The `pkg/image` package manages VM disk images, kernel parameters, and cloud-init configuration drives for Cloud Hypervisor workloads.

## Disk Image Management

### Supported Formats

| Format | Extension      | Description                |
|--------|----------------|----------------------------|
| `raw`  | `.img`, `.raw`| Raw disk image (default)   |
| `qcow2`| `.qcow2`       | QEMU copy-on-write v2      |
| `vhdx` | `.vhdx`        | Hyper-V virtual disk       |
| `vmdk` | `.vmdk`        | VMware virtual disk        |

### Detecting format

```go
format := image.DetectFormat("/var/lib/vm/disk.qcow2")
// format == image.FormatQCOW2
```

### Validating an image

```go
img := &image.Image{
    Path:     "/var/lib/vm/ubuntu.raw",
    Format:   image.FormatRaw,
    Readonly: false,
    Direct:   true,
    ExpectedSHA256: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
}

if err := img.Validate(); err != nil {
    if image.IsValidationError(err) {
        // Image path missing, unsupported format, or checksum mismatch
    }
}
```

`Validate()` performs the following checks:
1. Resolves the path to absolute
2. Verifies the file exists and is not a directory
3. Auto-detects format from extension if empty
4. Verifies the format is supported
5. Computes and compares SHA-256 when `ExpectedSHA256` is set

### Computing and verifying SHA-256

```go
// Compute checksum
sum, err := image.ComputeSHA256("/var/lib/vm/disk.raw")

// Verify against known checksum
err := image.VerifySHA256("/var/lib/vm/disk.raw", "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855")
```

## Kernel Command Line

### Parsing and validating

```go
cmdline, err := image.ParseCmdline("root=/dev/vda1 console=ttyS0,115200 quiet")
if err != nil {
    log.Fatal(err)
}

// Access parameters
root := cmdline.Get("root")   // "/dev/vda1"
hasQuiet := cmdline.Has("quiet") // true

// Modify
cmdline.Set("root", "/dev/vdb1")
cmdline.AddFlag("nomodeset")

// Validate semantics (root device, IP addresses, etc.)
if err := cmdline.Validate(); err != nil {
    log.Fatal(err)
}

// Rebuild string
fmt.Println(cmdline.String())
// root=/dev/vdb1 console=ttyS0,115200 quiet nomodeset
```

### Quoted values

Values containing spaces may be quoted with single or double quotes:

```go
cmdline, _ := image.ParseCmdline(`cmdline="initrd=/boot/initrd.img console=tty0"`)
fmt.Println(cmdline.Get("cmdline"))
// initrd=/boot/initrd.img console=tty0
```

### Validation rules

- Keys must match `[a-zA-Z_][a-zA-Z0-9_.\-]*`
- `root=` must start with `/dev/`, `UUID=`, or `LABEL=`
- `ip=` must be a valid IP address or CIDR notation

## Cloud-Init Config Drives

### Creating a NoCloud datasource

```go
ci := &image.CloudInit{
    InstanceID: "i-12345",
    Hostname:   "web-01",
    UserData: `#cloud-config
users:
  - name: admin
    sudo: ALL=(ALL) NOPASSWD:ALL
    ssh_authorized_keys:
      - ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI...
`,
    NetworkConfig: `version: 2
ethernets:
  eth0:
    dhcp4: true
`,
}

if err := ci.Validate(); err != nil {
    log.Fatal(err)
}

// Write NoCloud datasource files
if err := ci.WriteConfigDrive("/var/lib/cloud-init/seed"); err != nil {
    log.Fatal(err)
}
// Creates:
//   /var/lib/cloud-init/seed/user-data
//   /var/lib/cloud-init/seed/meta-data
//   /var/lib/cloud-init/seed/network-config (optional)
```

### Attaching to a VM

```go
// Create an Image descriptor for the config drive
seedImg := ci.ToImage("/var/lib/cloud-init/seed")

// Pass to vmm.DiskConfig
disk := vmm.DiskConfig{
    Path:     seedImg.Path,
    Readonly: true,
}
```

### Custom meta-data

If `MetaData` is empty it is auto-generated from `InstanceID` and `Hostname`. To provide custom meta-data:

```go
ci := &image.CloudInit{
    InstanceID: "i-1",
    Hostname:   "vm1",
    UserData:   "#cloud-config\n",
    MetaData: `instance-id: i-1
local-hostname: vm1
public-keys:
  - ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI...
`,
}
```

## Error Types

All validation errors are `*image.ValidationError`:

```go
err := img.Validate()
if ve, ok := err.(*image.ValidationError); ok {
    fmt.Printf("field=%s message=%s\n", ve.Field, ve.Message)
}
```

Use `image.IsValidationError(err)` for type checking.

## Design Decisions

1. **Standard library only** — No external dependencies for image parsing, SHA-256, or cloud-init generation.
2. **Format detection by extension** — Simple and reliable for the formats CH supports. Magic-byte detection is not needed because operators typically know their image formats.
3. **Quote-aware cmdline parser** — Kernel command lines frequently contain quoted values (e.g. `ip="10.0.0.1::10.0.0.1:255.255.255.0::eth0:off"`). The parser respects single and double quotes and preserves spaces inside them.
4. **Semantic validation** — Beyond syntax checking, `Cmdline.Validate()` enforces well-known rules (root device prefix, IP format) to catch configuration errors early.
5. **NoCloud datasource** — Cloud Hypervisor does not provide a built-in cloud-init datasource. The package generates the standard NoCloud file layout that can be attached as a read-only disk.
6. **Immutable Image after Validate** — `Validate()` resolves the path to absolute and auto-detects the format, leaving the Image ready for use without further mutation.