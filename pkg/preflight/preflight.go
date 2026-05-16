package preflight

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"syscall"
)

// Result holds the outcome of a single preflight check.
type Result struct {
	Name    string `json:"name"`
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
}

// Report aggregates all preflight results.
type Report struct {
	Results []Result `json:"results"`
}

// Check runs all preflight checks and returns a report.
func Check() *Report {
	var results []Result

	results = append(results, checkBinary("cloud-hypervisor"))
	results = append(results, checkKernel())
	results = append(results, checkKVM())
	results = append(results, checkCapabilities()...)

	return &Report{Results: results}
}

// HasFailures returns true if any check failed.
func (r *Report) HasFailures() bool {
	for _, res := range r.Results {
		if !res.OK {
			return true
		}
	}
	return false
}

// Failed returns only the failed results.
func (r *Report) Failed() []Result {
	var out []Result
	for _, res := range r.Results {
		if !res.OK {
			out = append(out, res)
		}
	}
	return out
}

// checkBinary verifies that the named binary exists in $PATH and is executable.
func checkBinary(name string) Result {
	path, err := exec.LookPath(name)
	if err != nil {
		return Result{
			Name:    "Binary availability",
			OK:      false,
			Message: fmt.Sprintf("%s not found in $PATH: %v", name, err),
		}
	}

	info, err := os.Stat(path)
	if err != nil {
		return Result{
			Name:    "Binary availability",
			OK:      false,
			Message: fmt.Sprintf("cannot stat %s: %v", path, err),
		}
	}

	mode := info.Mode()
	if mode&0o111 == 0 {
		return Result{
			Name:    "Binary availability",
			OK:      false,
			Message: fmt.Sprintf("%s exists but is not executable", path),
		}
	}

	return Result{
		Name:    "Binary availability",
		OK:      true,
		Message: fmt.Sprintf("found %s at %s", name, path),
	}
}

// checkKernel reads /proc/sys/kernel/osrelease and enforces a minimum version.
func checkKernel() Result {
	data, err := os.ReadFile("/proc/sys/kernel/osrelease")
	if err != nil {
		return Result{
			Name:    "Kernel version",
			OK:      false,
			Message: fmt.Sprintf("cannot read kernel version: %v", err),
		}
	}

	version := strings.TrimSpace(string(data))
	major, minor, ok := parseKernelVersion(version)
	if !ok {
		return Result{
			Name:    "Kernel version",
			OK:      false,
			Message: fmt.Sprintf("unparseable kernel version: %s", version),
		}
	}

	// Cloud Hypervisor upstream recommends Linux 5.10+; Ubuntu 24.04 ships 6.8.
	const minMajor, minMinor = 5, 10
	if major < minMajor || (major == minMajor && minor < minMinor) {
		return Result{
			Name:    "Kernel version",
			OK:      false,
			Message: fmt.Sprintf("kernel %s is too old (need >= %d.%d)", version, minMajor, minMinor),
		}
	}

	return Result{
		Name:    "Kernel version",
		OK:      true,
		Message: fmt.Sprintf("kernel %s satisfies >= %d.%d", version, minMajor, minMinor),
	}
}

var kernelVersionRe = regexp.MustCompile(`^(\d+)\.(\d+)`)

func parseKernelVersion(v string) (major, minor int, ok bool) {
	m := kernelVersionRe.FindStringSubmatch(v)
	if len(m) != 3 {
		return 0, 0, false
	}
	major, _ = strconv.Atoi(m[1])
	minor, _ = strconv.Atoi(m[2])
	return major, minor, true
}

// checkKVM verifies /dev/kvm exists and the current process can read/write it.
func checkKVM() Result {
	const dev = "/dev/kvm"
	info, err := os.Stat(dev)
	if err != nil {
		return Result{
			Name:    "KVM access",
			OK:      false,
			Message: fmt.Sprintf("%s does not exist: %v", dev, err),
		}
	}

	mode := info.Mode()
	if mode&os.ModeDevice == 0 {
		return Result{
			Name:    "KVM access",
			OK:      false,
			Message: fmt.Sprintf("%s is not a device node", dev),
		}
	}

	// Try to open to validate R/W access.
	f, err := os.OpenFile(dev, os.O_RDWR, 0)
	if err != nil {
		return Result{
			Name:    "KVM access",
			OK:      false,
			Message: fmt.Sprintf("cannot open %s: %v", dev, err),
		}
	}
	f.Close()

	return Result{
		Name:    "KVM access",
		OK:      true,
		Message: fmt.Sprintf("%s is present and accessible", dev),
	}
}

// ---------------------------------------------------------------------------
// Socket permission check
// ---------------------------------------------------------------------------

// CheckSocket verifies that a Unix domain socket exists, is a socket, is
// owned by the current user, and is not accessible by group or others.
//
// The check aborts startup with a fatal error if any of these conditions
// are not met.  The expected permission mode is 0700 (rwx------) or stricter.
func CheckSocket(path string) Result {
	if path == "" {
		return Result{
			Name:    "VMM socket permissions",
			OK:      false,
			Message: "socket path is empty",
		}
	}

	info, err := os.Stat(path)
	if err != nil {
		return Result{
			Name:    "VMM socket permissions",
			OK:      false,
			Message: fmt.Sprintf("socket %q does not exist: %v", path, err),
		}
	}

	if info.Mode()&os.ModeSocket == 0 {
		return Result{
			Name:    "VMM socket permissions",
			OK:      false,
			Message: fmt.Sprintf("%q is not a Unix socket (mode: %s)", path, info.Mode()),
		}
	}

	// Verify ownership matches the current effective user.
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return Result{
			Name:    "VMM socket permissions",
			OK:      false,
			Message: fmt.Sprintf("cannot determine socket owner for %q", path),
		}
	}

	uid := os.Getuid()
	if int(stat.Uid) != uid {
		return Result{
			Name:    "VMM socket permissions",
			OK:      false,
			Message: fmt.Sprintf("socket %q is owned by uid %d, expected %d", path, stat.Uid, uid),
		}
	}

	// Verify group and other bits are not set.
	perm := info.Mode().Perm()
	const unwanted = 0o077 // any group or other permission
	if perm&unwanted != 0 {
		return Result{
			Name:    "VMM socket permissions",
			OK:      false,
			Message: fmt.Sprintf("socket %q permissions %04o are too permissive (expected no group/other access)", path, perm),
		}
	}

	return Result{
		Name:    "VMM socket permissions",
		OK:      true,
		Message: fmt.Sprintf("socket %q exists, owned by uid %d, mode %04o", path, uid, perm),
	}
}

// Cloud Hypervisor capabilities typically required for full functionality.
// See https://github.com/cloud-hypervisor/cloud-hypervisor/blob/main/docs/ioctls.md
var requiredCaps = []string{
	"CAP_SYS_ADMIN",
	"CAP_NET_ADMIN",
	"CAP_IPC_LOCK",
	"CAP_SYS_NICE",
	"CAP_SYS_RESOURCE",
	"CAP_SYS_PTRACE",
	"CAP_DAC_READ_SEARCH",
	"CAP_SETUID",
	"CAP_SETGID",
}

// checkCapabilities reads /proc/self/status and verifies required caps.
func checkCapabilities() []Result {
	var results []Result

	caps, err := readProcCaps()
	if err != nil {
		return []Result{{
			Name:    "Capabilities",
			OK:      false,
			Message: fmt.Sprintf("cannot read process capabilities: %v", err),
		}}
	}

	for _, name := range requiredCaps {
		val, ok := capNameToValue[name]
		if !ok {
			continue
		}
		if caps.effective&val == 0 {
			results = append(results, Result{
				Name:    "Capability: " + name,
				OK:      false,
				Message: fmt.Sprintf("missing effective capability %s", name),
			})
		} else {
			results = append(results, Result{
				Name:    "Capability: " + name,
				OK:      true,
				Message: fmt.Sprintf("effective capability %s present", name),
			})
		}
	}

	return results
}

// procCaps holds effective and permitted capability bitmasks.
type procCaps struct {
	effective uint64
	permitted uint64
}

// readProcCaps parses CapEff and CapPrm from /proc/self/status.
func readProcCaps() (procCaps, error) {
	f, err := os.Open("/proc/self/status")
	if err != nil {
		return procCaps{}, err
	}
	defer f.Close()

	var caps procCaps
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "CapEff:") {
			v, err := strconv.ParseUint(strings.TrimSpace(strings.TrimPrefix(line, "CapEff:")), 16, 64)
			if err != nil {
				return procCaps{}, err
			}
			caps.effective = v
		}
		if strings.HasPrefix(line, "CapPrm:") {
			v, err := strconv.ParseUint(strings.TrimSpace(strings.TrimPrefix(line, "CapPrm:")), 16, 64)
			if err != nil {
				return procCaps{}, err
			}
			caps.permitted = v
		}
	}
	if err := scanner.Err(); err != nil {
		return procCaps{}, err
	}
	return caps, nil
}

// capNameToValue maps Linux capability names to their bit values.
var capNameToValue = map[string]uint64{
	"CAP_CHOWN":            1 << 0,
	"CAP_DAC_OVERRIDE":     1 << 1,
	"CAP_DAC_READ_SEARCH":  1 << 2,
	"CAP_FOWNER":           1 << 3,
	"CAP_FSETID":           1 << 4,
	"CAP_KILL":             1 << 5,
	"CAP_SETGID":           1 << 6,
	"CAP_SETUID":           1 << 7,
	"CAP_SETPCAP":          1 << 8,
	"CAP_LINUX_IMMUTABLE":  1 << 9,
	"CAP_NET_BIND_SERVICE": 1 << 10,
	"CAP_NET_BROADCAST":    1 << 11,
	"CAP_NET_ADMIN":        1 << 12,
	"CAP_NET_RAW":          1 << 13,
	"CAP_IPC_LOCK":         1 << 14,
	"CAP_IPC_OWNER":        1 << 15,
	"CAP_SYS_MODULE":       1 << 16,
	"CAP_SYS_RAWIO":        1 << 17,
	"CAP_SYS_CHROOT":       1 << 18,
	"CAP_SYS_PTRACE":       1 << 19,
	"CAP_SYS_PACCT":        1 << 20,
	"CAP_SYS_ADMIN":        1 << 21,
	"CAP_SYS_BOOT":         1 << 22,
	"CAP_SYS_NICE":         1 << 23,
	"CAP_SYS_RESOURCE":     1 << 24,
	"CAP_SYS_TIME":         1 << 25,
	"CAP_SYS_TTY_CONFIG":   1 << 26,
	"CAP_MKNOD":            1 << 27,
	"CAP_LEASE":            1 << 28,
	"CAP_AUDIT_WRITE":      1 << 29,
	"CAP_AUDIT_CONTROL":    1 << 30,
	"CAP_SETFCAP":          1 << 31,
	"CAP_MAC_OVERRIDE":     1 << 32,
	"CAP_MAC_ADMIN":        1 << 33,
	"CAP_SYSLOG":           1 << 34,
	"CAP_WAKE_ALARM":       1 << 35,
	"CAP_BLOCK_SUSPEND":    1 << 36,
	"CAP_AUDIT_READ":       1 << 37,
	"CAP_PERFMON":          1 << 38,
	"CAP_BPF":              1 << 39,
	"CAP_CHECKPOINT_RESTORE": 1 << 40,
}

// Print writes a human-readable report to stdout.
func (r *Report) Print() {
	fmt.Println("=== Preflight Report ===")
	for _, res := range r.Results {
		status := "PASS"
		if !res.OK {
			status = "FAIL"
		}
		fmt.Printf("[%s] %s: %s\n", status, res.Name, res.Message)
	}
	fmt.Println("========================")
}

// PrintFailed writes a structured report of only failed checks.
func (r *Report) PrintFailed() {
	failed := r.Failed()
	if len(failed) == 0 {
		fmt.Println("=== Preflight: all checks passed ===")
		return
	}
	fmt.Println("=== Preflight Failures ===")
	for _, res := range failed {
		fmt.Printf("[FAIL] %s: %s\n", res.Name, res.Message)
	}
	fmt.Println("==========================")
}

// Verify is a convenience function that runs checks and exits non-zero on failure.
func Verify() bool {
	r := Check()
	if r.HasFailures() {
		r.PrintFailed()
		return false
	}
	r.Print()
	return true
}