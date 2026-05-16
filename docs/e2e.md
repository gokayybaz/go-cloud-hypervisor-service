# End-to-End (E2E) Testing

The `e2e` package tests the `vmm.Client` against a **real** Cloud Hypervisor
binary.  These tests verify that the Go client correctly drives CH through its
full lifecycle: create, boot, pause, resume, shutdown, reboot, and delete.

## Prerequisites

1. **Linux host with KVM**  
   Cloud Hypervisor requires `/dev/kvm` and a Linux kernel >= 5.10.

2. **Cloud Hypervisor binary**  
   Download a release binary or build from source:

   ```bash
   # Download latest release (adjust version as needed)
   curl -LO https://github.com/cloud-hypervisor/cloud-hypervisor/releases/download/v41.0/cloud-hypervisor
   chmod +x cloud-hypervisor
   sudo mv cloud-hypervisor /usr/local/bin/
   ```

   Or build from source:

   ```bash
git clone https://github.com/cloud-hypervisor/cloud-hypervisor.git
cd cloud-hypervisor
cargo build --release
sudo cp target/release/cloud-hypervisor /usr/local/bin/
   ```

3. **Kernel image**  
   A compiled `vmlinux` or `bzImage` is required for boot tests.  You can
   download a pre-built CH kernel or build your own:

   ```bash
   # Download a pre-built kernel image (example URL)
   curl -LO https://github.com/cloud-hypervisor/rust-hypervisor-firmware/releases/download/0.4.2/hypervisor-fw
   # Or use a custom vmlinux
   export CH_KERNEL=/path/to/vmlinux
   ```

   The kernel path is read from the `CH_KERNEL` environment variable.

## Running E2E Tests

### Quick start

```bash
# Ensure the binary is in $PATH and a kernel image is available.
export CH_KERNEL=/path/to/vmlinux

go test ./e2e/... -v
```

### Override the binary path

If the binary is not in `$PATH` or you want to test a specific build:

```bash
export CH_BINARY=/home/user/ch/target/release/cloud-hypervisor
export CH_KERNEL=/home/user/vmlinux

go test ./e2e/... -v
```

### Run a single test

```bash
go test ./e2e/... -v -run TestVMMClient_FullLifecycle
```

### Run with timeout

E2E tests start a real VM, so they may need more time than unit tests:

```bash
go test ./e2e/... -v -timeout=120s
```

## Test Coverage

| Test | What it does | Skips when |
|------|--------------|------------|
| `TestVMMClient_Ping` | Health-checks the CH API | Binary absent |
| `TestVMMClient_Version` | Reads the CH build version | Binary absent |
| `TestVMMClient_CreateAndDelete` | Creates and deletes a VM config (no boot) | Binary absent |
| `TestVMMClient_FullLifecycle` | Create → Boot → Info → Pause → Resume → Shutdown → Delete | Binary or kernel absent |
| `TestVMMClient_Reboot` | Create → Boot → Reboot → Shutdown → Delete | Binary or kernel absent |

All tests automatically call `t.Skip` when the CH binary is not found, so
`go test ./...` remains safe to run on CI machines or macOS workstations that
do not have Cloud Hypervisor installed.

## Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `CH_BINARY` | Path to the `cloud-hypervisor` executable | `cloud-hypervisor` (looked up in `$PATH`) |
| `CH_KERNEL` | Path to a compiled kernel image (`vmlinux` or `bzImage`) | *none* |

## Troubleshooting

### "Cloud Hypervisor requires Linux/KVM"

On macOS or Windows the tests skip immediately because CH is Linux-only.  Run
the tests inside a Linux VM or container with KVM nested virtualization
enabled.

### "cloud-hypervisor binary not found"

Either add the binary to `$PATH` or set `CH_BINARY` explicitly.

### "kernel image not found"

Boot tests (`FullLifecycle`, `Reboot`) require a real kernel.  Set `CH_KERNEL`
to a compiled `vmlinux`.  Without it those tests skip, but `Ping`, `Version`,
and `CreateAndDelete` still run.

### VM boot fails with KVM error

Ensure your user has access to `/dev/kvm`:

```bash
ls -la /dev/kvm
sudo usermod -aG kvm $USER
# Log out and back in for group change to take effect.
```

If running inside a VM, verify that nested virtualization is enabled:

```bash
cat /sys/module/kvm_intel/parameters/nested  # should print 1
cat /sys/module/kvm_amd/parameters/nested   # should print 1
```

### Slow tests

E2E tests start and stop a real CH process for every test function.  Each test
waits up to 10 seconds for the Unix socket to appear and up to 60 seconds for
VM lifecycle operations.  If your machine is heavily loaded, increase the Go
test timeout:

```bash
go test ./e2e/... -v -timeout=300s
```

## CI Integration

To run e2e tests in CI:

1. Use a Linux runner with KVM (GitHub Actions `ubuntu-latest` + QEMU action).
2. Install or cache the CH binary.
3. Download or cache a pre-built kernel image.
4. Run:

   ```yaml
   - name: E2E tests
     run: go test ./e2e/... -v -timeout=120s
     env:
       CH_BINARY: ./cloud-hypervisor
       CH_KERNEL: ./vmlinux
   ```

Because tests skip automatically when the binary is missing, it is safe to
include `go test ./...` in a generic CI step that also runs on non-Linux
runners.
