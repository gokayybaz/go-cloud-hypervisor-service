package preflight

import (
	"net"
	"os"
	"testing"
)

func TestParseKernelVersion(t *testing.T) {
	cases := []struct {
		input       string
		wantMajor   int
		wantMinor   int
		wantOK      bool
	}{
		{"6.8.0-31-generic", 6, 8, true},
		{"5.15.0-100-generic", 5, 15, true},
		{"4.19.0-1-amd64", 4, 19, true},
		{"invalid", 0, 0, false},
		{"", 0, 0, false},
	}

	for _, c := range cases {
		major, minor, ok := parseKernelVersion(c.input)
		if ok != c.wantOK {
			t.Fatalf("parseKernelVersion(%q) ok=%v, want %v", c.input, ok, c.wantOK)
		}
		if !ok {
			continue
		}
		if major != c.wantMajor || minor != c.wantMinor {
			t.Fatalf("parseKernelVersion(%q) = %d.%d, want %d.%d", c.input, major, minor, c.wantMajor, c.wantMinor)
		}
	}
}

func TestReportHasFailures(t *testing.T) {
	pass := &Report{Results: []Result{{Name: "x", OK: true}}}
	if pass.HasFailures() {
		t.Fatal("expected no failures")
	}

	fail := &Report{Results: []Result{{Name: "x", OK: false}}}
	if !fail.HasFailures() {
		t.Fatal("expected failures")
	}
}

func TestReportFailed(t *testing.T) {
	r := &Report{Results: []Result{
		{Name: "a", OK: true},
		{Name: "b", OK: false},
		{Name: "c", OK: false},
	}}
	f := r.Failed()
	if len(f) != 2 {
		t.Fatalf("expected 2 failures, got %d", len(f))
	}
	if f[0].Name != "b" || f[1].Name != "c" {
		t.Fatalf("unexpected failed items: %+v", f)
	}
}

func TestCheckSocketEmptyPath(t *testing.T) {
	res := CheckSocket("")
	if res.OK {
		t.Fatal("expected failure for empty path")
	}
}

func TestCheckSocketNotExist(t *testing.T) {
	res := CheckSocket("/nonexistent/socket.sock")
	if res.OK {
		t.Fatal("expected failure for nonexistent socket")
	}
}

func TestCheckSocketNotASocket(t *testing.T) {
	f, err := os.CreateTemp("", "not-a-socket")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	defer os.Remove(f.Name())
	f.Close()

	res := CheckSocket(f.Name())
	if res.OK {
		t.Fatal("expected failure for non-socket file")
	}
}

func TestCheckSocketWrongOwner(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("skipping owner test when running as root")
	}

	dir := t.TempDir()
	path := dir + "/test.sock"
	
	// Create socket.
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("create socket: %v", err)
	}
	defer l.Close()

	// Change owner to root if possible.  On non-root this will fail,
	// so skip if we can't change it.
	if err := os.Chown(path, 0, 0); err != nil {
		t.Skip("cannot change file owner, skipping")
	}

	res := CheckSocket(path)
	if res.OK {
		t.Fatal("expected failure when socket not owned by current user")
	}
}

func TestCheckSocketTooPermissive(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/test.sock"

	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("create socket: %v", err)
	}
	defer l.Close()

	// Set mode to 0777 (too permissive).
	if err := os.Chmod(path, 0o777); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	res := CheckSocket(path)
	if res.OK {
		t.Fatal("expected failure when socket permissions are too permissive")
	}
}

func TestCheckSocketValid(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/test.sock"

	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("create socket: %v", err)
	}
	defer l.Close()

	// Set mode to 0700 (owner only).
	if err := os.Chmod(path, 0o700); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	res := CheckSocket(path)
	if !res.OK {
		t.Fatalf("expected success, got: %s", res.Message)
	}
}

func TestCheckSocketStrict0600(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/test.sock"

	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("create socket: %v", err)
	}
	defer l.Close()

	// Set mode to 0600 (owner read/write only).
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	res := CheckSocket(path)
	if !res.OK {
		t.Fatalf("expected success, got: %s", res.Message)
	}
}