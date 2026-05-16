package handler

import (
	"encoding/json"
	"testing"
)

// FuzzPatchCPURequest fuzzes JSON decoding of PatchCPURequest.
func FuzzPatchCPURequest(f *testing.F) {
	seed := []string{
		`{"count":4}`,
		`{}`,
		`{"count":0}`,
		`{"count":-1}`,
		`{"count":999999}`,
		`not-json`,
		`{"count":"string"}`,
		`{"count":null}`,
	}
	for _, s := range seed {
		f.Add([]byte(s))
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		var req PatchCPURequest
		_ = json.Unmarshal(data, &req)
	})
}

// FuzzPatchMemoryRequest fuzzes JSON decoding of PatchMemoryRequest.
func FuzzPatchMemoryRequest(f *testing.F) {
	seed := []string{
		`{"size_mb":2048}`,
		`{}`,
		`{"size_mb":32}`,
		`{"size_mb":-1}`,
		`{"size_mb":9999999}`,
		`not-json`,
		`{"size_mb":"string"}`,
		`{"size_mb":null}`,
	}
	for _, s := range seed {
		f.Add([]byte(s))
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		var req PatchMemoryRequest
		_ = json.Unmarshal(data, &req)
	})
}
