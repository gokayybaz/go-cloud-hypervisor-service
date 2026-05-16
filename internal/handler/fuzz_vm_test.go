package handler

import (
	"encoding/json"
	"testing"
)

// FuzzCreateVMRequest fuzzes JSON decoding and validation of CreateVMRequest.
func FuzzCreateVMRequest(f *testing.F) {
	seed := []string{
		`{"name":"test-vm","cpus":{"boot_vcpus":2,"max_vcpus":4},"memory":{"size":1024},"kernel":{"path":"/boot/vmlinuz"},"disks":[{"path":"/disk.raw"}]}`,
		`{}`,
		`{"name":"","cpus":{"boot_vcpus":0,"max_vcpus":0},"memory":{"size":32},"kernel":{"path":""},"disks":[]}`,
		`{"name":"x","cpus":{"boot_vcpus":1,"max_vcpus":1},"memory":{"size":256},"kernel":{"path":"/boot"},"disks":[{"path":"/d.raw"}],"net":[{"tap":"t0"}]}`,
		`not-json`,
		`{"name":123}`,
		`{"cpus":null,"memory":null,"kernel":null}`,
		`{"disks":[{}]}`,
	}
	for _, s := range seed {
		f.Add([]byte(s))
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		var req CreateVMRequest
		_ = json.Unmarshal(data, &req)
		_ = req.Validate()
	})
}
